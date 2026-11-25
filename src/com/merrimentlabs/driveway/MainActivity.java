// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Rect;
import android.os.Bundle;
import android.os.SystemClock;
import android.provider.DocumentsContract;
import android.view.ContextMenu;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.KeyboardShortcutGroup;
import android.view.KeyboardShortcutInfo;
import android.view.Menu;
import android.view.MenuItem;
import android.view.View;
import android.widget.ImageButton;
import android.widget.LinearLayout;
import android.widget.PopupMenu;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.List;

/**
 * Your servers, each on a card you tap to open in Files, with anything that
 * needs a hand shown on its card (a save that didn't reach it, a sign-in that
 * ended, an identity that changed). Servers found on the network lead into
 * Add a server. Everyday use is in Files itself.
 */
public final class MainActivity extends Activity {
  static final String LOCAL_NETWORK = "android.permission.ACCESS_LOCAL_NETWORK";

  /** The page's width; wide enough for two columns of cards in an expanded window. */
  private static final int WIDE_PAGE = 1040;
  /**
   * Each card column's least width: two columns from a 600dp window (small
   * tablets, half-width desktop windows). A 600dp window
   * leaves 552dp inside the page's 24dp sides; two columns and the 12dp gap
   * take 540, which leaves 12dp for a side inset (a display cutout). Large
   * text widens the column (Layouts.Grid), so at 200% the cards stay in one
   * column until about 850dp.
   */
  static final int CARD_COLUMN = 264;

  private LinearLayout localNetwork;
  private LinearLayout foundLine;
  private Layouts.Grid cards;
  private LinearLayout empty;
  private Discovery discovery;
  private boolean looking;
  private List<Discovery.Found> found = new ArrayList<>();
  /** The server whose menu is open. */
  private Servers.Server menuFor;
  private final Runnable refresh = this::showServers;

  /** A server saved a moment ago: its card parks when this screen shows. */
  static volatile String parkedId;
  static volatile long parkedAt;
  private static final long PARK_MS = 4000;

  @Override
  protected void onCreate(Bundle savedInstanceState) {
    super.onCreate(savedInstanceState);
    // Notes this version for the New labels on every start, so an update
    // labels only what it added since the version last run.
    NewLabelRows.store(this);
    // The weekly update check's job, armed again after a device restart
    // (only when the person turned the check on).
    UpdateJob.sync(this);
    LinearLayout col = Ui.page(this, WIDE_PAGE);
    setTitle(R.string.app_name);

    LinearLayout top = Ui.row(this);
    top.setGravity(Gravity.TOP);
    LinearLayout titles = Ui.column(this);
    titles.addView(Ui.title(this, getString(R.string.app_name), 28));
    titles.addView(Ui.secondary(this, getString(R.string.tagline)));
    top.addView(titles, Ui.weight());
    ImageButton gear = Ui.iconButton(this, R.drawable.ic_settings, getString(R.string.settings), v -> openSettings());
    LinearLayout.LayoutParams glp = new LinearLayout.LayoutParams(Ui.dp(this, 48), Ui.dp(this, 48));
    top.addView(gear, glp);
    col.addView(top);
    col.addView(Ui.gap(this, 12));

    // Nearby devices, when it is off and there are servers to reach.
    localNetwork = Ui.column(this);
    localNetwork.setBackground(Ui.rounded(this, R.color.accent_container, 16));
    localNetwork.setPadding(Ui.dp(this, 16), Ui.dp(this, 12), Ui.dp(this, 16), Ui.dp(this, 4));
    TextView needed = Ui.text(this, getString(R.string.local_network_needed), 14, 500);
    needed.setTextColor(Ui.color(this, R.color.on_accent_container));
    Ui.announces(needed);
    localNetwork.addView(needed);
    // Android's dialog while it still asks; App info once it was refused for good.
    Ui.addStart(localNetwork, Ui.flat(this, getString(R.string.allow_nearby), v -> Permissions.allow(this, ASK_BANNER, LOCAL_NETWORK)));
    LinearLayout.LayoutParams blp = Ui.matchWidth();
    blp.bottomMargin = Ui.dp(this, 12);
    col.addView(localNetwork, blp);

    // "2 servers found on your network": leads into Add a server.
    foundLine = Ui.column(this);
    LinearLayout.LayoutParams flp = Ui.matchWidth();
    flp.bottomMargin = Ui.dp(this, 12);
    col.addView(foundLine, flp);

    TextView mine = Ui.heading(this, getString(R.string.your_servers));
    col.addView(mine);
    cards = new Layouts.Grid(this, CARD_COLUMN, 2, 12);
    col.addView(cards);
    empty = emptyState();
    col.addView(empty);

    col.addView(Ui.gap(this, 16));
    col.addView(Ui.actions(this,
        Ui.button(this, getString(R.string.add_server), R.drawable.ic_add, v -> withPermissions(ASK_ADD)),
        Ui.flat(this, getString(R.string.open_files), R.drawable.ic_open_in_new, v -> openFiles(null))));

    discovery = new Discovery(this, this::showFound);
  }

