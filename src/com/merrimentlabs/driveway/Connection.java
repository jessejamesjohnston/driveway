// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;

import com.merrimentlabs.driveway.engine.Engine;

import org.json.JSONObject;

import java.io.File;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

/**
 * One run of connecting to a server from its form: Connect (a new server),
 * Save or Test connection (a server being changed). It reports itself as a
 * checklist (found, identity, signed in, folder opened), stops at the step
 * that failed, and asks the person inline when it needs them (the server's
 * identity, a service's setup question, the browser sign-in).
 *
 * A run belongs to no screen: it holds only the application context, and the
 * screen showing it attaches and detaches. A change of theme, text size or
 * display density rebuilds the screen around the same run, so a test, a save
 * or a sign-in in progress carries on and the new screen shows where it is.
 * Leaving the screen cancels it.
 */
final class Connection {
  enum State { PENDING, RUNNING, WAITING, DONE, FAILED }

  /** One line of the checklist. */
  static final class Step {
    final String id;
    State state = State.PENDING;
    /** What went wrong, or a detail of what was done (a fingerprint). */
    String detail;

    Step(String id) {
      this.id = id;
    }
  }

  /** What the run needs from the person right now. */
  enum Ask { NONE, IDENTITY, QUESTION, BROWSER }

  enum Outcome { RUNNING, CONNECTED, SAVED, FAILED, CANCELLED }

  interface Listener {
    void changed();
  }

  static final String FOUND = "found", IDENTITY = "identity", SIGN_IN = "signin", FOLDER = "folder";

  final Servers.Server server;
  final Kinds.Kind kind;
  final boolean isNew;
  final boolean save;
  final boolean signInAgain;
  /** This server signs in through the service's web page (Kinds.browser). */
  final boolean browser;
  final List<Step> steps = new ArrayList<>();

  // Everything below is read on the main thread and written under this lock.
  volatile Ask ask = Ask.NONE;
  volatile Outcome outcome = Outcome.RUNNING;
  /** The failure, in the person's words. */
  volatile String error;
  /** The engine's category for the failure (network, auth, notfound...), or "cancelled". */
  volatile String errorKind;
  /** Identity asked about: the server's fingerprint, and whether it changed. */
  volatile String fingerprint;
  /** The type of the key it presented (ssh-ed25519, ssh-rsa...). */
  volatile String keyType;
  volatile boolean identityChanged;
  /** The setup question being asked (rclone's, as JSON). */
  volatile JSONObject question;
  /** Counts the questions asked: one asked again (the same step, with an error) is a new one. */
  volatile int questionNo;
  /** What the person has typed into the question's answer so far (kept over a rebuild). */
  volatile String draftAnswer = "";
  /** Settings setup made (the sign-in, which drive), for the form; taken once. */
  private JSONObject settled;

  private final Context app;
  private final Handler main = new Handler(Looper.getMainLooper());
  private final Object lock = new Object();
  private Listener listener;
  private Thread thread;
  private boolean cancelled;
  private boolean signInCancelled;
  private volatile boolean signingIn;
  /** The person's answer to ask: Boolean (identity) or String (question, null to cancel). */
  private Object answer;
  private boolean answered;
  /** Setup's ten minutes are up. */
  private boolean timedOut;

  /** The run whose setup is under way (one at a time), or null. */
  private static volatile Connection settingUp;

  /**
   * Whether the run setting up was left (Back, or its screen closed): the
   * sign-in page it asks for must not open. A Cancel can come before the
   * engine's sign-in has anything to stop; this catches it at the page.
   */
  static boolean setupLeft() {
    Connection c = settingUp;
    return c != null && c.cancelled();
  }

