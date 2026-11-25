// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Notification;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;

import com.merrimentlabs.driveway.engine.Engine;

import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.StandardCopyOption;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Uploads what an app saved. The saved bytes stay in a local file until the
 * server has them, so a failed upload loses nothing: a notification says so
 * and offers to try again.
 *
 * Android tells the app its save worked as soon as it closes the file, which
 * is before the upload; this is the only honest way to report a failure
 * that happens later.
 *
 * Every save has a generation, the time the app opened the file. The newest
 * generation of a file always wins: a late close, a retry, or a save resumed
 * after Driveway was stopped can never put an older version over a newer one.
 */
final class Uploads {
  static final String CHANNEL = "uploads";
  static final String EXTRA_SERVER = "server";
  static final String EXTRA_PATH = "path";
  static final String EXTRA_LOCAL = "local";

  /** When this process started: older note-less staging files are leftovers. */
  private static final long PROCESS_START = System.currentTimeMillis();

  private static final ExecutorService pool = Executors.newFixedThreadPool(2);
  private static int running;

  private Uploads() {}

  /** A staged save: its local copy and generation. */
  private static final class Stage {
    final File file;
    final long gen;

    Stage(File file, long gen) {
      this.file = file;
      this.gen = gen;
    }
  }

  /** The newest save of each "serverId:path" not yet on the server. */
  private static final Map<String, Stage> latest = new ConcurrentHashMap<>();
  /** The newest generation handed to an upload, per file. */
  private static final Map<String, Long> queued = new ConcurrentHashMap<>();
  /** When each file's latest save was handed to an upload (System.currentTimeMillis). */
  private static final Map<String, Long> queuedAt = new ConcurrentHashMap<>();
  /**
   * Saves that failed and wait for Try again or Discard: the local copy
   * that failed, per file. It counts only while that copy is still the
   * file's latest save (failing), so a failure recorded just as a newer
   * save was handed over never speaks for the newer one.
   */
  private static final Map<String, File> failed = new ConcurrentHashMap<>();
  /** Why each of those failed, in the person's words (shown on the server's card). */
  private static final Map<String, String> failedWhy = new ConcurrentHashMap<>();
  /** Apps with the file open for writing right now. */
  private static final Map<String, AtomicInteger> writers = new ConcurrentHashMap<>();
  private static final Map<String, Object> locks = new ConcurrentHashMap<>();

  static String key(String serverId, String path) {
    return serverId + ":" + path;
  }

  private static long lastGen;

  /**
   * A new save's generation: the time, but always above every generation
   * before it, here or in a staging note, so a clock that steps back can't
   * make a newer save lose to an older one.
   */
  static synchronized long nextGen() {
    lastGen = Math.max(System.currentTimeMillis(), lastGen + 1);
    return lastGen;
  }

  private static synchronized void sawGen(long gen) {
    lastGen = Math.max(lastGen, gen);
  }

  /** A file for the bytes on their way to a server; kept until they arrive. */
  static File newLocalFile(Context ctx) {
    File dir = dir(ctx);
    dir.mkdirs();
    return new File(dir, UUID.randomUUID().toString());
  }

  private static File dir(Context ctx) {
    return new File(ctx.getNoBackupFilesDir(), "uploads");
  }

  /**
   * The local copy of a save that hasn't reached the server yet, or null.
   * Reads and sizes come from it until it has: an app that saves and reopens
   * at once gets what it saved, not the server's older copy.
   */
  static File pending(String serverId, String path) {
    Stage s = latest.get(key(serverId, path));
    return s != null && s.file.exists() ? s.file : null;
  }

  /** An app opened path for writing (rename and delete wait until it closes). */
  static void opened(String serverId, String path) {
    writers.computeIfAbsent(key(serverId, path), k -> new AtomicInteger()).incrementAndGet();
  }

  static void closed(String serverId, String path) {
    AtomicInteger n = writers.get(key(serverId, path));
    if (n != null) n.decrementAndGet();
  }