  private LinearLayout emptyState() {
    LinearLayout box = Ui.card(this);
    box.setGravity(Gravity.CENTER_HORIZONTAL);
    box.setPadding(Ui.dp(this, 24), Ui.dp(this, 24), Ui.dp(this, 24), Ui.dp(this, 24));
    box.addView(new DrivewayArt(this, false));
    TextView t = Ui.text(this, getString(R.string.no_servers_title), 16, 500);
    t.setGravity(Gravity.CENTER);
    t.setPadding(0, Ui.dp(this, 12), 0, 0);
    box.addView(t);
    TextView s = Ui.secondary(this, getString(R.string.no_servers));
    s.setGravity(Gravity.CENTER);
    box.addView(s);
    return box;
  }

  private static final int ASK_ADD = 1;
  private static final int ASK_LOOK = 2;
  private static final int ASK_ADD_FOUND = 3;
  private static final int ASK_BANNER = 5;
  /** A found server's Add, waiting for the permission answer. */
  private Intent addFound;

  /**
   * Asks for what Driveway needs only when it first needs it: the first time
   * you add a server or look for one. Local network reaches servers at home
   * (Android 17+); notifications say a save failed, with Try again.
   */
  private void withPermissions(int then) {
    List<String> ask = new ArrayList<>();
    if (!hasLocalNetwork(this)) ask.add(LOCAL_NETWORK);
    if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
      ask.add(Manifest.permission.POST_NOTIFICATIONS);
    }
    if (ask.isEmpty()) {
      proceed(then);
    } else {
      Permissions.request(this, ask.toArray(new String[0]), then);
    }
  }

  private void proceed(int what) {
    if (what == ASK_ADD) {
      startActivity(new Intent(this, ServerActivity.class));
    } else if (what == ASK_ADD_FOUND && addFound != null) {
      startActivity(addFound);
      addFound = null;
    } else if (what == ASK_LOOK && hasLocalNetwork(this)) {
      startLooking();
    }
  }

  private void startLooking() {
    // Called again whenever the window comes forward: only a new look
    // changes what's shown.
    if (looking) return;
    found = new ArrayList<>();
    showFoundLine();
    discovery.start();
    looking = true;
  }

  private void showPermissionState() {
    boolean granted = hasLocalNetwork(this);
    // The banner only matters once there are servers to reach.
    localNetwork.setVisibility(!granted && !Servers.get(this).all().isEmpty() ? View.VISIBLE : View.GONE);
    if (granted) startLooking();
  }

  @Override
  protected void onResume() {
    super.onResume();
    Uploads.recover(this);
    Uploads.listen(refresh);
    Problems.listen(refresh);
    showServers();
    showPermissionState();
  }

  /*
   * In desktop windowing (Googlebooks), Settings opens in a window of its own
   * and Driveway stays resumed, so onResume doesn't run on the way back:
   * check the permission again whenever Driveway's window comes forward.
   */
  @Override
  public void onTopResumedActivityChanged(boolean top) {
    super.onTopResumedActivityChanged(top);
    if (top) showPermissionState();
  }

  @Override
  public void onWindowFocusChanged(boolean focused) {
    super.onWindowFocusChanged(focused);
    if (focused) showPermissionState();
  }

  @Override
  protected void onPause() {
    super.onPause();
    Uploads.unlisten(refresh);
    Problems.unlisten(refresh);
    discovery.stop();
    looking = false;
  }

  // The server cards.

  /** What the cards showed last: an update that changes nothing leaves them (and screen readers) alone. */
  private String shown;

  private String cardsState(List<Servers.Server> all) {
    StringBuilder b = new StringBuilder();
    for (Servers.Server s : all) {
      b.append(s.id).append('|').append(s.label).append('|').append(s.summary()).append('|').append(s.folder)
          .append('|').append(Problems.of(this, s.id));
      for (Uploads.Failed f : Uploads.failedFor(s.id)) b.append('|').append(f.path).append(f.file.getName()).append(f.why);
      b.append('\n');
    }
    return b.toString();
  }

  private void showServers() {
    List<Servers.Server> current = Servers.get(this).all();
    String state = cardsState(current);
    if (state.equals(shown) && parkedId == null) return;
    shown = state;
    View focused = getCurrentFocus();
    String focusedTag = focused != null && focused.getTag() instanceof String ? (String) focused.getTag() : null;
    cards.removeAllViews();
    List<Servers.Server> all = current;
    empty.setVisibility(all.isEmpty() ? View.VISIBLE : View.GONE);
    String parked = parkedId != null && SystemClock.elapsedRealtime() - parkedAt < PARK_MS ? parkedId : null;
    for (Servers.Server s : all) {
      LinearLayout card = card(s, s.id.equals(parked));
      cards.addView(card);
      if (s.id.equals(parked)) park(card);
    }
    parkedId = null;
    // A rebuilt list keeps the keyboard where it was (Try again, a menu).
    if (focusedTag != null) {
      View again = cards.findViewWithTag(focusedTag);
      if (again != null) again.requestFocus();
    }
  }

  /**
   * A server's second line, only where it says something the name doesn't:
   * the kind and address (and starting folder) of a
   * server; for a cloud service, its name only when the server's name
   * differs from it ("Work files · Dropbox"), and the folder. Empty: the
   * row is one line.
   */
  static String where(Servers.Server s) {
    String kind = Kinds.label(s.backend);
    String summary = s.summary();
    List<String> parts = new ArrayList<>();
    if (!summary.equals(kind)) {
      parts.add(summary); // kind · host
    } else if (s.label == null || !s.label.trim().equalsIgnoreCase(kind)) {
      parts.add(kind);
    }
    if (s.folder != null && !s.folder.isEmpty()) parts.add(s.folder);
    return String.join(" · ", parts);
  }

  private LinearLayout card(Servers.Server s, boolean parked) {
    LinearLayout card = Ui.card(this);
    String where = where(s);

    // A compact row: the tile, the name, a second line
    // only when it adds something, and a status line only when the server
    // needs attention; being in Files is the normal state and isn't said.
    // The row is the button that opens the server in Files: one stop for
    // screen readers ("Open Family photos in Files. SMB · nas.local · photos").
    LinearLayout head = Ui.row(this);
    head.setPaddingRelative(Ui.dp(this, 16), Ui.dp(this, 8), Ui.dp(this, 4), Ui.dp(this, 8));
    head.setGravity(Gravity.CENTER_VERTICAL);
    head.setMinimumHeight(Ui.dp(this, 64));
    LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(Ui.dp(this, 40), Ui.dp(this, 40));
    tlp.setMarginEnd(Ui.dp(this, 16));
    head.addView(Ui.serviceTile(this, s.backend), tlp);
    LinearLayout texts = Ui.column(this);
    texts.setPadding(0, Ui.dp(this, 4), 0, Ui.dp(this, 4));
    texts.addView(Ui.text(this, s.label, 16, 500));
    if (!where.isEmpty()) texts.addView(Ui.secondary(this, where));
    // A service Driveway no longer works with: not in Files, and Remove is
    // the one thing to do. Just added: "Parked in Files" for a moment.
    boolean removed = Kinds.removed(s.backend);
    TextView state = null;
    if (removed || parked) {
      state = Ui.secondary(this, getString(removed ? R.string.not_in_files : R.string.parked));
      if (removed) state.setTextColor(Ui.color(this, R.color.trust_ink));
      else state.setCompoundDrawablesRelativeWithIntrinsicBounds(small(R.drawable.ic_check), null, null, null);
      state.setCompoundDrawablePadding(Ui.dp(this, 4));
      state.setPadding(0, Ui.dp(this, 2), 0, 0);
      texts.addView(state);
    }
    head.addView(texts, Ui.weight());
    head.setTag(s.id);
    String whereSaid = where.isEmpty() ? "" : ". " + where;
    if (removed) {
      head.setContentDescription(s.label + whereSaid + ". " + getString(R.string.not_in_files));
    } else {
      head.setOnClickListener(v -> openFiles(s.id));
      head.setContentDescription(getString(R.string.open_named, s.label) + whereSaid);
      Ui.pressable(head, 16);
    }
    // Long press, right-click, the Menu key or Shift+F10: Open, Change, Remove.
    registerForContextMenu(head);
    head.setOnContextClickListener(View::showContextMenu);

    ImageButton more = Ui.iconButton(this, R.drawable.ic_more_vert, getString(R.string.more_named, s.label), v -> menu(v, s));
    more.setTag(s.id + "/more");
    LinearLayout.LayoutParams mlp = new LinearLayout.LayoutParams(Ui.dp(this, 48), Ui.dp(this, 48));
    head.addView(more, mlp);
    card.addView(head);
    if (parked && state != null) {
      // The moment passes: the normal state isn't said.
      TextView shown = state;
      shown.postDelayed(() -> shown.setVisibility(View.GONE), PARK_MS);
    }

    // Anything that needs a hand, on the card.
    LinearLayout problems = Ui.column(this);
    problems.setPadding(Ui.dp(this, 12), 0, Ui.dp(this, 12), 0);
    String problem = Problems.of(this, s.id);
    if (removed) {
      View remove = Ui.tonal(this, getString(R.string.remove), v -> confirmRemove(s));
      remove.setContentDescription(getString(R.string.remove_named, s.label));
      remove.setTag(s.id + "/remove");
      problems.addView(Ui.strip(this, true, getString(Kinds.layered(s.backend) ? R.string.problem_removed_layered : R.string.problem_removed,
          Kinds.label(s.backend)), remove), stripParams());
    } else if (Problems.SIGN_IN.equals(problem)) {
      // Signed in through the browser before Driveway stopped offering its
      // service's browser sign-in: no signing in again (it would borrow
      // rclone's registration), Change where the form has another way in.
      Kinds.Kind kind = Kinds.of(s.backend);
      boolean again = Kinds.signsInAgain(kind, s.params);
      if (!again && !Kinds.changeRestores(kind)) {
        // No way to sign in again: the card says so and offers Remove.
        View remove = Ui.tonal(this, getString(R.string.remove), v -> confirmRemove(s));
        remove.setContentDescription(getString(R.string.remove_named, s.label));
        remove.setTag(s.id + "/remove");
        problems.addView(Ui.strip(this, true, getString(R.string.problem_signin_none, s.label, Kinds.label(s.backend)), remove),
            stripParams());
      } else {
        View fix = Ui.tonal(this, getString(again ? R.string.sign_in_again : R.string.edit), v -> fix(s, ServerActivity.FIX_SIGN_IN));
        fix.setContentDescription(getString(again ? R.string.sign_in_again_named : R.string.edit_named, s.label));
        fix.setTag(s.id + "/fix");
        problems.addView(Ui.strip(this, true, getString(R.string.problem_signin, s.label), fix), stripParams());
      }
    } else if (Problems.IDENTITY.equals(problem) || Problems.UNCONFIRMED.equals(problem)) {
      View fix = Ui.tonal(this, getString(R.string.check_identity), v -> fix(s, ServerActivity.FIX_IDENTITY));
      fix.setContentDescription(getString(R.string.check_identity_named, s.label));
      fix.setTag(s.id + "/fix");
      boolean changed = Problems.IDENTITY.equals(problem);
      problems.addView(Ui.strip(this, changed, getString(changed ? R.string.problem_identity : R.string.problem_unconfirmed,
          s.label), fix), stripParams());
    }
    List<Uploads.Failed> failed = Uploads.failedFor(s.id);
    for (int i = 0; i < failed.size() && i < 3; i++) {
      Uploads.Failed f = failed.get(i);
      View again = Ui.tonal(this, getString(R.string.try_again), R.drawable.ic_refresh,
          v -> Uploads.retry(this, f.serverId, f.path, f.file));
      again.setContentDescription(getString(R.string.try_again_named, f.path, s.label));
      again.setTag(s.id + "/again/" + f.path);
      View discard = Ui.flat(this, getString(R.string.discard), v -> confirmDiscard(s, f));
      discard.setContentDescription(getString(R.string.discard_named, f.path, s.label));
      discard.setTag(s.id + "/discard/" + f.path);
      // Why it didn't arrive, as the notification says (full, offline...).
      String said = getString(R.string.save_waiting, f.name(), s.label) + (f.why == null ? "" : " " + Errors.closed(f.why));
      problems.addView(Ui.strip(this, false, said, again, discard), stripParams());
    }
    if (failed.size() > 3) {
      TextView more3 = Ui.secondary(this, getResources().getQuantityString(R.plurals.saves_waiting_more, failed.size() - 3, failed.size() - 3));
      more3.setPadding(Ui.dp(this, 4), 0, 0, Ui.dp(this, 8));
      problems.addView(more3);
    }
    if (problems.getChildCount() > 0) card.addView(problems);
    return card;
  }

  private LinearLayout.LayoutParams stripParams() {
    LinearLayout.LayoutParams lp = Ui.matchWidth();
    lp.bottomMargin = Ui.dp(this, 12);
    return lp;
  }

  private android.graphics.drawable.Drawable small(int icon) {
    android.graphics.drawable.Drawable d = getDrawable(icon).mutate();
    int size = Ui.dp(this, 16);
    d.setBounds(0, 0, size, size);
    d.setTint(Ui.color(this, R.color.muted));
    return d;
  }

  /**
   * The small moment of a server arriving: its card slides into place
   * (200ms) and says "Parked in Files" for a few seconds. With Android's
   * animations off it simply says so.
   */
  private void park(View card) {
    if (!Ui.motion()) return;
    card.setTranslationY(Ui.dp(this, 24));
    card.setAlpha(0f);
    card.animate().translationY(0).alpha(1f).setDuration(200).setStartDelay(100).setInterpolator(Ui.EASE_OUT).start();
  }

  private void fix(Servers.Server s, String what) {
    startActivity(new Intent(this, ServerActivity.class).putExtra(ServerActivity.EXTRA_ID, s.id)
        .putExtra(ServerActivity.EXTRA_FIX, what));
  }

  private void edit(Servers.Server s) {
    startActivity(new Intent(this, ServerActivity.class).putExtra(ServerActivity.EXTRA_ID, s.id));
  }

  private void menu(View anchor, Servers.Server s) {
    PopupMenu p = new PopupMenu(this, anchor, Gravity.END);
    if (!Kinds.removed(s.backend)) {
      p.getMenu().add(Menu.NONE, MENU_OPEN, Menu.NONE, R.string.open_in_files);
      p.getMenu().add(Menu.NONE, MENU_EDIT, Menu.NONE, R.string.edit);
    }
    p.getMenu().add(Menu.NONE, MENU_REMOVE, Menu.NONE, R.string.remove);
    p.setOnMenuItemClickListener(item -> act(item.getItemId(), s));
    p.show();
  }

  private static final int MENU_OPEN = 1, MENU_EDIT = 2, MENU_REMOVE = 3;

  private boolean act(int what, Servers.Server s) {
    switch (what) {
      case MENU_OPEN:
        openFiles(s.id);
        return true;
      case MENU_EDIT:
        edit(s);
        return true;
      case MENU_REMOVE:
        confirmRemove(s);
        return true;
      default:
        return false;
    }
  }

  @Override
  public void onCreateContextMenu(ContextMenu menu, View v, ContextMenu.ContextMenuInfo info) {
    Servers.Server s = v.getTag() instanceof String ? Servers.get(this).find((String) v.getTag()) : null;
    if (s == null) return;
    menuFor = s;
    menu.setHeaderTitle(s.label);
    if (!Kinds.removed(s.backend)) {
      menu.add(Menu.NONE, MENU_OPEN, Menu.NONE, R.string.open_in_files);
      menu.add(Menu.NONE, MENU_EDIT, Menu.NONE, R.string.edit);
    }
    menu.add(Menu.NONE, MENU_REMOVE, Menu.NONE, R.string.remove);
  }

  @Override
  public boolean onContextItemSelected(MenuItem item) {
    Servers.Server s = menuFor;
    if (s == null) return false;
    return act(item.getItemId(), s) || super.onContextItemSelected(item);
  }

  // Keyboard: Ctrl+N adds a server, Ctrl+comma opens Settings; Meta+/ (the
  // shortcut list) shows both.

  @Override
  public boolean onKeyShortcut(int keyCode, KeyEvent event) {
    if (keyCode == KeyEvent.KEYCODE_N && event.isCtrlPressed()) {
      // A held key repeats: one Add screen.
      if (event.getRepeatCount() == 0) withPermissions(ASK_ADD);
      return true;
    }
    if (keyCode == KeyEvent.KEYCODE_COMMA && event.isCtrlPressed()) {
      if (event.getRepeatCount() == 0) openSettings();
      return true;
    }
    return super.onKeyShortcut(keyCode, event);
  }

  /** The Menu key, or Shift+F10, opens the focused server's menu (as a right-click does). */
  @Override
  public boolean onKeyUp(int keyCode, KeyEvent event) {
    View focus = getCurrentFocus();
    boolean menuKey = keyCode == KeyEvent.KEYCODE_MENU
        || (keyCode == KeyEvent.KEYCODE_F10 && event.isShiftPressed());
    if (menuKey && focus != null && focus.getTag() instanceof String && Servers.get(this).find((String) focus.getTag()) != null) {
      focus.showContextMenu();
      return true;
    }
    return super.onKeyUp(keyCode, event);
  }

  @Override
  public void onProvideKeyboardShortcuts(List<KeyboardShortcutGroup> data, Menu menu, int deviceId) {
    List<KeyboardShortcutInfo> keys = new ArrayList<>();
    keys.add(new KeyboardShortcutInfo(getString(R.string.add_server), KeyEvent.KEYCODE_N, KeyEvent.META_CTRL_ON));
    keys.add(new KeyboardShortcutInfo(getString(R.string.settings), KeyEvent.KEYCODE_COMMA, KeyEvent.META_CTRL_ON));
    data.add(new KeyboardShortcutGroup(getString(R.string.app_name), keys));
  }

  private void openSettings() {
    startActivity(new Intent(this, SettingsActivity.class));
  }

  private void confirmRemove(Servers.Server s) {
    // Saves that haven't reached the server yet are deleted with it: say so.
    int unsent = Uploads.unsent(this, s.id);
    String message = getString(R.string.remove_message);
    if (unsent > 0) message += "\n\n" + getResources().getQuantityString(R.plurals.remove_unsent, unsent, unsent);
    AlertDialog d = new AlertDialog.Builder(this)
        .setTitle(getString(R.string.remove_title, s.label))
        .setMessage(message)
        .setPositiveButton(R.string.remove, (x, w) -> {
          try {
            Servers.get(this).remove(s.id);
          } catch (Exception e) {
            android.util.Log.w("Driveway", "can't remove " + s.label, e);
            new AlertDialog.Builder(this)
                .setMessage(getString(R.string.remove_failed, s.label))
                .setPositiveButton(android.R.string.ok, null)
                .show();
          }
          showServers();
        })
        .setNegativeButton(R.string.cancel, null)
        .show();
    // Enter keeps the server.
    Ui.focus(d, AlertDialog.BUTTON_NEGATIVE);
  }

  /** Discard can't be undone (the only copy of that save goes): ask first, Cancel focused. */
  private void confirmDiscard(Servers.Server s, Uploads.Failed f) {
    AlertDialog d = new AlertDialog.Builder(this)
        .setTitle(getString(R.string.discard_save_title, f.name()))
        .setMessage(getString(R.string.discard_save_message, s.label))
        .setPositiveButton(R.string.discard, (x, w) -> Uploads.discardIfFailed(this, f))
        .setNegativeButton(R.string.cancel, null)
        .show();
    Ui.focus(d, AlertDialog.BUTTON_NEGATIVE);
  }

  // Found on the network.

  private void showFound(List<Discovery.Found> list) {
    found = list;
    showFoundLine();
  }

  private void showFoundLine() {
    foundLine.removeAllViews();
    if (found.isEmpty()) {
      foundLine.setVisibility(View.GONE);
      return;
    }
    foundLine.setVisibility(View.VISIBLE);
    StringBuilder names = new StringBuilder();
    for (int i = 0; i < found.size() && i < 3; i++) {
      if (i > 0) names.append(", ");
      names.append(found.get(i).name).append(" · ").append(Kinds.label(found.get(i).backend));
    }
    if (found.size() > 3) names.append(", …");
    LinearLayout r = Ui.cardRow(this, Ui.icon(this, R.drawable.ic_wifi, R.color.on_accent_container, 24),
        getResources().getQuantityString(R.plurals.found_count, found.size(), found.size()), names,
        Ui.icon(this, R.drawable.ic_chevron_right, R.color.on_accent_container, 24), v -> withPermissions(ASK_ADD));
    r.setBackground(null);
    Ui.pressable(r, 16);
    foundLine.setBackground(Ui.rounded(this, R.color.accent_container, 16));
    foundLine.addView(r);
  }

  static boolean hasLocalNetwork(android.content.Context c) {
    try {
      c.getPackageManager().getPermissionInfo(LOCAL_NETWORK, 0);
    } catch (PackageManager.NameNotFoundException e) {
      return true; // Android before 17: no such permission, nothing to ask
    }
    return c.checkSelfPermission(LOCAL_NETWORK) == PackageManager.PERMISSION_GRANTED;
  }

  @Override
  public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] results) {
    showPermissionState();
    // Adding goes ahead either way: without the permission, the checklist
    // says what's missing.
    proceed(requestCode);
  }

  /** Files at the server's top folder; for null, at Driveway's first server (Files itself with none). */
  void openFiles(String serverId) {
    openFiles(this, serverId);
  }

  /**
   * Files opens beside Driveway on a large screen: from a window at least
   * 600dp wide, or one already sharing the screen. On a phone it opens over
   * Driveway.
   */
  static boolean opensAdjacent(Activity a) {
    return a.isInMultiWindowMode() || a.getResources().getConfiguration().screenWidthDp >= 600;
  }

  /**
   * Whether Driveway's window floats (desktop windowing on a Googlebook): in
   * a multi-window mode, but short of the screen both ways. A split-screen
   * window spans the screen one way (its width or its height).
   */
  static boolean freeform(Activity a) {
    if (!a.isInMultiWindowMode()) return false;
    android.view.WindowManager wm = a.getWindowManager();
    Rect w = wm.getCurrentWindowMetrics().getBounds(), d = wm.getMaximumWindowMetrics().getBounds();
    return w.width() < d.width() && w.height() < d.height();
  }

  /** Least width for Files placed beside a floating Driveway window. */
  static final int FILES_MIN_DP = 400;

  /**
   * Where Files goes beside a floating Driveway window w on the screen d: on
   * the side with more room, the same top and height, at least minW wide and
   * as wide as Driveway where it fits, gap from Driveway. With too little room
   * on both sides, the half of the screen away from Driveway (it overlaps).
   */
  static Rect besideBounds(Rect w, Rect d, int minW, int gap) {
    int left = w.left - d.left - gap, right = d.right - w.right - gap;
    int room = Math.max(left, right);
    if (room >= minW) {
      int width = Math.min(room, Math.max(minW, w.width()));
      return right >= left ? new Rect(w.right + gap, w.top, w.right + gap + width, w.bottom)
          : new Rect(w.left - gap - width, w.top, w.left - gap, w.bottom);
    }
    return w.centerX() <= d.centerX() ? new Rect(d.centerX(), w.top, d.right, w.bottom)
        : new Rect(d.left, w.top, d.centerX(), w.bottom);
  }

  /**
   * Files beside Driveway: a window of its own next to a floating Driveway
   * window; split screen from a large or shared window; on a phone, over
   * Driveway.
   */
  static void startBeside(Activity a, Intent i) {
    if (freeform(a)) {
      android.view.WindowManager wm = a.getWindowManager();
      Rect b = besideBounds(wm.getCurrentWindowMetrics().getBounds(), wm.getMaximumWindowMetrics().getBounds(),
          Ui.dp(a, FILES_MIN_DP), Ui.dp(a, 8));
      i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
      a.startActivity(i, android.app.ActivityOptions.makeBasic().setLaunchBounds(b).toBundle());
      return;
    }
    if (opensAdjacent(a)) i.addFlags(Intent.FLAG_ACTIVITY_LAUNCH_ADJACENT | Intent.FLAG_ACTIVITY_NEW_TASK);
    a.startActivity(i);
  }

  /**
   * The server Open Files shows: the first in Files' own list (the order of
   * the cards, a service Driveway no longer opens left out). Files lists
   * every server in its sidebar, so the others are one tap away.
   */
  static String firstRoot(Activity a) {
    for (Servers.Server s : Servers.get(a).all()) {
      if (!Kinds.removed(s.backend)) return s.id;
    }
    return null;
  }

  static void openFiles(Activity a, String serverId) {
    Intent i = new Intent(Intent.ACTION_VIEW);
    // A server's own root, which Files opens at: the authority's list of roots
    // left Files to choose (on a Googlebook it showed Recent or Downloads).
    if (serverId == null) serverId = firstRoot(a);
    if (serverId != null) {
      i.setDataAndType(DocumentsContract.buildRootUri(Servers.AUTHORITY, serverId), DocumentsContract.Root.MIME_TYPE_ITEM);
    } else {
      // No server yet: Files itself.
      i.setDataAndType(DocumentsContract.buildRootsUri(Servers.AUTHORITY), DocumentsContract.Root.MIME_TYPE_ITEM);
    }
    i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
    try {
      startBeside(a, i);
    } catch (ActivityNotFoundException e) {
      // Files without root links: the Downloads view, from which the sidebar shows Driveway's servers.
      try {
        startBeside(a, new Intent(DownloadManager.ACTION_VIEW_DOWNLOADS));
      } catch (ActivityNotFoundException e2) {
        new AlertDialog.Builder(a)
            .setTitle(R.string.no_files_title)
            .setMessage(R.string.no_files)
            .setPositiveButton(android.R.string.ok, null)
            .show();
      }
    }
  }
}