  Connection(Context c, Servers.Server draft, Kinds.Kind kind, boolean isNew, boolean save, boolean signInAgain) {
    this.app = c.getApplicationContext();
    this.server = draft;
    this.kind = kind;
    this.isNew = isNew;
    this.save = save;
    this.signInAgain = signInAgain;
    this.browser = Kinds.browser(kind, draft.params);
    if (Kinds.main(kind.backend) != null) {
      steps.add(new Step(FOUND));
      if (kind.backend.equals("sftp")) steps.add(new Step(IDENTITY));
    }
    steps.add(new Step(SIGN_IN));
    steps.add(new Step(FOLDER));
  }

  Step step(String id) {
    for (Step s : steps) if (s.id.equals(id)) return s;
    return null;
  }

  boolean running() {
    return outcome == Outcome.RUNNING;
  }

  /** The screen showing this run; it hears every change on the main thread. */
  void attach(Listener l) {
    synchronized (lock) {
      listener = l;
    }
    changed();
  }

  void detach(Listener l) {
    synchronized (lock) {
      if (listener == l) listener = null;
    }
  }

  /** Settings setup made since the screen last asked, or null. */
  JSONObject takeSettled() {
    synchronized (lock) {
      JSONObject s = settled;
      settled = null;
      return s;
    }
  }

  private void changed() {
    main.post(() -> {
      Listener l;
      synchronized (lock) {
        l = listener;
      }
      if (l != null) l.changed();
    });
  }

  void start() {
    thread = new Thread(this::run, "connect " + server.label);
    thread.start();
  }

  /** Leaving the screen, or Back: stops everything; nothing more reaches the engine. */
  void cancel() {
    synchronized (lock) {
      if (outcome == Outcome.RUNNING) outcome = Outcome.CANCELLED;
      cancelled = true;
      answered = true;
      answer = null;
      lock.notifyAll();
    }
    if (signingIn) Engine.configCancel();
    if (thread != null) thread.interrupt();
  }

  /** "Cancel sign-in" while the browser is open: the sign-in step fails, saying so. */
  void cancelSignIn() {
    synchronized (lock) {
      signInCancelled = true;
    }
    if (signingIn) new Thread(Engine::configCancel).start();
  }

  /** "Open the page again": the service's sign-in page, in the browser. */
  void reopenSignIn() {
    String url = Servers.lastSignInUrl;
    if (url == null) return;
    try {
      app.startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
    } catch (RuntimeException ignored) {
    }
  }

  /** The person's answer to the identity question. */
  void trust(boolean yes) {
    answer(yes);
  }

  /** The person's answer to a setup question; null cancels. */
  void answer(Object a) {
    synchronized (lock) {
      if (answered) return;
      answer = a;
      answered = true;
      lock.notifyAll();
    }
  }

  private Object waitFor(Ask what) throws InterruptedException {
    synchronized (lock) {
      if (cancelled) throw new InterruptedException();
      // Setup ran out of time: a question that comes after counts as Cancel.
      if (timedOut && what == Ask.QUESTION) return null;
      answered = false;
      answer = null;
      ask = what;
    }
    changed();
    synchronized (lock) {
      while (!answered) lock.wait();
      ask = Ask.NONE;
      if (cancelled) throw new InterruptedException();
      return answer;
    }
  }

  private void set(String id, State state, String detail) {
    Step s = step(id);
    if (s == null) return;
    synchronized (lock) {
      s.state = state;
      s.detail = detail;
    }
    changed();
  }

  private boolean cancelled() {
    synchronized (lock) {
      return cancelled;
    }
  }

  // The run, on its own thread.