  /**
   * An app opened path to replace its contents: from now on reads come from
   * local. (Android tells Driveway an app closed a file only after the app
   * has moved on; registering at open leaves no moment where a reopen would
   * see the server's older copy.) The note says "not ready": if Driveway is
   * stopped before the app finishes, the half-written copy is never sent.
   */
  static void begin(String serverId, String path, File local, long gen) {
    remember(local, serverId, path, gen, false, 0);
    promote(key(serverId, path), local, gen);
  }

  /** Makes local the copy reads see, unless a newer save already is. */
  private static void promote(String key, File local, long gen) {
    latest.compute(key, (k, cur) -> cur == null || cur.gen <= gen || !cur.file.exists() ? new Stage(local, gen) : cur);
  }

  /**
   * Uploads local, saved at generation gen, to path. Uploads to one path run
   * one at a time, and a save older than one already queued is dropped.
   */
  static void upload(Context ctx, String serverId, String path, File local, long gen, Runnable done) {
    Context app = ctx.getApplicationContext();
    String key = key(serverId, path);
    Long q = queued.get(key);
    if (q != null && q > gen) {
      forget(local); // a newer save of the same file is already on its way
      if (done != null) done.run();
      return;
    }
    queued.merge(key, gen, Math::max);
    // When it was first handed over, kept in its note: a save resumed at a
    // later start, or tried again, keeps its place, so one handed over before
    // a move was refused doesn't hold that move's original (movingSince).
    JSONObject was = note(local);
    long handed = was != null && was.optBoolean("ready") && was.optLong("queued") > 0
        ? was.optLong("queued") : System.currentTimeMillis();
    queuedAt.put(key, handed);
    remember(local, serverId, path, gen, true, handed);
    promote(key, local, gen);
    if (failed.remove(key) != null) changed();
    // An earlier failure's notification (Try again, Discard) goes while this
    // save is on its way, including one resumed at start: Discard must not
    // delete a copy that is being sent. A new failure brings it back.
    app.getSystemService(NotificationManager.class).cancel(key.hashCode());
    started(app);
    pool.execute(() -> {
      try {
        synchronized (locks.computeIfAbsent(key, k -> new Object())) {
          if (!local.exists()) return; // sent already (a second Try again)
          Long newest = queued.get(key);
          if (newest != null && newest > gen) {
            forget(local);
            return;
          }
          if (run(app, serverId, path, local, gen)) {
            latest.computeIfPresent(key, (k, cur) -> cur.file.equals(local) ? null : cur);
            sweepMoves(app);
          } else {
            // Only the save of the file as it is now waits for Try again or
            // Discard: an older one that failed while a newer save was being
            // written gives way to it (the newer one goes out when it's done).
            Stage cur = latest.get(key);
            if (cur != null && cur.file.equals(local)) failed.put(key, local);
          }
          changed();
        }
      } finally {
        finished(app);
        if (done != null) done.run();
      }
    });
  }

  // Each local copy has a note beside it saying where it goes, its
  // generation, and whether the app finished writing it, so a save
  // interrupted by Android stopping Driveway is sent the next time it runs.

