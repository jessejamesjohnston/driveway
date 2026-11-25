// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.content.res.ColorStateList;
import android.graphics.Typeface;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.KeyboardShortcutGroup;
import android.view.KeyboardShortcutInfo;
import android.view.Menu;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.window.OnBackInvokedDispatcher;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.Spinner;
import android.widget.SpinnerAdapter;
import android.widget.TextView;

import com.merrimentlabs.driveway.engine.Engine;

import org.json.JSONObject;

import java.nio.file.Files;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Adding a server is a short path: "Where are your files?" (servers found on
 * the network, a computer or NAS by kind, cloud services by name), then the
 * details that kind needs, then Connect, which shows a live checklist (found,
 * identity, signed in, folder opened) and ends on "<name> is in Files".
 * Changing a server skips the path: its form opens with Save, and Save or
 * Test connection show the same checklist under the form.
 *
 * Keyboard: Tab moves through each page; Enter moves to the next field and,
 * in the last one, connects or saves; Ctrl+S does too from anywhere on the
 * form; Escape (like Back) goes back a step, asking first if a form was
 * changed. A resized window keeps the screen as it is (configChanges); a
 * change of theme or text size rebuilds it with the form, and any connection
 * in progress, as they were (see Kept and Connection).
 *
 * The class reads top to bottom in the order of the path: state and the
 * handoff to a rebuilt screen (Kept, restore), Back and the keyboard, step 1
 * (Where are your files, More services), step 2 (building the form, locking
 * it, the key and file fields), step 3 (collect, the connection, the
 * checklist, the finish). The form's state stays in this class because
 * nearly every step reads it and Kept hands all of it over at once;
 * docs/DESIGN.md ("Where the Add a server code lives") says what is split out
 * and what is next.
 */
public final class ServerActivity extends Activity implements Connection.Listener {
  static final String EXTRA_ID = "id";
  static final String EXTRA_BACKEND = "backend";
  static final String EXTRA_LABEL = "label";
  static final String EXTRA_HOST = "host";
  static final String EXTRA_PORT = "port";
  static final String EXTRA_HTTPS = "https";
  /** Opened from a server card's problem: sign in again, or check its identity. */
  static final String EXTRA_FIX = "fix";
  static final String FIX_SIGN_IN = "signin";
  static final String FIX_IDENTITY = "identity";

  private enum Screen { WHERE, MORE, DETAILS, CONNECTING, DONE }

  private Ui.Page page;
  private LinearLayout col;
  private Screen screen;
  private Servers.Server server;
  private boolean isNew;
  /** Opened straight onto the details (a found server's Add): Back leaves. */
  private boolean startedAtDetails;
  private boolean fromMore;
  private Kinds.Kind kind;
  private boolean saved;

  // The form, built once per kind and kept while the path moves on.
  private LinearLayout form;
  private EditText labelInput;
  private EditText folderInput;
  private final Map<String, View> inputs = new LinkedHashMap<>();
  private LinearLayout moreBox;
  private Button moreToggle;
  private TextView formStatus;
  private LinearLayout checklistBox;
  private Button primary;
  /** A new service's Connect button, in the form under its essentials (the bar has none). */
  private Button connectButton;
  private Button testButton;
  /** Sign in again, in the bar of a server being changed, or null. */
  private Button signInAgainButton;
  /** Make a key, or Forget this key (SFTP), as shown now. */
  private Button keyCreate, keyForget;
  /** Key and settings files chosen for FILE fields: their contents, by setting. */
  private final Map<String, String> fileValues = new LinkedHashMap<>();
  /** What each chosen file was found to be ("driveway@project..."), by setting. */
  private final Map<String, String> fileSummaries = new LinkedHashMap<>();
  /** The FILE field whose picker is open. */
  private String pickingKey;
  /** Each FILE field's Choose button (focused when the file is missing). */
  private final Map<String, Button> fileButtons = new LinkedHashMap<>();
  private String privatePem;
  private String publicLine;
  private boolean labelEdited;
  private boolean signInAgain;
  /** "Your account lists this sign-in as rclone", while it does. */
  private TextView oldSignInNote;
  /** The form as it was first shown, to tell whether anything changed. */
  private String initial;
  /** Settings this screen's setup settled (they win over the stored server's). */
  private final java.util.Set<String> settledKeys = new java.util.HashSet<>();

  /** The connection running or last run from this form. */
  private Connection run;
  /** The prompt last shown, so a new one takes the keyboard to its safe button once. */
  private String shownAsk = "";

  // Where are your files?
  private Discovery discovery;
  private boolean looking;
  private List<Discovery.Found> found = new ArrayList<>();
  private LinearLayout foundBox;
  private String search = "";
  private boolean doneAnimated;

  /** The form being restored after the screen was rebuilt, while it is rebuilt. */
  private Kept restoring;
  /** Dialogs on screen; closed with the screen. */
  private final List<AlertDialog> dialogs = new ArrayList<>();

  /**
   * The screen, handed from this one to the one Android builds in its place
   * after a change of theme, text size or display density. It stays in memory
   * (never in the saved-state Bundle), so passwords and keys aren't written
   * anywhere, and it carries a connection in progress over to the new screen.
   * A form killed with the app in the background starts over.
   */
  private static final class Kept {
    Servers.Server server;
    boolean isNew;
    boolean startedAtDetails;
    boolean fromMore;
    boolean saved;
    Screen screen;
    String backend;
    final Map<String, String> values = new LinkedHashMap<>();
    String label;
    String folder;
    boolean labelEdited;
    String privatePem;
    String publicLine;
    boolean moreOpen;
    CharSequence status;
    String initial;
    Connection run;
    String search;
    java.util.Set<String> settledKeys;
    final Map<String, String> fileValues = new LinkedHashMap<>();
    final Map<String, String> fileSummaries = new LinkedHashMap<>();
    String pickingKey;
    final java.util.Set<String> newShown = new java.util.HashSet<>();
  }