  private void run() {
    Servers servers = Servers.get(app);
    String during = steps.get(0).id;
    try {
      if (kind.backend.equals("sftp")) {
        during = FOUND;
        if (!identity(servers)) return;
      }
      // Services with setup steps of their own (rclone's), including a
      // browser sign-in: run them when adding, or when asked to sign in again.
      boolean setUp = Kinds.main(server.backend) == null && setUpNeeded();
      during = SIGN_IN;
      if (setUp) {
        if (cancelled()) return;
        set(SIGN_IN, State.RUNNING, null);
        if (!setUp()) return;
      }
      // rclone's setup answers (a single-use login token) are never kept.
      Kinds.dropSetupAnswers(kind, server.params);
      synchronized (lock) {
        if (cancelled) return; // the server stays as it was
        servers.push(server);
      }
      SessionService.touch(app);
      Step found = step(FOUND);
      if (found != null && found.state != State.DONE) {
        during = FOUND;
        set(FOUND, State.RUNNING, null);
        // A quick look for the server's port: "Found" ticks on its own when
        // the server answers. (If it doesn't, the engine's own connection
        // below decides, so a server the probe can't see still works.)
        if (reachable()) set(FOUND, State.DONE, null);
      }
      if (cancelled()) return;
      during = SIGN_IN;
      set(SIGN_IN, State.RUNNING, null);
      Engine.check(server.id);
      if (found != null) set(FOUND, State.DONE, null);
      set(SIGN_IN, State.DONE, null);
      if (cancelled()) return;
      during = FOLDER;
      set(FOLDER, State.RUNNING, null);
      if (!server.folder.isEmpty()) Engine.list(server.id, server.folder);
      if (browser || server.params.has("token")) {
        // Checking may have refreshed the sign-in (some services replace
        // the refresh token each time): keep the one the engine has now.
        JSONObject now = new JSONObject(Engine.configValues(server.id));
        if (now.has("token")) server.params.put("token", now.get("token"));
      }
      set(FOLDER, State.DONE, null);
      if (save) {
        // Under the lock: a screen that leaves now either sees it saved or
        // stops it before it is.
        // A failure to keep the settings fails the save, not a checklist step.
        try {
          synchronized (lock) {
            if (cancelled) return;
            servers.save(server);
            // Decided in the same breath: Back after this sees it saved.
            outcome = Outcome.SAVED;
          }
        } catch (Exception e) {
          // What went wrong goes to the log; the form says the settings weren't kept.
          Logs.w("can't keep the settings of " + server.label, e);
          synchronized (lock) {
            error = app.getString(R.string.save_failed);
            errorKind = "save";
            outcome = Outcome.FAILED;
          }
          return;
        }
        changed();
      } else {
        finish(Outcome.CONNECTED);
      }
    } catch (InterruptedException e) {
      finish(Outcome.CANCELLED);
    } catch (Exception e) {
      if (cancelled()) {
        finish(Outcome.CANCELLED);
        return;
      }
      fail(e, during);
    } finally {
      synchronized (lock) {
        ask = Ask.NONE;
      }
      changed();
    }
  }

  private void finish(Outcome o) {
    synchronized (lock) {
      if (outcome == Outcome.RUNNING) outcome = o;
    }
    changed();
  }

  /**
   * Puts a failure at the step it belongs to: can't reach the server is
   * "Found", a refused sign-in is "Signed in", a missing folder is the
   * folder's. Steps before it that were running are done (the server was
   * found if it refused the password).
   */
  private void fail(Exception e, String during) {
    String kindOf = Errors.kind(e);
    String at = during;
    Step found = step(FOUND);
    if (kindOf.equals("network") && found != null && found.state != State.DONE) at = FOUND;
    else if (kindOf.equals("auth")) at = SIGN_IN;
    String message = Errors.explain(app, server, e, true);
    synchronized (lock) {
      boolean before = true;
      for (Step s : steps) {
        if (s.id.equals(at)) {
          before = false;
          s.state = State.FAILED;
          s.detail = message;
        } else if (before) {
          s.state = State.DONE;
        } else if (s.state != State.DONE) {
          s.state = State.PENDING;
        }
      }
      error = message;
      errorKind = kindOf;
      outcome = Outcome.FAILED;
    }
    changed();
  }