  private static void remember(File local, String serverId, String path, long gen, boolean ready, long queued) {
    File note = new File(local.getPath() + ".json");
    File tmp = new File(local.getPath() + ".json.tmp");
    try (FileOutputStream out = new FileOutputStream(tmp)) {
      JSONObject n = new JSONObject().put("server", serverId).put("path", path).put("gen", gen).put("ready", ready);
      if (queued > 0) n.put("queued", queued);
      out.write(n.toString().getBytes(StandardCharsets.UTF_8));
      out.getFD().sync();
    } catch (Exception e) {
      Logs.w("can't note where " + local + " goes: " + e);
      return;
    }
    try {
      Files.move(tmp.toPath(), note.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
    } catch (Exception e) {
      Logs.w("can't note where " + local + " goes: " + e);
    }
  }

  static void forget(File local) {
    local.delete();
    new File(local.getPath() + ".json").delete();
  }

  /** The note beside a staged copy, or null. */
  static JSONObject note(File local) {
    try {
      return new JSONObject(new String(Files.readAllBytes(new File(local.getPath() + ".json").toPath()),
          StandardCharsets.UTF_8));
    } catch (Exception e) {
      return null;
    }
  }

  /** The generation of a staged copy: from its note, else when it was written. */
  static long genOf(File local) {
    JSONObject n = note(local);
    return n != null && n.has("gen") ? n.optLong("gen") : local.lastModified();
  }

  /** The server has a save of path: older saves of it still on the device go. */
  private static void retireOlder(Context ctx, String serverId, String path, long gen) {
    File[] notes = dir(ctx).listFiles((d, n) -> n.endsWith(".json"));
    if (notes == null) return;
    for (File note : notes) {
      File f = new File(note.getPath().substring(0, note.getPath().length() - 5));
      JSONObject n = note(f);
      if (n == null || !serverId.equals(n.optString("server")) || !path.equals(n.optString("path"))) continue;
      if (genOf(f) < gen) forget(f);
    }
  }

  /**
   * Gives up on a save that didn't reach the server: the local copy goes, and
   * Driveway shows the server's version again.
   */
  static void discard(Context ctx, String serverId, String path, File local) {
    String key = key(serverId, path);
    latest.computeIfPresent(key, (k, cur) -> cur.file.equals(local) ? null : cur);
    // Only this save's failure: a newer save's stays with it.
    if (failed.remove(key, local)) failedWhy.remove(key);
    // Older saves of the same file that failed too go with it: otherwise the
    // next start would send the newest of them over the server's version.
    // (Older first, this one last, as after an upload.)
    retireOlder(ctx, serverId, path, genOf(local));
    forget(local);
    sweepMoves(ctx);
    ctx.getSystemService(NotificationManager.class).cancel(key.hashCode());
    changed();
  }

  /** Drops a save that never completed on the device (the write failed). */
  static void abandon(String serverId, String path, File local) {
    latest.computeIfPresent(key(serverId, path), (k, cur) -> cur.file.equals(local) ? null : cur);
    forget(local);
  }

  /** settle's answers. */
  static final int SETTLED = 0;
  static final int BUSY = 1;
  static final int FAILED_SAVE = 2;
  /** Another file saved since recentSince hasn't reached its server (on its way, or failed). */
  static final int RECENT = 3;

  /**
   * Moves Driveway refused (Files then copies and deletes), per
   * "serverId:path", with when. Each has a record in the uploads folder too,
   * restored at start (recover): Android can stop Driveway after the copy
   * closes and before Files deletes the original.
   */
  private static final Map<String, Long> refusedMoves = new ConcurrentHashMap<>();
  /** Keeps a refused move's record in step with its entry above. */
  private static final Object moveLock = new Object();
  private static final String MOVE = ".move";
  /**
   * How long after a refused move its deletes count as the end of that
   * move; longer while a save begun since then is still on the device (a
   * big copy can take longer than this to write and upload).
   */
  private static final long MOVE_WINDOW_MS = 10 * 60_000;

  /**
   * Driveway refused to move path itself (to another server, or a folder on
   * a service without a folder rename): Files will copy it, then delete it.
   */
  static void moveRefused(Context ctx, String serverId, String path) {
    moveRefused(ctx, serverId, path, System.currentTimeMillis());
  }

  /** As above, refused at a given time. */
  static void moveRefused(Context ctx, String serverId, String path, long at) {
    String key = key(serverId, path);
    synchronized (moveLock) {
      sweepMoves(ctx);
      refusedMoves.put(key, at);
      File note = moveNote(ctx, key);
      File tmp = new File(note.getPath() + ".tmp");
      dir(ctx).mkdirs();
      try {
        try (FileOutputStream out = new FileOutputStream(tmp)) {
          out.write(new JSONObject().put("move", key).put("at", at).toString().getBytes(StandardCharsets.UTF_8));
          out.getFD().sync();
        }
        Files.move(tmp.toPath(), note.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
      } catch (Exception e) {
        // Still kept while Driveway runs.
        Logs.w("can't note the refused move of " + path + ": " + e);
      }
    }
  }

  /** The record of a refused move of key ("serverId:path"), named after it so a new refusal replaces it. */
  static File moveNote(Context ctx, String key) {
    return new File(dir(ctx), UUID.nameUUIDFromBytes(key.getBytes(StandardCharsets.UTF_8)) + MOVE);
  }

  /**
   * Refused moves whose deletes no longer count as the end of the move (as
   * movingSince counts them) go, with their records.
   */
  private static void sweepMoves(Context ctx) {
    synchronized (moveLock) {
      long window = System.currentTimeMillis() - MOVE_WINDOW_MS;
      refusedMoves.entrySet().removeIf(e -> {
        if (e.getValue() >= window || unsentSince(e.getValue())) return false;
        moveNote(ctx, e.getKey()).delete();
        return true;
      });
    }
  }

  /** A refused move recorded by an earlier run, back in refusedMoves. */
  private static void restoreMove(File note) {
    try {
      JSONObject n = new JSONObject(new String(Files.readAllBytes(note.toPath()), StandardCharsets.UTF_8));
      synchronized (moveLock) {
        refusedMoves.merge(n.getString("move"), n.getLong("at"), Math::max);
      }
    } catch (Exception e) {
      Logs.w("can't read the refused move in " + note + ": " + e);
    }
  }

  /**
   * When a move of path (or of a folder holding it, or of something in it)
   * was refused in the last ten minutes, or before with a save begun since
   * still on the device; Long.MAX_VALUE if none was. Its delete is then the
   * end of a move Files makes by copying (settle).
   */
  static long movingSince(String serverId, String path) {
    String k = key(serverId, path);
    long since = Long.MAX_VALUE;
    long window = System.currentTimeMillis() - MOVE_WINDOW_MS;
    for (Map.Entry<String, Long> e : refusedMoves.entrySet()) {
      String r = e.getKey();
      boolean related = r.equals(k) || k.startsWith(r + "/") || r.startsWith(k + "/");
      if (related && (e.getValue() >= window || unsentSince(e.getValue()))) since = Math.min(since, e.getValue());
    }
    return since;
  }

  /** Whether a save begun or handed to an upload since t hasn't reached its server (as settle counts them). */
  private static boolean unsentSince(long t) {
    for (Map.Entry<String, Stage> e : latest.entrySet()) {
      Stage st = e.getValue();
      if (st.file.exists() && (st.gen >= t || queuedAt.getOrDefault(e.getKey(), 0L) >= t)) return true;
    }
    return false;
  }

  static int settle(String serverId, String path, long timeoutMs) throws InterruptedException {
    return settle(serverId, path, timeoutMs, Long.MAX_VALUE);
  }

  /**
   * Waits until no save to path, or under it if it's a folder, is on its way
   * and no app has it open for writing. Renaming, moving or deleting before
   * then would let the upload land afterwards and bring the old name back.
   * A save that failed and waits for Try again or Discard counts too, with
   * its own answer.
   *
   * Saves of any other file, on any server, handed to an upload since
   * recentSince count too (answer RECENT): when Files moves something
   * Driveway can't move on the server (to another server, or a folder on an
   * object store), it reads it, writes the copy, and deletes the original
   * at once, while the copy is still only on this device. Until the copy
   * has reached its server, the original stays (movingSince).
   */
  static int settle(String serverId, String path, long timeoutMs, long recentSince) throws InterruptedException {
    String exact = key(serverId, path);
    String under = exact + "/";
    long until = System.currentTimeMillis() + timeoutMs;
    while (true) {
      boolean busy = false;
      boolean failing = false;
      boolean recentBusy = false;
      boolean recentFailing = false;
      for (Map.Entry<String, Stage> e : latest.entrySet()) {
        String k = e.getKey();
        if (!e.getValue().file.exists()) continue;
        boolean here = k.equals(exact) || k.startsWith(under);
        // Another file's save since the move began: handed to an upload, or
        // still being written or waiting for its close (begun since then).
        if (!here && queuedAt.getOrDefault(k, 0L) < recentSince && e.getValue().gen < recentSince) continue;
        if (e.getValue().file.equals(failed.get(k))) {
          if (here) failing = true;
          else recentFailing = true;
        } else if (here) {
          busy = true;
        } else {
          recentBusy = true;
        }
      }
      for (Map.Entry<String, AtomicInteger> e : writers.entrySet()) {
        String k = e.getKey();
        if ((k.equals(exact) || k.startsWith(under)) && e.getValue().get() > 0) busy = true;
      }
      if (!busy && !recentBusy) {
        if (failing) return FAILED_SAVE;
        return recentFailing ? RECENT : SETTLED;
      }
      if (System.currentTimeMillis() > until) return busy ? BUSY : RECENT;
      Thread.sleep(100);
    }
  }

  private static boolean recovered;

  /**
   * Sends saves left over from a Driveway that was stopped before they were
   * uploaded: for each file, only the newest finished one. Copies an app was
   * still writing when Driveway stopped are dropped (the server keeps what it
   * had rather than get half a file).
   */
  static synchronized void recover(Context ctx) {
    if (recovered) return;
    recovered = true;
    File[] files = dir(ctx).listFiles();
    if (files == null) return;
    Map<String, File> newest = new HashMap<>();
    Map<String, JSONObject> notes = new HashMap<>();
    for (File f : files) {
      String name = f.getName();
      if (name.endsWith(MOVE)) {
        // Before anything is resumed or deleted: the provider calls this
        // first, and an activity may never start.
        restoreMove(f);
        continue;
      }
      if (name.endsWith(".json") || name.endsWith(".tmp")) continue;
      JSONObject n = note(f);
      sawGen(genOf(f));
      if (n == null) {
        // A copy with no note: an open that failed halfway, from an earlier run.
        if (f.lastModified() < PROCESS_START) forget(f);
        continue;
      }
      String key = key(n.optString("server"), n.optString("path"));
      if (!n.optBoolean("ready")) {
        Stage cur = latest.get(key);
        if (cur == null || !cur.file.equals(f)) {
          Logs.i("dropping an unfinished save of " + n.optString("path"));
          forget(f);
        }
        continue;
      }
      File best = newest.get(key);
      if (best == null || genOf(f) > genOf(best)) {
        if (best != null) forget(best);
        newest.put(key, f);
        notes.put(key, n);
      } else {
        forget(f);
      }
    }
    for (Map.Entry<String, File> e : newest.entrySet()) {
      JSONObject n = notes.get(e.getKey());
      Logs.i("resuming the save of " + n.optString("path"));
      upload(ctx, n.optString("server"), n.optString("path"), e.getValue(), genOf(e.getValue()), null);
    }
    // Once the saves are back in place: a refused move only they still hold
    // stays (a resumed save keeps the time it was first handed over, so it
    // holds only moves refused before that; one noted before Driveway kept
    // that time counts as handed over now).
    sweepMoves(ctx);
  }

  /**
   * Try again on a failed save's notification. Saves left from an earlier
   * run are sorted out first (this may be an old one a newer save replaced),
   * and the notification goes while it tries, so Discard can't be pressed
   * during the upload; a new failure brings it back.
   */
  static void retry(Context ctx, String serverId, String path, File local) {
    recover(ctx);
    if (!local.exists()) return; // sent already, or a newer save replaced it
    JSONObject n = note(local);
    if (n != null && !n.optBoolean("ready")) return; // an app is still writing it
    ctx.getSystemService(NotificationManager.class).cancel(key(serverId, path).hashCode());
    upload(ctx, serverId, path, local, genOf(local), null);
  }

  /**
   * Discard from a server's card: only if that very save still waits (the
   * card may be older than a Try again, or a newer save may have begun).
   */
  static void discardIfFailed(Context ctx, Failed f) {
    String key = key(f.serverId, f.path);
    if (!f.file.equals(failing(key))) return;
    discard(ctx, f.serverId, f.path, f.file);
  }

  /** Whether the last save of path failed and waits for Try again or Discard. */
  static boolean failed(String serverId, String path) {
    return failing(key(serverId, path)) != null;
  }

  /** The latest save of key if it is the one that failed, else null. */
  private static File failing(String key) {
    Stage cur = latest.get(key);
    File f = failed.get(key);
    return cur != null && cur.file.equals(f) ? f : null;
  }

  /** How many saves to the server are still on the device (not on the server yet). */
  static int unsent(Context ctx, String serverId) {
    File[] notes = dir(ctx).listFiles((d, n) -> n.endsWith(".json"));
    int n = 0;
    for (int i = 0; notes != null && i < notes.length; i++) {
      File f = new File(notes[i].getPath().substring(0, notes[i].getPath().length() - 5));
      JSONObject note = note(f);
      if (note != null && serverId.equals(note.optString("server")) && f.exists()) n++;
    }
    return n;
  }

  /** A server was removed: its saves still on the device go with it. */
  static void forgetServer(Context ctx, String serverId) {
    File[] notes = dir(ctx).listFiles((d, n) -> n.endsWith(".json"));
    if (notes == null) return;
    NotificationManager nm = ctx.getSystemService(NotificationManager.class);
    for (File note : notes) {
      File f = new File(note.getPath().substring(0, note.getPath().length() - 5));
      JSONObject n = note(f);
      if (n == null || !serverId.equals(n.optString("server"))) continue;
      String key = key(serverId, n.optString("path"));
      latest.remove(key);
      failed.remove(key);
      failedWhy.remove(key);
      nm.cancel(key.hashCode());
      forget(f);
    }
    changed();
  }

  /** Uploads and reports; true if the server has the file. */
  static boolean run(Context ctx, String serverId, String path, File local, long gen) {
    Servers.Server s = Servers.get(ctx).find(serverId);
    String name = path.substring(path.lastIndexOf('/') + 1);
    int notification = key(serverId, path).hashCode();
    NotificationManager nm = ctx.getSystemService(NotificationManager.class);
    if (s == null) {
      // Kept: the server list may not have loaded (removing a server forgets its saves itself).
      Logs.w("upload of " + name + " waits: no server " + serverId);
      return false;
    }
    Exception last = null;
    for (int attempt = 0; attempt < 3; attempt++) {
      try {
        Engine.uploadWithProgress(serverId, path, local.getPath(), (done, total) -> progress(serverId, path, s.label, done, total));
        // Older saves first, this one last: if Driveway stops in between,
        // the next start sends this one again rather than an older one.
        retireOlder(ctx, serverId, path, gen);
        forget(local);
        nm.cancel(notification);
        failedWhy.remove(key(serverId, path));
        Problems.clear(ctx, serverId);
        Logs.i("uploaded " + name + " to " + s.label);
        return true;
      } catch (Exception e) {
        last = e;
        if (!Errors.kind(e).equals("network")) break;
        try {
          Thread.sleep(2000L << attempt);
        } catch (InterruptedException ie) {
          break;
        }
      }
    }
    sending.remove(key(serverId, path));
    Logs.w("upload of " + name + " to " + s.label + " failed: " + last);
    Problems.observe(ctx, s, last);
    failedWhy.put(key(serverId, path), Errors.explain(ctx, s, last));
    channel(ctx);
    Intent retry = new Intent(ctx, RetryReceiver.class)
        .putExtra(EXTRA_SERVER, serverId).putExtra(EXTRA_PATH, path).putExtra(EXTRA_LOCAL, local.getPath());
    PendingIntent pi = PendingIntent.getBroadcast(ctx, notification, retry,
        PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    Intent discard = new Intent(retry).setAction(RetryReceiver.DISCARD);
    PendingIntent di = PendingIntent.getBroadcast(ctx, notification + 1, discard,
        PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    Notification n = new Notification.Builder(ctx, CHANNEL)
        .setSmallIcon(R.drawable.ic_server)
        .setContentTitle(ctx.getString(R.string.upload_failed, name, s.label))
        .setContentText(Errors.explain(ctx, s, last))
        .setStyle(new Notification.BigTextStyle().bigText(Errors.explain(ctx, s, last)))
        .addAction(new Notification.Action.Builder(null, ctx.getString(R.string.try_again), pi).build())
        .addAction(new Notification.Action.Builder(null, ctx.getString(R.string.discard), di).build())
        .setAutoCancel(false)
        .build();
    nm.notify(notification, n);
    // A newer save handed to an upload takes this notification away when
    // it is queued; one queued just before it was shown would leave it
    // offering Try again and Discard for a save that gave way.
    Long newer = queued.get(key(serverId, path));
    if (newer != null && newer > gen) nm.cancel(notification);
    return false;
  }

  static void channel(Context ctx) {
    SessionService.channels(ctx);
  }

  // The session service keeps Driveway in the foreground while uploads run.

  private static synchronized void started(Context ctx) {
    running++;
    SessionService.touch(ctx);
  }

  private static synchronized void finished(Context ctx) {
    running--;
    SessionService.touch(ctx);
  }

  // Progress for the notification: per save, bytes sent of the total.

  static final class Sending {
    final String name;
    final String server;
    volatile long done;
    volatile long total;

    Sending(String name, String server) {
      this.name = name;
      this.server = server;
    }
  }

  private static final Map<String, Sending> sending = new ConcurrentHashMap<>();

  private static void progress(String serverId, String path, String label, long done, long total) {
    Sending p = sending.computeIfAbsent(key(serverId, path), k -> new Sending(path.substring(path.lastIndexOf('/') + 1), label));
    p.done = done;
    p.total = total;
    if (done >= total) sending.remove(key(serverId, path));
  }

  /** What's uploading now, for the notification. */
  static List<Sending> sending() {
    return new ArrayList<>(sending.values());
  }

  /** The device ran out of room while an app was saving to a server. */
  static void deviceFull(Context ctx, Servers.Server s, String path) {
    channel(ctx);
    String name = path.substring(path.lastIndexOf('/') + 1);
    ctx.getSystemService(NotificationManager.class).notify(key(s.id, path).hashCode(),
        new Notification.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_server)
            .setContentTitle(ctx.getString(R.string.upload_failed, name, s.label))
            .setContentText(ctx.getString(R.string.err_device_full))
            .setStyle(new Notification.BigTextStyle().bigText(ctx.getString(R.string.err_device_full)))
            .build());
  }

  static synchronized int running() {
    return running;
  }

  // The failed saves, for the server cards on the home screen.

  /** A save that didn't reach its server: Try again or Discard, on its card as in its notification. */
  static final class Failed {
    final String serverId;
    final String path;
    final File file;
    final String why;

    Failed(String serverId, String path, File file, String why) {
      this.serverId = serverId;
      this.path = path;
      this.file = file;
      this.why = why;
    }

    String name() {
      return path.substring(path.lastIndexOf('/') + 1);
    }
  }

  /** The server's saves that failed and wait for Try again or Discard, newest first. */
  static List<Failed> failedFor(String serverId) {
    List<Failed> out = new ArrayList<>();
    String prefix = serverId + ":";
    for (String key : failed.keySet()) {
      if (!key.startsWith(prefix)) continue;
      File st = failing(key);
      if (st == null || !st.exists()) continue;
      JSONObject n = note(st);
      if (n == null || !n.optBoolean("ready")) continue; // a newer save is being written
      out.add(new Failed(serverId, key.substring(prefix.length()), st, failedWhy.get(key)));
    }
    out.sort((a, b) -> Long.compare(genOf(b.file), genOf(a.file)));
    return out;
  }

  private static final List<Runnable> listeners = new java.util.concurrent.CopyOnWriteArrayList<>();
  private static final android.os.Handler main = new android.os.Handler(android.os.Looper.getMainLooper());

  /** Runs on the main thread when a save fails, succeeds or is discarded. */
  static void listen(Runnable r) {
    listeners.add(r);
  }

  static void unlisten(Runnable r) {
    listeners.remove(r);
  }

  private static void changed() {
    for (Runnable r : listeners) main.post(r);
  }
}