  @Override
  protected void onCreate(Bundle savedInstanceState) {
    super.onCreate(savedInstanceState);
    page = Ui.pageWithBar(this, Ui.PAGE_WIDTH);
    col = page.col;
    live = this;
    discovery = new Discovery(this, this::showFound);
    // A New label counts as seen once its row is on screen.
    page.scroll.getViewTreeObserver().addOnScrollChangedListener(newRows::countSeen);
    page.scroll.getViewTreeObserver().addOnGlobalLayoutListener(newRows::countSeen);
    getOnBackInvokedDispatcher().registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, this::leave);
    Object last = getLastNonConfigurationInstance();
    if (last instanceof Kept) {
      restore((Kept) last);
      return;
    }
    String id = getIntent().getStringExtra(EXTRA_ID);
    if (id != null) {
      server = Servers.get(this).find(id);
      if (server == null) {
        finish();
        return;
      }
      Kinds.Kind k = Kinds.of(server.backend);
      if (k == null) {
        finish();
        return;
      }
      showForm(k);
      String fix = getIntent().getStringExtra(EXTRA_FIX);
      if (FIX_SIGN_IN.equals(fix) && Kinds.signsInAgain(k, server.params)) {
        signInAgain = true;
        testThen(true);
      } else if (FIX_SIGN_IN.equals(fix)) {
        // A server that signed in through the browser before Driveway stopped
        // offering that can't sign in again: its form, at the other way in.
        backTo(true);
      } else if (FIX_IDENTITY.equals(fix)) {
        testThen(true);
      }
      return;
    }
    isNew = true;
    server = new Servers.Server();
    server.id = Servers.newId();
    String backend = getIntent().getStringExtra(EXTRA_BACKEND);
    // Only a service that can be added now (not a hidden one, or one with no way in).
    if (backend != null && Kinds.offered(Kinds.of(backend))) {
      prefill(backend);
      startedAtDetails = true;
      showForm(Kinds.of(backend));
    } else {
      showWhere();
    }
  }

  private void restore(Kept k) {
    server = k.server;
    isNew = k.isNew;
    startedAtDetails = k.startedAtDetails;
    fromMore = k.fromMore;
    saved = k.saved;
    search = k.search == null ? "" : k.search;
    run = k.run;
    if (k.settledKeys != null) settledKeys.addAll(k.settledKeys);
    fileValues.putAll(k.fileValues);
    fileSummaries.putAll(k.fileSummaries);
    pickingKey = k.pickingKey;
    newRows.shown.addAll(k.newShown);
    if (k.backend != null && Kinds.of(k.backend) != null) {
      restoring = k;
      buildForm(Kinds.of(k.backend));
      restoring = null;
      for (Map.Entry<String, View> e : inputs.entrySet()) {
        String value = k.values.get(e.getKey());
        if (value == null) continue;
        View v = e.getValue();
        if (v instanceof EditText) {
          ((EditText) v).setText(value);
        } else if (v instanceof Spinner) {
          int at = positionOf((Spinner) v, value);
          if (at >= 0) ((Spinner) v).setSelection(at);
        }
      }
      labelInput.setText(k.label);
      folderInput.setText(k.folder);
      labelEdited = k.labelEdited;
      if (moreBox != null && k.moreOpen != (moreBox.getVisibility() == View.VISIBLE)) toggleMore();
      formStatus.setText(k.status);
      initial = k.initial;
    }
    switch (k.screen == null ? Screen.WHERE : k.screen) {
      case MORE:
        showMore();
        break;
      case DETAILS:
        showForm(kind);
        break;
      case CONNECTING:
        showConnecting();
        break;
      case DONE:
        doneAnimated = true;
        showDone();
        break;
      default:
        showWhere();
    }
    if (run != null) run.attach(this);
  }

  /** Where {@code value} is among a spinner's choices, or -1 when it isn't one. */
  private static int positionOf(Spinner spinner, String value) {
    SpinnerAdapter choices = spinner.getAdapter();
    for (int i = 0; i < choices.getCount(); i++) {
      if (value.equals(choices.getItem(i))) return i;
    }
    return -1;
  }

  @Override
  public Object onRetainNonConfigurationInstance() {
    if (server == null) return null;
    Kept k = new Kept();
    k.server = server;
    k.isNew = isNew;
    k.startedAtDetails = startedAtDetails;
    k.fromMore = fromMore;
    k.saved = saved;
    k.screen = screen;
    k.search = search;
    k.run = run;
    k.settledKeys = new java.util.HashSet<>(settledKeys);
    k.fileValues.putAll(fileValues);
    k.fileSummaries.putAll(fileSummaries);
    k.pickingKey = pickingKey;
    k.newShown.addAll(newRows.shown);
    if (kind == null || form == null) return k;
    k.backend = kind.backend;
    for (Map.Entry<String, View> e : inputs.entrySet()) {
      String value = valueOf(e.getValue());
      if (value != null) k.values.put(e.getKey(), value);
    }
    k.label = labelInput.getText().toString();
    k.folder = folderInput.getText().toString();
    k.labelEdited = labelEdited;
    k.privatePem = privatePem;
    k.publicLine = publicLine;
    k.moreOpen = moreBox != null && moreBox.getVisibility() == View.VISIBLE;
    k.status = formStatus.getText();
    k.initial = initial;
    return k;
  }

  private static String valueOf(View v) {
    if (v instanceof EditText) return ((EditText) v).getText().toString();
    if (v instanceof Spinner) return String.valueOf(((Spinner) v).getSelectedItem());
    return null;
  }

  /** Everything the form holds, to compare with how it started. */
  private String snapshot() {
    StringBuilder b = new StringBuilder();
    for (View v : inputs.values()) b.append(valueOf(v)).append('\n');
    b.append(labelInput.getText()).append('\n').append(folderInput.getText()).append('\n').append(privatePem);
    for (Map.Entry<String, String> e : fileValues.entrySet()) b.append('\n').append(e.getKey()).append('=').append(e.getValue().hashCode());
    return b.toString();
  }

  private boolean formChanged() {
    return form != null && initial != null && !initial.equals(snapshot());
  }

  /** Back or Escape: one step back, asking first before a changed form is lost. */
  private void leave() {
    if (screen == null) {
      finish();
      return;
    }
    switch (screen) {
      case MORE:
        showWhere();
        return;
      case CONNECTING:
        // Stops the connection (and a sign-in waiting in the browser); the
        // details are as they were.
        if (run != null) run.cancel();
        showForm(kind);
        return;
      case DETAILS:
        if (!formChanged()) {
          backFromDetails();
          return;
        }
        AlertDialog d = show(new AlertDialog.Builder(this)
            .setTitle(isNew ? R.string.discard_new_title : R.string.discard_title)
            .setPositiveButton(R.string.discard, (x, w) -> backFromDetails())
            .setNegativeButton(R.string.keep_editing, null));
        Ui.focus(d, AlertDialog.BUTTON_NEGATIVE);
        return;
      default:
        finish();
    }
  }

  private void backFromDetails() {
    if (run != null) run.cancel();
    if (!isNew || startedAtDetails) {
      finish();
      return;
    }
    form = null;
    if (fromMore) showMore();
    else showWhere();
  }

  private AlertDialog show(AlertDialog.Builder b) {
    AlertDialog d = b.create();
    dialogs.add(d);
    d.setOnDismissListener(x -> dialogs.remove(d));
    d.show();
    return d;
  }

  @Override
  public boolean onKeyDown(int keyCode, KeyEvent event) {
    if (keyCode == KeyEvent.KEYCODE_ESCAPE && event.hasNoModifiers()) return true;
    return super.onKeyDown(keyCode, event);
  }

  @Override
  public boolean onKeyUp(int keyCode, KeyEvent event) {
    if (keyCode == KeyEvent.KEYCODE_ESCAPE && event.hasNoModifiers()) {
      leave();
      return true;
    }
    return super.onKeyUp(keyCode, event);
  }

  @Override
  public boolean onKeyShortcut(int keyCode, KeyEvent event) {
    if (keyCode == KeyEvent.KEYCODE_S && event.isCtrlPressed() && screen == Screen.DETAILS) {
      // A held key repeats: once.
      if (event.getRepeatCount() == 0) primaryAction();
      return true;
    }
    return super.onKeyShortcut(keyCode, event);
  }

  @Override
  public void onProvideKeyboardShortcuts(List<KeyboardShortcutGroup> data, Menu menu, int deviceId) {
    List<KeyboardShortcutInfo> keys = new ArrayList<>();
    keys.add(new KeyboardShortcutInfo(getString(isNew ? R.string.connect : R.string.save), KeyEvent.KEYCODE_S,
        KeyEvent.META_CTRL_ON));
    keys.add(new KeyboardShortcutInfo(getString(R.string.back), KeyEvent.KEYCODE_ESCAPE, 0));
    data.add(new KeyboardShortcutGroup(getTitle(), keys));
  }

  /** Between onResume and onPause: discovery runs only then. */
  private boolean resumed;

  @Override
  protected void onResume() {
    super.onResume();
    resumed = true;
    if (screen == Screen.WHERE) startLooking();
  }

  /*
   * In desktop windowing (Googlebooks), Settings opens in a window of its own
   * and this screen stays resumed, so onResume doesn't run on the way back:
   * look for servers once Nearby devices is allowed there, whenever this
   * window comes forward.
   */
  @Override
  public void onTopResumedActivityChanged(boolean top) {
    super.onTopResumedActivityChanged(top);
    if (top && resumed && screen == Screen.WHERE) startLooking();
  }

  @Override
  public void onWindowFocusChanged(boolean focused) {
    super.onWindowFocusChanged(focused);
    if (focused && resumed && screen == Screen.WHERE) startLooking();
  }

  @Override
  protected void onPause() {
    super.onPause();
    resumed = false;
    discovery.stop();
    looking = false;
  }

  @Override
  protected void onDestroy() {
    super.onDestroy();
    if (live == this) live = null;
    if (run != null) run.detach(this);
    for (AlertDialog d : new ArrayList<>(dialogs)) d.dismiss();
    // Rebuilt for a new theme or text size: the next screen carries on with
    // the same form and connection.
    if (isChangingConfigurations()) return;
    // The screen is gone for good: a connection still running stops before
    // it changes anything more (the engine's settings are put back below).
    if (run != null) {
      run.cancel();
      if (run.outcome == Connection.Outcome.SAVED) saved = true;
    }
    if (server == null || saved) return;
    if (!isNew) {
      // Edited, maybe tested, not saved: the server goes back to how it was
      // (if Driveway is stopped first, its next start puts the key back).
      Servers servers = Servers.get(this);
      Servers.Server stored = servers.find(server.id);
      if (stored != null) servers.push(stored);
      servers.restorePin(server.id);
    } else if (Servers.get(this).find(server.id) == null) {
      // A connection registered it with the engine; forget it.
      Engine.removeRemote(server.id);
      Servers.get(this).knownHosts(server.id).delete();
      Servers.get(this).keepPin(server.id);
    }
  }

  private void prefill(String backend) {
    server.backend = backend;
    Kinds.Kind k = Kinds.of(backend);
    for (Map.Entry<String, String> e : k == null ? java.util.Collections.<String, String>emptyMap().entrySet() : k.prefill.entrySet()) {
      try {
        server.params.put(e.getKey(), e.getValue());
      } catch (org.json.JSONException ignored) {
      }
    }
    server.label = getIntent().getStringExtra(EXTRA_LABEL);
    String host = getIntent().getStringExtra(EXTRA_HOST);
    int port = getIntent().getIntExtra(EXTRA_PORT, 0);
    fillFound(backend, host, port, getIntent().getBooleanExtra(EXTRA_HTTPS, false));
  }

  private void fillFound(String backend, String host, int port, boolean https) {
    try {
      if (backend.equals("webdav")) {
        // An IPv6 address goes in brackets in a URL (its zone's % escaped).
        String h = host != null && host.contains(":") ? "[" + host.replace("%", "%25") + "]" : host;
        server.params.put("url", (https ? "https://" : "http://") + h + (port > 0 ? ":" + port : ""));
      } else {
        server.params.put("host", host);
        Kinds.Kind k = Kinds.main(backend);
        if (port > 0 && k != null) {
          for (Kinds.Field f : k.fields) {
            if (f.key.equals("port") && !String.valueOf(port).equals(f.fallback)) server.params.put("port", String.valueOf(port));
          }
        }
      }
    } catch (Exception ignored) {
    }
  }

  private void clearPage() {
    col.removeAllViews();
    page.bar.removeAllViews();
    page.scroll.scrollTo(0, 0);
  }

  // Step 1: where are your files?

  private void showWhere() {
    screen = Screen.WHERE;
    kind = null;
    clearPage();
    col.addView(Ui.topBar(this, getString(R.string.where_title), v -> leave()));
    col.addView(stepLine(1));

    col.addView(Ui.heading(this, getString(R.string.found_on_network)));
    foundBox = Ui.card(this);
    col.addView(foundBox);
    renderFound();

    col.addView(Ui.heading(this, getString(R.string.a_computer)));
    Layouts.Grid tiles = new Layouts.Grid(this, 150, 4, 12);
    for (Kinds.Kind k : Kinds.MAIN) tiles.addView(tile(k));
    col.addView(tiles);

    col.addView(Ui.heading(this, getString(R.string.a_cloud)));
    LinearLayout cloud = Ui.card(this);
    for (String b : Kinds.CLOUD) {
      Kinds.Kind k = Kinds.other(b);
      if (!Kinds.oneTap(k)) continue;
      if (cloud.getChildCount() > 0) cloud.addView(Ui.divider(this));
      cloud.addView(serviceRow(k, rowText(k)));
    }
    if (cloud.getChildCount() > 0) cloud.addView(Ui.divider(this));
    cloud.addView(Ui.cardRow(this, Ui.iconTile(this, R.drawable.ic_cloud), getString(R.string.more_services),
        getString(R.string.more_services_blurb, moreCount()), Ui.chevron(this), v -> showMore()));
    col.addView(cloud);
    startLooking();
  }

  /** How many services More services offers besides S3 and FTP (the ones it names). */
  private static int moreCount() {
    int n = 0;
    for (Kinds.Kind k : Kinds.others()) {
      if (Kinds.offered(k) && !k.backend.equals("s3") && !k.backend.equals("ftp")) n++;
    }
    return n;
  }

  private TextView stepLine(int n) {
    TextView t = Ui.secondary(this, getString(R.string.step_of, n, 3));
    t.setPadding(0, 0, 0, Ui.dp(this, 4));
    return t;
  }

  /** A kind of server as a tappable tile: its icon, name and a few words. */
  private View tile(Kinds.Kind k) {
    LinearLayout t = Ui.card(this);
    t.setPadding(Ui.dp(this, 16), Ui.dp(this, 16), Ui.dp(this, 16), Ui.dp(this, 14));
    t.addView(Ui.serviceTile(this, k.backend));
    TextView name = Ui.text(this, k.label, 16, 500);
    name.setPadding(0, Ui.dp(this, 12), 0, 0);
    t.addView(name);
    t.addView(Ui.secondary(this, Kinds.tileText(k)));
    t.setOnClickListener(v -> choose(k, false));
    t.setContentDescription(getString(R.string.choose_named, k.label, Kinds.tileText(k)));
    Ui.pressable(t, 16, R.color.card);
    return t;
  }

  /** A service's subtitle in a list: how it signs in, or what it is. */
  private String rowText(Kinds.Kind k) {
    if (Kinds.SIGN_IN.equals(k.method)) return getString(R.string.sign_in_browser);
    if (k.alt != null) return k.alt.row;
    String sub = k.blurb.isEmpty() ? "" : k.blurb.split("\n", 2)[0];
    return sub.length() > 90 ? sub.substring(0, 89).trim() + "…" : sub;
  }

  /** A service as a row. */
  private View serviceRow(Kinds.Kind k, String subtitle) {
    return newRows.labeled(Ui.cardRow(this, Ui.serviceTile(this, k.backend), k.label, subtitle, Ui.chevron(this),
        v -> choose(k, false)), true, k, subtitle);
  }

  private void startLooking() {
    if (screen != Screen.WHERE || looking || !MainActivity.hasLocalNetwork(this)) return;
    found = new ArrayList<>();
    looking = true;
    lookStarted = System.currentTimeMillis();
    discovery.start();
    renderFound();
    // Nothing announced itself: say so, rather than "Looking…" for as long
    // as the screen is open.
    foundBox.postDelayed(() -> {
      if (screen == Screen.WHERE && looking && found.isEmpty()) renderFound();
    }, LOOK_MS);
  }

  /** How long a look runs before saying nothing was found (it keeps listening). */
  private static final long LOOK_MS = 10_000;
  private long lookStarted;

  private void showFound(List<Discovery.Found> list) {
    found = list;
    if (screen == Screen.WHERE) renderFound();
  }

  private void renderFound() {
    if (foundBox == null) return;
    foundBox.removeAllViews();
    if (!MainActivity.hasLocalNetwork(this)) {
      LinearLayout box = Ui.column(this);
      box.setPadding(Ui.dp(this, 16), Ui.dp(this, 12), Ui.dp(this, 16), Ui.dp(this, 4));
      box.addView(Ui.secondary(this, getString(R.string.look_hint)));
      Ui.addStart(box, Ui.flat(this, getString(R.string.look_for_servers), v -> Permissions.allow(this, ASK_LOOK,
          MainActivity.LOCAL_NETWORK)));
      foundBox.addView(box);
      return;
    }
    if (found.isEmpty()) {
      boolean long_ = !looking || System.currentTimeMillis() - lookStarted >= LOOK_MS - 100;
      LinearLayout r = Ui.row(this);
      r.setMinimumHeight(Ui.dp(this, 56));
      r.setPadding(Ui.dp(this, 16), Ui.dp(this, 8), Ui.dp(this, 16), Ui.dp(this, 8));
      if (!long_) {
        ProgressBar p = new ProgressBar(this, null, android.R.attr.progressBarStyleSmall);
        p.setIndeterminateTintList(ColorStateList.valueOf(Ui.accent(this)));
        p.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(Ui.dp(this, 20), Ui.dp(this, 20));
        lp.setMarginEnd(Ui.dp(this, 12));
        r.addView(p, lp);
      }
      TextView t = Ui.secondary(this, getString(long_ ? R.string.nothing_found : R.string.looking));
      Ui.announces(t);
      r.addView(t, Ui.weight());
      foundBox.addView(r);
      return;
    }
    for (int i = 0; i < found.size(); i++) {
      Discovery.Found f = found.get(i);
      if (i > 0) foundBox.addView(Ui.divider(this));
      View add = Ui.tonal(this, getString(R.string.add), v -> chooseFound(f));
      add.setContentDescription(getString(R.string.add_named, f.name, Kinds.label(f.backend)));
      foundBox.addView(Ui.cardRow(this, Ui.icon(this, Kinds.icon(f.backend), R.color.muted, 24), f.name,
          Kinds.label(f.backend) + " · " + f.host, add, null));
    }
  }

  private void chooseFound(Discovery.Found f) {
    Kinds.Kind k = Kinds.of(f.backend);
    if (k == null) return;
    freshDraft(k);
    server.label = f.name;
    fillFound(f.backend, f.host, f.port, f.https);
    fromMore = false;
    showForm(k);
  }

  private void choose(Kinds.Kind k, boolean more) {
    if (!Kinds.offered(k)) return;
    freshDraft(k);
    fromMore = more;
    showForm(k);
  }

  /** A new server of this kind, with nothing from an earlier choice. */
  private void freshDraft(Kinds.Kind k) {
    if (run != null) run.cancel();
    run = null;
    checklist = null;
    // Nothing from an earlier try (its settings in the engine, a trusted key).
    Engine.removeRemote(server.id);
    Servers.get(this).knownHosts(server.id).delete();
    Servers.get(this).keepPin(server.id);
    server.backend = k.backend;
    server.params = new JSONObject();
    for (Map.Entry<String, String> e : k.prefill.entrySet()) {
      try {
        server.params.put(e.getKey(), e.getValue());
      } catch (org.json.JSONException ignored) {
      }
    }
    fileValues.clear();
    fileSummaries.clear();
    server.label = null;
    server.folder = "";
    privatePem = null;
    publicLine = null;
    form = null;
    settledKeys.clear();
  }

  private static final int ASK_LOOK = 2;
  static final int ASK_NEARBY = 4;

  @Override
  public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] results) {
    // A "Don't allow" in Android's dialog is the answer: nothing opens App
    // info after it (Permissions.allow does that up front, only when Android
    // no longer shows its dialog).
    if (requestCode == ASK_LOOK) {
      renderFound();
      startLooking();
    } else if (requestCode == ASK_NEARBY && MainActivity.hasLocalNetwork(this)) {
      retry();
    }
  }

  // More services: every other rclone service, searchable.

  private LinearLayout moreList;
  /** The New chips on this visit's service rows. */
  private final NewLabelRows newRows = new NewLabelRows(this);

  private void showMore() {
    screen = Screen.MORE;
    kind = null;
    clearPage();
    col.addView(Ui.topBar(this, getString(R.string.more_services), v -> leave()));
    col.addView(stepLine(1));
    EditText q = new EditText(this);
    q.setSingleLine(true);
    q.setHint(R.string.search_services);
    q.setTypeface(Ui.face(this, 400));
    q.setTextColor(Ui.color(this, R.color.ink));
    q.setBackground(Ui.outlined(this));
    q.setMinHeight(Ui.dp(this, 52));
    q.setPaddingRelative(Ui.dp(this, 12), 0, Ui.dp(this, 16), 0);
    android.graphics.drawable.Drawable mag = getDrawable(R.drawable.ic_search).mutate();
    mag.setTint(Ui.color(this, R.color.muted));
    mag.setBounds(0, 0, Ui.dp(this, 22), Ui.dp(this, 22));
    q.setCompoundDrawablesRelative(mag, null, null, null);
    q.setCompoundDrawablePadding(Ui.dp(this, 10));
    q.setText(search);
    q.setImeOptions(EditorInfo.IME_ACTION_SEARCH);
    q.addTextChangedListener(new Watcher(() -> {
      search = q.getText().toString();
      renderMore();
    }));
    // Enter with one service left picks it.
    q.setOnEditorActionListener((v, action, event) -> {
      boolean enter = action == EditorInfo.IME_ACTION_SEARCH
          || (event != null && event.getKeyCode() == KeyEvent.KEYCODE_ENTER && event.getAction() == KeyEvent.ACTION_UP);
      if (enter) {
        List<Kinds.Kind> m = matches();
        if (m.size() == 1) choose(m.get(0), true);
      }
      return enter || (event != null && event.getKeyCode() == KeyEvent.KEYCODE_ENTER);
    });
    LinearLayout.LayoutParams qlp = Ui.matchWidth();
    qlp.topMargin = Ui.dp(this, 8);
    qlp.bottomMargin = Ui.dp(this, 12);
    col.addView(q, qlp);
    moreList = Ui.card(this);
    col.addView(moreList);
    renderMore();
  }

  private List<Kinds.Kind> matches() {
    String s = search.trim().toLowerCase(Locale.ROOT);
    List<Kinds.Kind> out = new ArrayList<>();
    for (Kinds.Kind k : Kinds.others()) {
      // Only what can be added now: a service with no way in isn't listed.
      if (!Kinds.offered(k)) continue;
      if (s.isEmpty() || k.label.toLowerCase(Locale.ROOT).contains(s) || k.backend.contains(s)
          || k.blurb.toLowerCase(Locale.ROOT).contains(s)) {
        out.add(k);
      }
    }
    return out;
  }

  private void renderMore() {
    moreList.removeAllViews();
    List<Kinds.Kind> m = matches();
    if (m.isEmpty()) {
      TextView t = Ui.secondary(this, getString(R.string.no_service_matches, search.trim()));
      t.setPadding(Ui.dp(this, 16), Ui.dp(this, 16), Ui.dp(this, 16), Ui.dp(this, 16));
      Ui.announces(t);
      moreList.addView(t);
      return;
    }
    for (int i = 0; i < m.size(); i++) {
      Kinds.Kind k = m.get(i);
      if (i > 0) moreList.addView(Ui.divider(this));
      String sub = rowText(k);
      moreList.addView(newRows.labeled(Ui.cardRow(this, null, k.label, sub, Ui.chevron(this), v -> choose(k, true)), false, k,
          sub));
    }
  }

  // Step 2: the details.

  private void showForm(Kinds.Kind k) {
    if (k == null) {
      finish();
      return;
    }
    if (form == null || kind != k) buildForm(k);
    screen = Screen.DETAILS;
    clearPage();
    ViewGroup parent = (ViewGroup) form.getParent();
    if (parent != null) parent.removeView(form);
    col.addView(form);
    setTitle(isNew ? titleFor(k) : getString(R.string.edit_server));
    // The buttons are in the form (addActions), under the settings it can't
    // connect without; the pinned bar stays away.
    primary = connectButton;
    updateMainButton();
    if (!isNew && run != null) renderRun();
  }

  private String titleFor(Kinds.Kind k) {
    return Kinds.main(k.backend) != null ? getString(R.string.add_kind, k.label) : getString(R.string.add_service, k.label);
  }

  /** The main button says what it will do: Connect, Save, or Sign in and save. */
  private void updateMainButton() {
    if (primary == null || kind == null) return;
    if (isNew) {
      primary.setText(Kinds.main(kind.backend) == null ? getString(R.string.connect_to, kind.label) : getString(R.string.connect));
    } else {
      primary.setText(Kinds.browser(kind, server.params) && !server.params.has("token") ? R.string.sign_in : R.string.save);
    }
    lockForm();
  }

  /** A connection is running from this form (Save, Test connection, a sign-in). */
  private boolean busy() {
    return run != null && run.running();
  }

  /**
   * While a connection uses the form's values, the form can't be changed:
   * what the run checks is what it saves. The fields,
   * choices, Choose file, Make a key and Forget, Sign in again, Save and Test
   * connection are disabled; Cancel, More settings, Copy and the checklist
   * (its questions, a setup answer, Cancel sign-in) stay usable. All come
   * back when the run ends, on this screen or one rebuilt around the run.
   */
  private void lockForm() {
    boolean busy = busy();
    List<View> lock = new ArrayList<>();
    if (primary != null) lock.add(primary);
    if (testButton != null) lock.add(testButton);
    if (signInAgainButton != null) lock.add(signInAgainButton);
    if (labelInput != null) lock.add(labelInput);
    if (folderInput != null) lock.add(folderInput);
    // A key or settings file's box holds its Choose button (below).
    for (View v : inputs.values()) if (v instanceof EditText || v instanceof Spinner) lock.add(v);
    lock.addAll(fileButtons.values());
    if (keyCreate != null) lock.add(keyCreate);
    if (keyForget != null) lock.add(keyForget);
    // A disabled view gives up focus, and Android would hand it to the top
    // of the page: the checklist takes it first.
    if (busy) {
      View focused = getCurrentFocus();
      boolean keyboard = false, reader = false;
      for (View v : lock) {
        if (v.isEnabled() && v == focused) keyboard = true;
        if (v.isEnabled() && accessibilityFocused(v)) reader = true;
      }
      if ((keyboard || reader) && checklist != null) checklist.takeFocus(keyboard, reader);
    }
    for (View v : lock) v.setEnabled(!busy);
    if (filling()) {
      if (primary != null) primary.setEnabled(false);
      if (testButton != null) testButton.setEnabled(false);
    }
  }

  /**
   * The server whose form a key or settings file is being made or read for
   * (main thread only). Save and Test connection wait for it, so a run never
   * starts on a draft that changes under it.
   */
  private static String fillingFor;

  private boolean filling() {
    return fillingFor != null && server != null && fillingFor.equals(server.id);
  }

  /** Waits for a key or file at most this long: a stuck file provider can't hold Save forever. */
  private static final long FILL_LIMIT_MS = 20_000;

  private void startFilling(String forServer) {
    fillingFor = forServer;
    lockForm();
    Object mine = new Object();
    fillToken = mine;
    new android.os.Handler(android.os.Looper.getMainLooper()).postDelayed(() -> {
      if (fillToken == mine) filled();
    }, FILL_LIMIT_MS);
  }

  private static Object fillToken;

  /** A key or file arrived (or failed): the form's buttons come back on the screen showing it. */
  private static void filled() {
    fillingFor = null;
    fillToken = null;
    if (live != null) live.lockForm();
  }

  private static boolean accessibilityFocused(View v) {
    return v.createAccessibilityNodeInfo().isAccessibilityFocused();
  }

  private void primaryAction() {
    if (screen != Screen.DETAILS) return;
    testThen(true);
  }

  private void buildForm(Kinds.Kind k) {
    connectButton = null;
    if (Kinds.main(k.backend) == null) {
      buildServiceForm(k);
      return;
    }
    kind = k;
    inputs.clear();
    form = Ui.column(this);
    form.addView(Ui.topBar(this, isNew ? titleFor(k) : server.label, v -> leave()));
    if (isNew) form.addView(stepLine(2));
    if (!k.blurb.isEmpty()) form.addView(Ui.secondary(this, k.blurb.split("\n", 2)[0]));

    // Server: its name and where it is. Sign in: who Driveway is there.
    LinearLayout serverCard = FormViews.group(this);
    LinearLayout signInCard = FormViews.group(this);
    labelInput = FormViews.field(this, serverCard, getString(R.string.name), getString(R.string.name_hint), server.label, InputType.TYPE_CLASS_TEXT);
    labelEdited = server.label != null && !server.label.isEmpty();
    labelInput.addTextChangedListener(new Watcher(() -> labelEdited = true));
    List<Kinds.Field> advanced = new ArrayList<>();
    for (Kinds.Field f : k.fields) {
      if (f.advanced) advanced.add(f);
      else addField(Kinds.signInField(f) ? signInCard : serverCard, f);
    }
    View groups = signInCard.getChildCount() == 0 ? FormViews.section(this, R.string.group_server, serverCard)
        : new Layouts.Columns(this, 16, FormViews.section(this, R.string.group_server, serverCard),
            FormViews.section(this, R.string.group_sign_in, signInCard));
    form.addView(groups);

    if (k.note != null) {
      TextView note = Ui.secondary(this, k.note);
      note.setPadding(Ui.dp(this, 4), Ui.dp(this, 12), Ui.dp(this, 4), 0);
      form.addView(note);
    }
    form.addView(newFormStatus());
    addActions(null);
    addMore(k, advanced);
    addKeptNote(k, false);
    checklistBox = Ui.column(this);
    form.addView(checklistBox);
    followAddress(k);
    if (restoring == null) initial = snapshot();
  }

  /** The form's status line: a missing field, a file that isn't a key, a result. */
  private TextView newFormStatus() {
    formStatus = Ui.secondary(this, "");
    formStatus.setPadding(Ui.dp(this, 4), Ui.dp(this, 8), Ui.dp(this, 4), 0);
    // Read out as it changes (a missing field, a result), and selectable to
    // copy once there is something to copy (an empty status is no stop).
    Ui.announces(formStatus);
    formStatus.addTextChangedListener(new Watcher(() -> {
      formStatus.setTextIsSelectable(formStatus.length() > 0);
      formStatus.setVisibility(formStatus.length() > 0 ? View.VISIBLE : View.GONE);
    }));
    formStatus.setVisibility(View.GONE);
    return formStatus;
  }

  /**
   * "More settings": the folder to start in (most people want the top),
   * then every other setting, folded away until asked for. A
   * server with a starting folder opens with More settings unfolded, so the
   * folder it starts in is in sight.
   */
  private void addMore(Kinds.Kind k, List<Kinds.Field> advanced) {
    LinearLayout more = FormViews.group(this);
    folderInput = FormViews.field(this, more, getString(R.string.folder), folderHint(k), server.folder, InputType.TYPE_CLASS_TEXT);
    savesOnEnter(folderInput);
    for (Kinds.Field f : advanced) addField(more, f);
    more.setVisibility(View.GONE);
    moreBox = more;
    moreToggle = Ui.flat(this, getString(R.string.more_settings), R.drawable.ic_chevron_right, v -> toggleMore());
    moreToggle.setStateDescription(getString(R.string.collapsed));
    LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT,
        ViewGroup.LayoutParams.WRAP_CONTENT);
    tlp.topMargin = Ui.dp(this, 8);
    tlp.setMarginStart(-Ui.dp(this, 4));
    form.addView(moreToggle, tlp);
    form.addView(more);
    if (server.folder != null && !server.folder.isEmpty()) toggleMore();
  }

  /**
   * A service's form: its name in Files and the few settings it can't
   * connect without, then (for a new server) one centered "Connect to
   * <service>", saying where it goes, then everything else under More
   * settings. A service set up with a credential instead of a browser
   * sign-in says on top what to connect with, and where to get it.
   */
  private void buildServiceForm(Kinds.Kind k) {
    kind = k;
    inputs.clear();
    fileButtons.clear();
    form = Ui.column(this);
    form.addView(Ui.topBar(this, isNew ? titleFor(k) : server.label, v -> leave()));
    if (isNew) form.addView(stepLine(2));
    // A new server, or one that signed in through the browser before Driveway
    // stopped offering that (it is changed to the other way in).
    if (k.alt != null && (isNew || (Kinds.browser(k, server.params) && !Kinds.signsInAgain(k, server.params)))) {
      LinearLayout note = Ui.column(this);
      note.setBackground(Ui.rounded(this, R.color.accent_container, 16));
      note.setPadding(Ui.dp(this, 16), Ui.dp(this, 12), Ui.dp(this, 16), Ui.dp(this, 14));
      TextView with = Ui.text(this, getString(R.string.alt_connect, k.alt.with), 16, 500);
      with.setTextColor(Ui.color(this, R.color.on_accent_container));
      note.addView(with);
      TextView how = Ui.text(this, k.alt.how, 14, 400);
      how.setTextColor(Ui.color(this, R.color.on_accent_container));
      how.setPadding(0, Ui.dp(this, 6), 0, 0);
      note.addView(how);
      LinearLayout.LayoutParams nlp = Ui.matchWidth();
      nlp.topMargin = Ui.dp(this, 8);
      nlp.bottomMargin = Ui.dp(this, 4);
      form.addView(note, nlp);
    } else if (isNew && !k.blurb.isEmpty() && !Kinds.SIGN_IN.equals(k.method)) {
      form.addView(Ui.secondary(this, k.blurb.split("\n", 2)[0]));
    }

    LinearLayout card = FormViews.group(this);
    String name = server.label != null && !server.label.isEmpty() ? server.label : k.label;
    labelInput = FormViews.field(this, card, getString(R.string.name), getString(R.string.name_hint), name, InputType.TYPE_CLASS_TEXT);
    labelEdited = server.label != null && !server.label.isEmpty();
    labelInput.addTextChangedListener(new Watcher(() -> labelEdited = true));
    List<Kinds.Field> advanced = new ArrayList<>();
    for (Kinds.Field f : k.fields) {
      if (f.advanced) advanced.add(f);
      else addField(card, f);
    }
    LinearLayout.LayoutParams clp = Ui.matchWidth();
    clp.topMargin = Ui.dp(this, 12);
    form.addView(card, clp);
    form.addView(newFormStatus());

    boolean browser = Kinds.browser(k, server.params);
    addActions(isNew && browser ? getString(R.string.connect_opens_browser, k.label) : null);
    // Signed in before Driveway had its own registration: still listed as
    // rclone in the account. Sign in again (in the bar) moves it.
    oldSignInNote = null;
    if (!isNew && Kinds.oldSignIn(k, server.params)) {
      oldSignInNote = Ui.secondary(this, getString(R.string.old_sign_in_note, k.label));
      oldSignInNote.setPadding(Ui.dp(this, 8), Ui.dp(this, 4), Ui.dp(this, 8), Ui.dp(this, 8));
      form.addView(oldSignInNote, Ui.matchWidth());
    }
    addMore(k, advanced);
    addKeptNote(k, browser);
    checklistBox = Ui.column(this);
    form.addView(checklistBox);
    followAddress(k);
    if (restoring == null) initial = snapshot();
  }

  /**
   * How the sign-in is kept, as the last line of the form, after More
   * settings, centered: reference, not a step. In the
   * form, not pinned to the screen's bottom, where it floats far from a form
   * on a large window and meets the keyboard on a phone. A browser sign-in:
   * "that access"; a form with a password, key or key file: "your sign-in";
   * a form with neither (a public web folder) says nothing.
   */
  private void addKeptNote(Kinds.Kind k, boolean browser) {
    boolean secret = false;
    for (Kinds.Field f : k.fields) {
      if (f.type == Kinds.Type.PASSWORD || f.type == Kinds.Type.KEY || f.type == Kinds.Type.FILE || f.masked) secret = true;
    }
    if (!browser && !secret) return;
    TextView note = Ui.secondary(this, getString(browser ? R.string.sign_in_note : R.string.secret_note));
    note.setGravity(Gravity.CENTER_HORIZONTAL);
    note.setTextAlignment(View.TEXT_ALIGNMENT_CENTER);
    note.setPadding(Ui.dp(this, 4), Ui.dp(this, 24), Ui.dp(this, 4), Ui.dp(this, 8));
    form.addView(note, Ui.matchWidth());
  }

  /**
   * The form's actions, right under the settings it can't connect without:
   * the main one (Connect, Connect to <service>, Save,
   * Sign in) a full-width filled button, the largest control on the page, so
   * most people just tap it; then, for a server being changed, Sign in again
   * (tonal), Test connection and Cancel as smaller secondary buttons. A line
   * under the main button says where it goes (the browser), when it does.
   */
  private void addActions(String where) {
    // Enter in the last setting above the buttons connects or saves (the
    // others move on to the next).
    EditText last = labelInput;
    for (Kinds.Field f : kind.fields) {
      if (!f.advanced && inputs.get(f.key) instanceof EditText) last = (EditText) inputs.get(f.key);
    }
    savesOnEnter(last);
    LinearLayout box = Ui.column(this);
    box.setPadding(0, Ui.dp(this, 20), 0, Ui.dp(this, 4));
    connectButton = Ui.button(this, getString(R.string.connect), v -> primaryAction());
    Ui.emphasize(connectButton);
    box.addView(connectButton, Ui.matchWidth());
    if (where != null) {
      // Supporting text, centered and tight under the button it explains.
      TextView w = Ui.secondary(this, where);
      w.setGravity(Gravity.CENTER_HORIZONTAL);
      w.setTextAlignment(View.TEXT_ALIGNMENT_CENTER);
      w.setPadding(Ui.dp(this, 4), Ui.dp(this, 6), Ui.dp(this, 4), 0);
      box.addView(w, Ui.matchWidth());
    }
    testButton = null;
    signInAgainButton = null;
    if (!isNew) {
      List<View> more = new ArrayList<>();
      if (Kinds.signsInAgain(kind, server.params) && server.params.has("token")) {
        signInAgainButton = Ui.tonal(this, getString(R.string.sign_in_again), v -> {
          if (busy()) return;
          signInAgain = true;
          testThen(true);
        });
        more.add(signInAgainButton);
      }
      testButton = Ui.flat(this, getString(R.string.test), v -> testThen(false));
      more.add(testButton);
      // Cancel discards without asking: it says what it does.
      more.add(Ui.flat(this, getString(R.string.cancel), v -> finish()));
      LinearLayout.LayoutParams mlp = Ui.matchWidth();
      mlp.topMargin = Ui.dp(this, 8);
      box.addView(Ui.actions(this, more.toArray(new View[0])), mlp);
    }
    form.addView(box, Ui.matchWidth());
  }

  /** The name follows the server's address (a host or URL setting) until it's typed in. */
  private void followAddress(Kinds.Kind k) {
    String key = null;
    for (Kinds.Field f : k.fields) {
      if (!f.advanced && (f.key.equals("host") || f.key.equals("url"))) {
        key = f.key;
        break;
      }
    }
    View host = key == null ? null : inputs.get(key);
    boolean isHost = "host".equals(key);
    if (host instanceof EditText) {
      ((EditText) host).addTextChangedListener(new Watcher(() -> {
        if (!labelEdited) {
          labelInput.setText(Kinds.followedName(((EditText) host).getText().toString(), k, isHost));
          labelEdited = false;
        }
      }));
    }
  }

  private void toggleMore() {
    boolean open = moreBox.getVisibility() == View.GONE;
    moreBox.setVisibility(open ? View.VISIBLE : View.GONE);
    moreToggle.setStateDescription(getString(open ? R.string.expanded : R.string.collapsed));
    android.graphics.drawable.Drawable d = getDrawable(open ? R.drawable.ic_expand_more : R.drawable.ic_chevron_right).mutate();
    d.setBounds(0, 0, Ui.dp(this, 18), Ui.dp(this, 18));
    d.setTintList(moreToggle.getTextColors());
    moreToggle.setCompoundDrawablesRelative(d, null, null, null);
  }

  /** Enter (or the keyboard's Done) in this field connects or saves. */
  private void savesOnEnter(EditText e) {
    e.setImeOptions(EditorInfo.IME_ACTION_DONE);
    e.setOnEditorActionListener((v, action, event) -> {
      if (action == EditorInfo.IME_ACTION_DONE) {
        primaryAction();
        return true;
      }
      // A hardware Enter: act when it's released (the press must be taken too).
      if (action == EditorInfo.IME_NULL && event != null && event.getKeyCode() == KeyEvent.KEYCODE_ENTER) {
        if (event.getAction() == KeyEvent.ACTION_UP) primaryAction();
        return true;
      }
      return false;
    });
  }

  private String folderHint(Kinds.Kind k) {
    if (k.folderHint != null && !k.folderHint.isEmpty()) return k.folderHint;
    switch (k.backend) {
      case "smb":
        return getString(R.string.folder_hint_smb);
      case "sftp":
        return getString(R.string.folder_hint_sftp);
      default:
        return getString(R.string.folder_hint);
    }
  }

  private void addField(LinearLayout parent, Kinds.Field f) {
    String value = server.params.optString(f.key, "");
    switch (f.type) {
      case KEY:
        addKey(parent);
        return;
      case FILE:
        addFile(parent, f);
        return;
      case CHOICE: {
        TextView l = FormViews.label(this, f.label);
        parent.addView(l);
        Spinner sp = new Spinner(this);
        sp.setId(View.generateViewId());
        sp.setSaveEnabled(false);
        l.setLabelFor(sp.getId());
        sp.setMinimumHeight(Ui.dp(this, 52));
        sp.setBackground(Ui.outlined(this));
        sp.setPaddingRelative(Ui.dp(this, 4), 0, Ui.dp(this, 4), 0);
        sp.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item, f.choices));
        int at = f.choices.indexOf(value.isEmpty() ? f.fallback : value);
        if (at >= 0) sp.setSelection(at);
        parent.addView(sp, Ui.matchWidth());
        inputs.put(f.key, sp);
        return;
      }
      default: {
        int type = f.type == Kinds.Type.NUMBER ? InputType.TYPE_CLASS_NUMBER
            : f.masked ? InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD
            : InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI;
        String hint = f.hint.isEmpty() && !f.fallback.isEmpty() ? f.fallback : f.hint;
        // A single-use token, once the service has given Driveway its own access.
        boolean again = f.ephemeral && server.params.has("token");
        if (again) hint = getString(R.string.leave_empty_keep);
        // Everything under More settings is optional: no need to say so there.
        boolean optional = !Kinds.required(kind, f, server.params);
        EditText e = FormViews.field(this, parent, f.label + (optional && !f.advanced ? getString(R.string.optional) : ""), hint, value, type);
        inputs.put(f.key, e);
        // A port typed after the name moves to Port as soon as focus leaves Server.
        if (f.key.equals("host") && Kinds.takesPort(kind)) {
          e.setOnFocusChangeListener((v, has) -> {
            if (!has && form != null && !busy()) splitAddress(true);
          });
        }
      }
    }
  }

  /**
   * A key or settings file: what was chosen (the key's account, the app's
   * ID), and a button that opens Android's file picker. Nothing is read
   * until the person picks a file, and only that file.
   */
  private void addFile(LinearLayout parent, Kinds.Field f) {
    TextView l = FormViews.label(this, f.label + (Kinds.required(kind, f, server.params) ? "" : getString(R.string.optional)));
    parent.addView(l);
    if (restoring == null && !fileValues.containsKey(f.key)) {
      String saved = server.params.optString(f.key, "");
      if (!saved.isEmpty()) {
        fileValues.put(f.key, saved);
        try {
          fileSummaries.put(f.key, new JSONObject(Engine.checkCredential(kind.backend, f.key, saved)).optString("summary"));
        } catch (Exception ignored) {
          // Shown as chosen, without a name.
        }
      }
    }
    LinearLayout box = Ui.column(this);
    box.setTag("file:" + f.key);
    parent.addView(box, Ui.matchWidth());
    showFile(box, f);
    inputs.put(f.key, box);
  }

  private void showFile(LinearLayout box, Kinds.Field f) {
    box.removeAllViews();
    boolean chosen = fileValues.containsKey(f.key);
    String summary = fileSummaries.get(f.key);
    TextView state = Ui.secondary(this, !chosen ? f.hint
        : getString(R.string.file_chosen, summary == null || summary.isEmpty() ? getString(R.string.file_chosen_unnamed) : summary));
    state.setPadding(Ui.dp(this, 4), 0, Ui.dp(this, 4), Ui.dp(this, 4));
    if (chosen) state.setTextColor(Ui.color(this, R.color.ink));
    box.addView(state);
    Button choose = Ui.tonal(this, getString(chosen ? R.string.choose_another_file : R.string.choose_file),
        R.drawable.ic_description, v -> pickFile(f.key));
    // The visible words first, so a voice command still matches them.
    choose.setContentDescription(getString(chosen ? R.string.choose_another_file_for : R.string.choose_file_for, f.label));
    choose.setEnabled(!busy());
    fileButtons.put(f.key, choose);
    Ui.addStart(box, choose);
  }

  private static final int PICK_FILE = 7;
  /** A key or settings file is small; anything bigger isn't one. */
  private static final int MAX_FILE = 256 * 1024;

  private void pickFile(String key) {
    pickingKey = key;
    Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
    try {
      startActivityForResult(i, PICK_FILE);
    } catch (android.content.ActivityNotFoundException e) {
      formStatus.setText(R.string.no_file_picker);
    }
  }

  @Override
  protected void onActivityResult(int requestCode, int resultCode, Intent data) {
    super.onActivityResult(requestCode, resultCode, data);
    if (requestCode != PICK_FILE || resultCode != RESULT_OK || data == null || data.getData() == null || kind == null) return;
    String key = pickingKey;
    String backend = kind.backend;
    // The form it was picked for: this one, or the one rebuilt in its place
    // (a theme change), never another server's.
    String forServer = server.id;
    Uri uri = data.getData();
    startFilling(forServer);
    new Thread(() -> {
      String error = null;
      JSONObject got = null;
      try (java.io.InputStream in = getContentResolver().openInputStream(uri)) {
        if (in == null) throw new java.io.IOException("nothing to read");
        java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream();
        byte[] buf = new byte[8192];
        for (int n; (n = in.read(buf)) > 0; ) {
          out.write(buf, 0, n);
          if (out.size() > MAX_FILE) throw new Errors.Worded(getString(R.string.file_too_big));
        }
        got = new JSONObject(Engine.checkCredential(backend, key, out.toString("UTF-8")));
      } catch (Errors.Worded e) {
        error = e.getMessage();
      } catch (java.io.IOException | SecurityException e) {
        error = getString(R.string.file_unreadable);
      } catch (Exception e) {
        // The engine's file check's own sentence, or the plain one.
        String said = Errors.credential(e);
        error = said != null ? said : getString(R.string.file_unreadable);
      }
      String err = error;
      JSONObject result = got;
      runOnUiThread(() -> {
        filled();
        ServerActivity a = live;
        if (a == null || a.kind == null || !backend.equals(a.kind.backend) || a.form == null || a.server == null
            || !forServer.equals(a.server.id)) return;
        if (a.busy()) {
          a.formStatus.setText(R.string.file_late);
          return;
        }
        a.fileChosen(key, result, err);
      });
    }).start();
  }

  /** A picked file: kept and shown if it is the right kind, else why not. */
  private void fileChosen(String key, JSONObject result, String error) {
    Kinds.Field field = null;
    for (Kinds.Field f : kind.fields) if (f.key.equals(key)) field = f;
    View box = form.findViewWithTag("file:" + key);
    if (field == null || !(box instanceof LinearLayout)) return;
    if (error != null) {
      formStatus.setText(error);
      return;
    }
    fileValues.put(key, result.optString("value"));
    fileSummaries.put(key, result.optString("summary"));
    formStatus.setText("");
    // What the file also says (a key's project), where the field is still empty.
    JSONObject fill = result.optJSONObject("fill");
    for (java.util.Iterator<String> it = fill == null ? java.util.Collections.<String>emptyIterator() : fill.keys(); it.hasNext(); ) {
      String k = it.next();
      View v = inputs.get(k);
      if (v instanceof EditText && ((EditText) v).length() == 0) ((EditText) v).setText(fill.optString(k));
    }
    showFile((LinearLayout) box, field);
    Button b = fileButtons.get(key);
    if (b != null) b.post(b::requestFocus);
  }

  /** A key made for a screen that was rebuilt before it arrived: {server id, private, public}. */
  private static volatile String[] madeKey;
  /** The screen on show (main thread only), and its key's place on the form. */
  private static ServerActivity live;
  private LinearLayout keyBox;

  /** SFTP: Driveway's own key, made here; the public half goes on the server. */
  private void addKey(LinearLayout parent) {
    // A rebuilt screen keeps a key made but not saved yet.
    privatePem = restoring != null ? restoring.privatePem : server.params.optString("key_pem", null);
    publicLine = restoring != null ? restoring.publicLine : server.params.optString("driveway_public", null);
    String[] made = madeKey;
    if (restoring != null && made != null && made[0].equals(server.id) && (privatePem == null || privatePem.isEmpty())) {
      madeKey = null;
      privatePem = made[1];
      publicLine = made[2];
    }
    parent.addView(FormViews.label(this, getString(R.string.key)));
    LinearLayout box = Ui.column(this);
    keyBox = box;
    parent.addView(box);
    showKey(box);
  }

  private void showKey(LinearLayout box) {
    box.removeAllViews();
    keyCreate = null;
    keyForget = null;
    if (privatePem == null || privatePem.isEmpty()) {
      TextView none = Ui.secondary(this, getString(R.string.key_none));
      none.setPadding(Ui.dp(this, 4), 0, Ui.dp(this, 4), 0);
      box.addView(none);
      String forServer = server.id;
      keyCreate = Ui.tonal(this, getString(R.string.key_create), R.drawable.ic_key, v -> {
        startFilling(forServer);
        makeKey(forServer);
      });
      keyCreate.setEnabled(!busy());
      Ui.addStart(box, keyCreate);
      return;
    }
    showMadeKey(box);
  }

  private void makeKey(String forServer) {
    new Thread(() -> {
      try {
        JSONObject k = new JSONObject(Engine.newKey("driveway@" + Build.MODEL.replace(' ', '-')));
        // In memory for a screen rebuilt meanwhile (a theme change) that
        // hasn't shown its form yet: it takes it from there.
        madeKey = new String[] {forServer, k.optString("private"), k.optString("public")};
        runOnUiThread(() -> {
          filled();
          // The screen showing this form now: this one, or the one rebuilt in its place.
          ServerActivity a = live;
          if (a == null || a.server == null || !forServer.equals(a.server.id) || a.keyBox == null) return;
          // Save waits for the key, so a run can only be going if the wait ran
          // out: never change the draft it is checking.
          if (a.busy()) return;
          madeKey = null;
          a.privatePem = k.optString("private");
          a.publicLine = k.optString("public");
          a.showKey(a.keyBox);
        });
      } catch (Exception e) {
        android.util.Log.w("Driveway", "can't make a key", e);
        runOnUiThread(() -> {
          filled();
          formStatus.setText(R.string.key_failed);
        });
      }
    }).start();
  }

  private void showMadeKey(LinearLayout box) {
    TextView add = Ui.secondary(this, getString(R.string.key_add));
    add.setPadding(Ui.dp(this, 4), 0, Ui.dp(this, 4), Ui.dp(this, 4));
    box.addView(add);
    TextView pub = Ui.text(this, publicLine, 14, 400);
    pub.setTypeface(Typeface.MONOSPACE);
    pub.setTextIsSelectable(true);
    pub.setBackground(Ui.rounded(this, R.color.page, 10));
    pub.setPadding(Ui.dp(this, 12), Ui.dp(this, 8), Ui.dp(this, 12), Ui.dp(this, 8));
    // Selectable, so a keyboard stop: at least 48dp tall even as one line.
    pub.setMinHeight(Ui.dp(this, 48));
    pub.setGravity(Gravity.CENTER_VERTICAL);
    box.addView(pub);
    Button copy = Ui.tonal(this, getString(R.string.copy), v -> {
      getSystemService(ClipboardManager.class).setPrimaryClip(ClipData.newPlainText("Driveway key", publicLine));
    });
    copy.setOnClickListener(v -> {
      getSystemService(ClipboardManager.class).setPrimaryClip(ClipData.newPlainText("Driveway key", publicLine));
      // Says so on the button itself (no toast); read out as it changes.
      copy.setText(R.string.copied);
      copy.postDelayed(() -> copy.setText(R.string.copy), 2000);
    });
    copy.setContentDescription(getString(R.string.copy_key));
    keyForget = Ui.flat(this, getString(R.string.key_forget), v -> {
      privatePem = null;
      publicLine = null;
      showKey(box);
    });
    keyForget.setEnabled(!busy());
    box.addView(Ui.actions(this, copy, keyForget));
  }

  // Step 3: connect (or, for a server being changed, test and save).

  /** What a setup run settled: into the draft, and into the form's fields that show it. */
  private void keepSetup(JSONObject values) {
    for (java.util.Iterator<String> it = values.keys(); it.hasNext(); ) {
      String key = it.next();
      settledKeys.add(key);
      Object value = values.opt(key);
      try {
        server.params.put(key, value);
      } catch (org.json.JSONException ignored) {
      }
      View v = inputs.get(key);
      String text = String.valueOf(value);
      if (v instanceof EditText && !text.equals(((EditText) v).getText().toString())) {
        ((EditText) v).setText(text);
      } else if (v instanceof Spinner) {
        int at = positionOf((Spinner) v, text);
        if (at >= 0) ((Spinner) v).setSelection(at);
      }
    }
    // A single-use answer (Jottacloud's login token) is used up now: Save
    // after Test must not hand it to the service again.
    for (Kinds.Field f : kind.fields) {
      View v = inputs.get(f.key);
      if (f.ephemeral && v instanceof EditText) ((EditText) v).setText("");
    }
    if (oldSignInNote != null && !Kinds.oldSignIn(kind, server.params)) oldSignInNote.setVisibility(View.GONE);
    updateMainButton();
  }

  /** What splitting the Server field changed that the form should say (a port that replaced another), or null. */
  private String addressNote;
  /** The port addressNote names, from when focus left the Server field. */
  private String addressNotePort;
  /** What the status line was given when focus last left the Server field. */
  private String leftSaid;

  /**
   * "host:port" in the Server field: the host stays there and the port goes
   * to Port under More settings, so the form shows what is used. The port
   * typed with the name wins over a different one already in Port (it is
   * the one typed last, next to the name it belongs to), and the form says
   * so (addressNote). What can't be a host is said in plain words on the
   * field, never looked up as a name.
   */
  private void splitAddress() {
    splitAddress(false);
  }

  /**
   * leaving: focus just left the Server field. A port moved then says so on
   * the form's status line; what can't be used is said there too and left
   * as typed (Connect points to the field). On Connect a problem stops it.
   */
  private void splitAddress(boolean leaving) {
    // On Connect the note starts empty: it is kept below only while it still holds.
    String kept = leaving ? null : addressNote;
    String keptPort = leaving ? null : addressNotePort;
    if (!leaving) {
      addressNote = null;
      addressNotePort = null;
    }
    if (!Kinds.takesPort(kind)) return;
    View hv = inputs.get("host"), pv = inputs.get("port");
    if (!(hv instanceof EditText) || !(pv instanceof EditText)) return;
    EditText hostField = (EditText) hv, portField = (EditText) pv;
    String fallback = "";
    for (Kinds.Field f : kind.fields) if (f.key.equals("port")) fallback = f.fallback;
    Kinds.Split sp = Kinds.split(hostField.getText().toString(), portField.getText().toString(), fallback, kind.backend);
    Kinds.Address a = sp.address;
    // What the last time focus left the field said goes, unless it still holds.
    if (leaving && leftSaid != null && leftSaid.contentEquals(formStatus.getText())) formStatus.setText("");
    leftSaid = null;
    if (a.problem != 0) {
      String hostLabel = getString(R.string.name);
      for (Kinds.Field f : kind.fields) if (f.key.equals("host")) hostLabel = f.label;
      String problem = getString(a.problem, a.bad, a.problem == R.string.address_scheme ? kind.label : hostLabel);
      if (leaving) {
        formStatus.setText(problem);
        leftSaid = problem;
        return;
      }
      throw new Missing(problem, hostField);
    }
    if (!sp.host.equals(hostField.getText().toString().trim())) hostField.setText(sp.host);
    if (!sp.port.equals(portField.getText().toString())) portField.setText(sp.port);
    String note = sp.note == 0 ? null : sp.note == R.string.address_port_moved
        ? getString(sp.note, sp.port, sp.replaced) : getString(sp.note, sp.port);
    if (leaving) {
      if (note != null) formStatus.setText(note);
      leftSaid = note;
      // A replaced port is said again on the connecting screen, while Port still holds it.
      if (sp.note == R.string.address_port_moved) {
        addressNote = note;
        addressNotePort = sp.port;
      }
      return;
    }
    if (sp.note == R.string.address_port_moved) {
      addressNote = note;
    } else if (sp.note == 0 && keptPort != null && keptPort.equals(portField.getText().toString().trim())) {
      addressNote = kept;
    }
  }

  private Servers.Server collect() throws IllegalArgumentException {
    splitAddress();
    Servers.Server s = server.copy();
    s.backend = kind.backend;
    JSONObject p = new JSONObject();
    try {
      // Signing in again starts from no sign-in: a server that signed in
      // through the browser before Driveway stopped offering its sign-in
      // is asked for the alternative's credential then, never sent to the
      // sign-in rclone would borrow.
      JSONObject saved = server.params;
      if (signInAgain) {
        saved = new JSONObject(server.params.toString());
        saved.remove("token");
      }
      for (Kinds.Field f : kind.fields) {
        View v = inputs.get(f.key);
        String raw = v instanceof EditText ? ((EditText) v).getText().toString() : null;
        // Passwords as typed; anything else (a pasted API key) without the
        // space or line break that came with it.
        boolean asTyped = f.type == Kinds.Type.PASSWORD || f.key.equals("pass") || f.key.equals("password");
        String value = f.type == Kinds.Type.FILE ? fileValues.getOrDefault(f.key, "")
            : raw != null ? (asTyped ? raw : raw.trim())
            : v instanceof Spinner ? String.valueOf(((Spinner) v).getSelectedItem()) : "";
        if ("drive-folder".equals(f.link)) value = driveFolderId(value);
        // A single-use token is needed only until the service has given its
        // own access; an old browser sign-in needs no alternative's credential.
        boolean required = Kinds.required(kind, f, saved);
        if (required && value.isEmpty() && f.type != Kinds.Type.KEY) {
          throw new Missing(getString(R.string.needs, f.label), f.type == Kinds.Type.FILE ? fileButtons.get(f.key) : v);
        }
        if (!value.isEmpty()) p.put(f.key, value);
      }
      if (kind.backend.equals("sftp")) {
        if (privatePem != null && !privatePem.isEmpty()) {
          p.put("key_pem", privatePem);
          p.put("driveway_public", publicLine);
        }
        if (!p.has("pass") && !p.has("key_pem")) throw new IllegalArgumentException(getString(R.string.needs_login));
      }
      if (Kinds.main(kind.backend) == null) {
        // Settings that have no field on the form (the sign-in, which drive
        // setup chose): those this screen's own setup settled, else the
        // server's own as they are now (the service may have replaced the
        // sign-in since this screen opened).
        Servers.Server stored = isNew ? null : Servers.get(this).find(server.id);
        java.util.Set<String> onForm = new java.util.HashSet<>();
        for (Kinds.Field f : kind.fields) onForm.add(f.key);
        // Signing in again leaves the sign-in itself behind, not the settings
        // that choose how it signs in (a registration of its own, a national
        // cloud, a tenant).
        if (signInAgain) onForm.add("token");
        for (String key : settledKeys) {
          if (!onForm.contains(key) && !p.has(key) && server.params.has(key)) p.put(key, server.params.get(key));
        }
        JSONObject from = stored != null ? stored.params : server.params;
        for (java.util.Iterator<String> it = from.keys(); it.hasNext(); ) {
          String key = it.next();
          if (!onForm.contains(key) && !p.has(key)) p.put(key, from.get(key));
        }
      }
    } catch (org.json.JSONException e) {
      android.util.Log.w("Driveway", "can't read the form", e);
      throw new IllegalArgumentException(getString(R.string.form_failed), e);
    }
    s.params = p;
    String label = labelInput.getText().toString().trim();
    s.label = label.isEmpty() ? p.optString("host", kind.label) : label;
    s.folder = folderInput.getText().toString().trim().replaceAll("^/+|/+$", "");
    return s;
  }

  /** A Drive folder's ID from its link (…/folders/ID, or ?id=ID), or the text as it is. */
  static String driveFolderId(String s) {
    java.util.regex.Matcher m = java.util.regex.Pattern.compile("(?:/folders/|[?&]id=)([A-Za-z0-9_-]{10,})").matcher(s);
    return m.find() ? m.group(1) : s;
  }

  private void testThen(boolean save) {
    // Already connecting (a second press, Enter, Ctrl+S), or a key or file
    // is still on its way to this form: what's saved must include it.
    if (run != null && run.running() || filling()) return;
    Servers.Server s;
    try {
      s = collect();
    } catch (IllegalArgumentException e) {
      formStatus.setText(e.getMessage());
      if (e instanceof Missing && ((Missing) e).field instanceof EditText) {
        // The field says what's missing, and takes focus (read out, too).
        EditText field = (EditText) ((Missing) e).field;
        field.requestFocus();
        field.setError(e.getMessage());
      } else if (e instanceof Missing && ((Missing) e).field != null) {
        ((Missing) e).field.requestFocus();
      }
      return;
    }
    // Empty, or which port is used now that the Server field had one.
    formStatus.setText(addressNote == null ? "" : addressNote);
    if (run != null) run.detach(this);
    run = new Connection(this, s, kind, isNew, save, signInAgain);
    signInAgain = false;
    shownAsk = "";
    run.attach(this);
    run.start();
    if (isNew) showConnecting();
    else renderRun();
    updateMainButton();
  }

  /** Try again: the same request from the form as it is now. */
  private void retry() {
    boolean save = run == null || run.save;
    if (isNew) {
      showForm(kind);
    }
    testThen(save);
  }

  @Override
  public void changed() {
    if (run == null || isDestroyed()) return;
    JSONObject settled = run.takeSettled();
    if (settled != null && form != null) keepSetup(settled);
    if (run.outcome == Connection.Outcome.SAVED && !saved) {
      saved = true;
      MainActivity.parkedId = server.id;
      MainActivity.parkedAt = android.os.SystemClock.elapsedRealtime();
      if (isNew) {
        showDone();
      } else {
        finish();
      }
      return;
    }
    if (screen == Screen.CONNECTING || (screen == Screen.DETAILS && !isNew)) renderRun();
    if (screen == Screen.DONE) return;
    updateMainButton();
  }

  private LinearLayout runView;

  private void showConnecting() {
    screen = Screen.CONNECTING;
    clearPage();
    String name = run != null ? run.server.label : server.label;
    col.addView(Ui.topBar(this, getString(R.string.connecting_to, name), v -> leave()));
    col.addView(stepLine(3));
    // The port the Server field's replaced (the form, now out of sight, says it too).
    if (addressNote != null) {
      TextView note = Ui.secondary(this, addressNote);
      note.setPadding(Ui.dp(this, 4), Ui.dp(this, 8), Ui.dp(this, 4), 0);
      col.addView(note);
    }
    runView = Ui.column(this);
    LinearLayout.LayoutParams lp = Ui.matchWidth();
    lp.topMargin = Ui.dp(this, 8);
    col.addView(runView, lp);
    renderRun();
  }

  /** The checklist, and the actions that fit where the run is. */
  private void renderRun() {
    if (run == null) return;
    LinearLayout target = screen == Screen.CONNECTING ? runView : checklistBox;
    if (target == null) return;
    LinearLayout fresh = checklistFor(run, target);
    if (fresh != null) {
      target.removeAllViews();
      LinearLayout.LayoutParams lp = Ui.matchWidth();
      if (screen == Screen.DETAILS) lp.topMargin = Ui.dp(this, 16);
      target.addView(fresh, lp);
      // Under a long form, the checklist scrolls into view as it starts.
      if (screen == Screen.DETAILS) {
        fresh.post(() -> fresh.requestRectangleOnScreen(
            new android.graphics.Rect(0, 0, fresh.getWidth(), fresh.getHeight()), false));
      }
    }
    if (screen == Screen.CONNECTING) {
      page.bar.removeAllViews();
      if (run.running()) {
        // Stops connecting (and a sign-in waiting in the browser).
        page.bar.addView(Ui.actions(this, Ui.flat(this, getString(R.string.back_to_details), v -> leave())), Ui.weight());
      } else {
        List<View> b = new ArrayList<>();
        b.add(Ui.button(this, getString(R.string.back_to_details), v -> showForm(kind)));
        if (run.save && !"untrusted".equals(run.errorKind) && !"cancelled".equals(run.errorKind)
            && !"save".equals(run.errorKind) && !SetupChecklist.tokenless(run)) {
          b.add(Ui.flat(this, getString(R.string.save_anyway), v -> saveAnyway()));
        }
        page.bar.addView(Ui.actions(this, b.toArray(new View[0])), Ui.weight());
      }
    }
    // A question for the person takes the keyboard to its safe answer once.
    String askNow = run.ask + ":" + (run.question == null ? "" : run.questionNo);
    if (!askNow.equals(shownAsk)) {
      shownAsk = askNow;
      View safe = target.findViewWithTag("safe");
      if (safe != null) safe.post(safe::requestFocus);
    }
  }

  private SetupChecklist checklist;

  private final SetupChecklist.Actions checklistActions = new SetupChecklist.Actions() {
    @Override
    public boolean isNew() {
      return isNew;
    }

    @Override
    public void signInAgain() {
      signInAgain = true;
      retry();
    }

    @Override
    public void retry() {
      ServerActivity.this.retry();
    }

    @Override
    public void backTo(boolean signIn) {
      ServerActivity.this.backTo(signIn);
    }

    @Override
    public void saveAnyway() {
      ServerActivity.this.saveAnyway();
    }
  };

  /** A new checklist card for the run, or null when the one on screen there was updated. */
  private LinearLayout checklistFor(Connection r, LinearLayout target) {
    if (checklist == null || checklist.run != r || checklist.card.getParent() != target) {
      if (checklist != null && checklist.card.getParent() instanceof ViewGroup) {
        ((ViewGroup) checklist.card.getParent()).removeView(checklist.card);
      }
      checklist = new SetupChecklist(this, r, checklistActions);
      return checklist.card;
    }
    checklist.update();
    return null;
  }

  /** Back to the details, with the field that needs changing focused. */
  private void backTo(boolean signIn) {
    if (isNew) showForm(kind);
    View target = null;
    if (signIn) {
      for (Kinds.Field f : kind.fields) {
        if (f.advanced || !Kinds.signInField(f)) continue;
        if (f.type == Kinds.Type.FILE && fileButtons.get(f.key) != null) {
          target = fileButtons.get(f.key);
          break;
        }
        if (inputs.get(f.key) instanceof EditText) {
          target = inputs.get(f.key);
          break;
        }
      }
    } else {
      target = folderInput;
      // The folder is under More settings: unfold it to show the field.
      if (moreBox != null && moreBox.getVisibility() == View.GONE) toggleMore();
    }
    if (target != null) {
      View t = target;
      t.post(t::requestFocus);
    }
  }

  /** Keeps the server although the connection failed (it may be off right now). */
  private void saveAnyway() {
    if (run == null || run.running()) return;
    // The form as it is now (it may have been changed since the attempt).
    Servers.Server s;
    try {
      s = collect();
    } catch (IllegalArgumentException e) {
      if (isNew) showForm(kind);
      formStatus.setText(e.getMessage());
      return;
    }
    try {
      if (kind != null && (Kinds.browser(kind, s.params) || s.params.has("token"))) {
        // The service may have replaced the sign-in while connecting (even
        // when a later step failed): keep the one the engine has now.
        JSONObject now = new JSONObject(Engine.configValues(s.id));
        if (now.has("token")) s.params.put("token", now.get("token"));
      }
      Servers.get(this).save(s);
      saved = true;
      MainActivity.parkedId = s.id;
      MainActivity.parkedAt = android.os.SystemClock.elapsedRealtime();
      finish();
    } catch (Exception e) {
      android.util.Log.w("Driveway", "can't save " + s.label, e);
      formStatus.setText(R.string.save_anyway_failed);
      if (isNew) showForm(kind);
    }
  }

  // The finish: "<name> is in Files".

  private void showDone() {
    screen = Screen.DONE;
    clearPage();
    String name = run != null ? run.server.label : server.label;
    setTitle(getString(R.string.is_in_files, name));
    LinearLayout box = Ui.column(this);
    box.setGravity(Gravity.CENTER_HORIZONTAL);
    box.setPadding(0, Ui.dp(this, 24), 0, Ui.dp(this, 8));
    DrivewayArt art = new DrivewayArt(this, true);
    box.addView(art);
    TextView t = Ui.title(this, getString(R.string.is_in_files, name), 24);
    t.setGravity(Gravity.CENTER);
    t.setTextAlignment(View.TEXT_ALIGNMENT_CENTER);
    t.setPadding(0, Ui.dp(this, 16), 0, Ui.dp(this, 4));
    box.addView(t, Ui.matchWidth());
    TextView body = Ui.secondary(this, getString(R.string.is_in_files_body));
    body.setGravity(Gravity.CENTER);
    body.setTextAlignment(View.TEXT_ALIGNMENT_CENTER);
    box.addView(body, Ui.matchWidth());
    Layouts.Flow buttons = Ui.actions(this,
        Ui.button(this, getString(R.string.open_files), R.drawable.ic_open_in_new, v -> {
          MainActivity.openFiles(this, server.id);
          finish();
        }),
        Ui.flat(this, getString(R.string.done), v -> finish()));
    buttons.setPadding(0, Ui.dp(this, 12), 0, Ui.dp(this, 16));
    box.addView(buttons, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT,
        ViewGroup.LayoutParams.WRAP_CONTENT));
    col.addView(box);
    if (run != null) {
      checklist = null;
      LinearLayout c = checklistFor(run, col);
      if (c != null) col.addView(c);
    }
    if (!doneAnimated) {
      doneAnimated = true;
      art.arrive();
    }
    buttons.getChildAt(0).post(() -> buttons.getChildAt(0).requestFocus());
  }

  /** A required field left empty. */
  private static final class Missing extends IllegalArgumentException {
    private static final long serialVersionUID = 1L;

    final transient View field;

    Missing(String message, View field) {
      super(message);
      this.field = field;
    }
  }

  static final class Watcher implements TextWatcher {
    private final Runnable r;

    Watcher(Runnable r) {
      this.r = r;
    }

    @Override
    public void beforeTextChanged(CharSequence s, int start, int count, int after) {}

    @Override
    public void onTextChanged(CharSequence s, int start, int before, int count) {}

    @Override
    public void afterTextChanged(Editable s) {
      r.run();
    }
  }
}