  /**
   * Whether the service's own setup runs: for a browser sign-in, when there
   * is no sign-in yet or the person asked to sign in again; otherwise each
   * time (it checks the settings), except where the alternative's
   * credential is used as it is (premiumize.me's API key) or a single-use
   * token was already traded for the service's own access and no new one
   * was given.
   */
  private boolean setUpNeeded() {
    if (browser) return !server.params.has("token") || signInAgain;
    if (kind.skipSetup && Kinds.altPresent(kind, server.params)) return false;
    if (kind.alt != null && kind.alt.makesToken && server.params.has("token")) {
      for (Kinds.Field f : kind.fields) if (f.ephemeral && server.params.has(f.key)) return true;
      return false;
    }
    return true;
  }

  /**
   * SFTP's first connection: the server's fingerprint, to trust before its
   * key is pinned, so a different machine answering at that address later is
   * refused. A changed key is shown as changed; Cancel is the safe default.
   */
  private boolean identity(Servers servers) throws Exception {
    set(FOUND, State.RUNNING, null);
    String host = server.params.optString("host");
    int port = server.params.optInt("port", 22);
    JSONObject key = new JSONObject(Engine.hostKey(host, port));
    set(FOUND, State.DONE, null);
    String line = key.optString("line");
    File pinned = servers.knownHosts(server.id);
    String old = "";
    try {
      if (pinned.exists()) old = new String(Files.readAllBytes(pinned.toPath()), StandardCharsets.UTF_8).trim();
    } catch (Exception ignored) {
    }
    String print = key.optString("fingerprint");
    if (old.equals(line)) {
      set(IDENTITY, State.DONE, print);
      return true;
    }
    synchronized (lock) {
      fingerprint = print;
      keyType = key.optString("type");
      identityChanged = !old.isEmpty() && old.split(" ", 2)[0].equals(line.split(" ", 2)[0]);
    }
    set(IDENTITY, State.WAITING, null);
    Object yes = waitFor(Ask.IDENTITY);
    if (!Boolean.TRUE.equals(yes)) {
      synchronized (lock) {
        errorKind = "untrusted";
        error = null;
        outcome = Outcome.FAILED;
      }
      set(IDENTITY, State.FAILED, null);
      return false;
    }
    // Under the lock: a screen that leaves now puts the old pin back after this, never before.
    synchronized (lock) {
      if (cancelled) throw new InterruptedException();
      servers.pinForTest(server.id, line);
    }
    set(IDENTITY, State.DONE, print);
    return true;
  }

  /**
   * rclone's setup for the service: the form's settings, then its own steps.
   * For a web sign-in, the browser opens and this waits (Cancel sign-in stops
   * it). Questions the service asks (which drive, a code) are asked one at a
   * time. False if it failed or was cancelled.
   */
  private boolean setUp() throws Exception {
    // Setup runs under a scratch name: rclone's setup rewrites the settings
    // as it goes, and a server already in use (Files, an upload) keeps its
    // own until Save.
    String sid = server.id + "-setup";
    SessionService.hold(app);
    // Nobody signs in for longer than this; give the port and the foreground
    // back. (The browser can open at any step: Google Drive asks a question
    // first.) A question left open ends too.
    java.util.concurrent.ScheduledExecutorService timer = java.util.concurrent.Executors.newSingleThreadScheduledExecutor();
    timer.schedule(() -> {
      synchronized (lock) {
        timedOut = true;
      }
      cancelSignIn();
      answer(null);
    }, 10, TimeUnit.MINUTES);
    synchronized (lock) {
      // A screen that left before setup began: none starts (no browser).
      if (cancelled) {
        timer.shutdownNow();
        SessionService.release(app);
        throw new InterruptedException();
      }
      signInCancelled = false;
      signingIn = true;
      settingUp = this;
    }
    // "Open the page again" opens this sign-in's page, never an older one's.
    Servers.lastSignInUrl = null;
    Servers.browserProblem = null;
    try {
      if (browser) {
        synchronized (lock) {
          ask = Ask.BROWSER;
        }
        set(SIGN_IN, State.WAITING, null);
      }
      JSONObject p = new JSONObject(server.params.toString());
      if (signInAgain) p.remove("token");
      String out = Engine.configStart(sid, server.backend, p.toString());
      while (true) {
        JSONObject q = new JSONObject(out);
        if (q.optBoolean("done")) break;
        synchronized (lock) {
          question = q;
          questionNo++;
          draftAnswer = q.optString("default");
        }
        Object a = waitFor(Ask.QUESTION);
        synchronized (lock) {
          question = null;
        }
        if (a == null) {
          synchronized (lock) {
            errorKind = "cancelled";
            error = null;
            outcome = Outcome.FAILED;
          }
          set(SIGN_IN, State.FAILED, null);
          return false;
        }
        if (browser) {
          synchronized (lock) {
            ask = Ask.BROWSER;
          }
          set(SIGN_IN, State.WAITING, null);
        } else {
          set(SIGN_IN, State.RUNNING, null);
        }
        out = Engine.configAnswer(sid, q.optString("state"), (String) a);
      }
      synchronized (lock) {
        ask = Ask.NONE;
      }
      JSONObject values = new JSONObject(Engine.configValues(sid));
      for (java.util.Iterator<String> it = values.keys(); it.hasNext(); ) {
        String key = it.next();
        server.params.put(key, values.get(key));
      }
      Kinds.dropSetupAnswers(kind, server.params);
      // Everything setup settled (the sign-in, and choices like which drive)
      // goes into the form and the draft too: Save after Test keeps it
      // instead of losing it or asking again.
      synchronized (lock) {
        settled = new JSONObject(server.params.toString());
      }
      set(SIGN_IN, State.RUNNING, null);
      return true;
    } catch (Exception e) {
      boolean byPerson;
      synchronized (lock) {
        byPerson = signInCancelled;
      }
      if (cancelled()) throw new InterruptedException();
      boolean expired;
      synchronized (lock) {
        expired = timedOut;
      }
      String noPage = Servers.browserProblem;
      if (!byPerson && noPage != null && browser) {
        // The browser couldn't open the page: say why, at once.
        fail(new Exception("[" + noPage + "] the sign-in page couldn't open"), SIGN_IN);
        return false;
      }
      if (byPerson && expired && browser) {
        // Ten minutes passed with the sign-in page open: say so.
        fail(new Exception("[expired] the sign-in page was left open too long"), SIGN_IN);
        return false;
      }
      if (byPerson) {
        // The person pressed Cancel sign-in: say that, not rclone's own
        // words for a stopped sign-in.
        synchronized (lock) {
          errorKind = "cancelled";
          error = null;
          outcome = Outcome.FAILED;
        }
        set(SIGN_IN, State.FAILED, null);
        return false;
      }
      throw e;
    } finally {
      signingIn = false;
      if (settingUp == this) settingUp = null;
      timer.shutdownNow();
      Engine.removeRemote(sid);
      SessionService.release(app);
      synchronized (lock) {
        if (ask == Ask.BROWSER) ask = Ask.NONE;
      }
    }
  }

  /** Whether the server's port answers within a few seconds (main kinds only). */
  private boolean reachable() {
    String host;
    int port;
    try {
      switch (server.backend) {
        case "smb":
          host = server.params.optString("host");
          port = server.params.optInt("port", 445);
          break;
        case "nfs":
          host = server.params.optString("host");
          port = server.params.optInt("port", 0);
          if (port <= 0) port = 111; // the portmapper
          break;
        case "webdav": {
          Uri u = Uri.parse(server.params.optString("url"));
          host = u.getHost();
          port = u.getPort() > 0 ? u.getPort() : "https".equals(u.getScheme()) ? 443 : 80;
          break;
        }
        default:
          return false;
      }
      if (host == null || host.isEmpty()) return false;
      try (Socket s = new Socket()) {
        s.connect(new InetSocketAddress(host, port), 4000);
        return true;
      }
    } catch (Exception e) {
      return false;
    }
  }
}
