// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * Settings: the two permissions Android lets you switch off (so it's clear
 * why a server can't be reached or a failed save went unannounced), then the
 * About block every Merriment Labs app shares, in the same order: the app and
 * its version, Merriment Labs, Source code, Privacy, Open-source notices,
 * License, Check for updates with its weekly switch; then Driveway's Help and
 * Contact; and the copyright line under the card.
 */
public final class SettingsActivity extends Activity {
  static final String HELP = "https://merrimentlabs.com/support/driveway";
  static final String PRIVACY = "https://merrimentlabs.com/privacy/driveway";
  static final String CONTACT = "hello@merrimentlabs.com";
  static final String MAKER = "https://merrimentlabs.com";
  static final String SOURCE = "https://github.com/" + UpdateCheck.REPO;
  static final String LICENSE = SOURCE + "/blob/main/LICENSE";

  private LinearLayout permissions;
  private TextView updateState;
  private boolean checking;

  @Override
  protected void onCreate(Bundle savedInstanceState) {
    super.onCreate(savedInstanceState);
    LinearLayout col = Ui.page(this);
    col.addView(Ui.topBar(this, getString(R.string.settings), v -> finish()));

    col.addView(Ui.heading(this, getString(R.string.permissions)));
    permissions = Ui.card(this);
    col.addView(permissions);

    col.addView(Ui.heading(this, getString(R.string.about)));
    LinearLayout about = Ui.card(this);
    String version = version();
    about.addView(Ui.cardRow(this, appIcon(), getString(R.string.about_title, version), getString(R.string.tagline), null, null));
    about.addView(Ui.divider(this));
    // The block every Merriment Labs app shares.
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_language, R.color.muted, 24), getString(R.string.maker),
        getString(R.string.maker_sub), Ui.external(this), v -> open(MAKER)));
    about.addView(Ui.divider(this));
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_terminal, R.color.muted, 24), getString(R.string.source),
        getString(R.string.source_sub), Ui.external(this), v -> open(SOURCE)));
    about.addView(Ui.divider(this));
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_shield, R.color.muted, 24), getString(R.string.privacy),
        getString(R.string.privacy_sub), Ui.external(this), v -> open(PRIVACY)));
    about.addView(Ui.divider(this));
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_description, R.color.muted, 24), getString(R.string.licenses),
        getString(R.string.licenses_sub), Ui.chevron(this), v -> startActivity(new Intent(this, LicensesActivity.class))));
    about.addView(Ui.divider(this));
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_description, R.color.muted, 24), getString(R.string.license),
        getString(R.string.license_sub), Ui.external(this), v -> open(LICENSE)));
    about.addView(Ui.divider(this));
    LinearLayout update = Ui.cardRow(this, Ui.icon(this, R.drawable.ic_refresh, R.color.muted, 24),
        getString(R.string.update_check), getString(R.string.update_check_sub), null, v -> checkForUpdates());
    updateState = (TextView) ((LinearLayout) update.getChildAt(1)).getChildAt(1);
    Ui.announces(updateState);
    about.addView(update);
    about.addView(weeklyRow());
    about.addView(Ui.divider(this));
    // Driveway's own rows, below the shared block.
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_help, R.color.muted, 24), getString(R.string.help),
        getString(R.string.help_sub), Ui.external(this), v -> open(HELP)));
    about.addView(Ui.divider(this));
    about.addView(Ui.cardRow(this, Ui.icon(this, R.drawable.ic_mail, R.color.muted, 24), getString(R.string.contact),
        CONTACT, Ui.external(this), v -> email(version)));
    col.addView(about);

    TextView copyright = Ui.secondary(this, getString(R.string.copyright));
    copyright.setTextSize(12.5f);
    copyright.setPadding(Ui.dp(this, 4), Ui.dp(this, 12), Ui.dp(this, 4), 0);
    col.addView(copyright);
  }

  /**
   * Check for updates: one question to GitHub, its answer under the row,
   * and the release page opened when there is a newer version.
   */
  private void checkForUpdates() {
    if (checking) return;
    String version = version();
    checking = true;
    updateState.setText(R.string.update_checking);
    new Thread(() -> {
      UpdateCheck.Result r = UpdateCheck.check(version, UpdateCheck.API);
      runOnUiThread(() -> {
        checking = false;
        if (isDestroyed()) return;
        updateState.setText(updateWords(r));
        if (r.outcome == UpdateCheck.Outcome.AVAILABLE) open(UpdateCheck.RELEASES_PAGE);
      });
    }).start();
  }

  private String updateWords(UpdateCheck.Result r) {
    switch (r.outcome) {
      case AVAILABLE:
        return getString(R.string.update_available, r.version);
      case UP_TO_DATE:
        return getString(R.string.update_latest);
      case NO_RELEASE:
        return getString(R.string.update_no_release);
      case OFFLINE:
        return getString(R.string.update_offline);
      default:
        return getString(R.string.update_failed);
    }
  }

  /**
   * Check weekly, off until turned on. Turning it on asks for notifications
   * (where they're still off), since that is how a newer version is told.
   */
  private View weeklyRow() {
    UpdateCheck.Store store = UpdateJob.store(this);
    android.widget.Switch sw = new android.widget.Switch(this);
    sw.setChecked(UpdateCheck.weekly(store));
    sw.setClickable(false);
    sw.setFocusable(false);
    sw.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    LinearLayout r = Ui.cardRow(this, Ui.icon(this, R.drawable.ic_notifications, R.color.muted, 24),
        getString(R.string.update_weekly), getString(R.string.update_weekly_sub), sw, v -> {
          boolean on = !UpdateCheck.weekly(store);
          UpdateCheck.setWeekly(store, on);
          sw.setChecked(on);
          v.setStateDescription(getString(on ? R.string.on : R.string.off));
          UpdateJob.sync(this);
          if (on && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            Permissions.allow(this, ASK_NOTIFY, Manifest.permission.POST_NOTIFICATIONS);
          }
        });
    // Read as a switch with its state.
    r.setAccessibilityDelegate(new View.AccessibilityDelegate() {
      @Override
      public void onInitializeAccessibilityNodeInfo(View host, android.view.accessibility.AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(host, info);
        info.setClassName(android.widget.Switch.class.getName());
        info.setCheckable(true);
        checked(info, UpdateCheck.weekly(store));
      }
    });
    r.setStateDescription(getString(sw.isChecked() ? R.string.on : R.string.off));
    return r;
  }

  /** A checkable node's state: the int form from Android 16, the boolean form (deprecated there) before it. */
  @SuppressWarnings("deprecation") // setChecked(boolean) is the only form before Android 16 (API 36)
  private static void checked(android.view.accessibility.AccessibilityNodeInfo info, boolean on) {
    if (android.os.Build.VERSION.SDK_INT >= 36) {
      info.setChecked(on ? android.view.accessibility.AccessibilityNodeInfo.CHECKED_STATE_TRUE
          : android.view.accessibility.AccessibilityNodeInfo.CHECKED_STATE_FALSE);
    } else {
      info.setChecked(on);
    }
  }

  private View appIcon() {
    FrameLayout f = new FrameLayout(this);
    f.setBackground(Ui.rounded(this, R.color.icon_background, 12));
    ImageView v = new ImageView(this);
    v.setImageResource(R.drawable.ic_launcher_foreground);
    f.addView(v, new FrameLayout.LayoutParams(Ui.dp(this, 40), Ui.dp(this, 40)));
    f.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
    f.setLayoutParams(new LinearLayout.LayoutParams(Ui.dp(this, 40), Ui.dp(this, 40)));
    return f;
  }

  private String version() {
    try {
      return getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
    } catch (PackageManager.NameNotFoundException e) {
      return "";
    }
  }

  @Override
  protected void onResume() {
    super.onResume();
    showPermissions();
  }

  // In a desktop window, Android's Settings opens beside Driveway, which
  // stays resumed: look again whenever the window comes forward.
  @Override
  public void onWindowFocusChanged(boolean focused) {
    super.onWindowFocusChanged(focused);
    if (focused) showPermissions();
  }

  private void showPermissions() {
    permissions.removeAllViews();
    boolean nearby = MainActivity.hasLocalNetwork(this);
    boolean notify = checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
    permissions.addView(permissionRow(R.drawable.ic_wifi, R.string.nearby_devices,
        nearby ? R.string.nearby_on : R.string.nearby_off, nearby));
    permissions.addView(Ui.divider(this));
    permissions.addView(permissionRow(R.drawable.ic_notifications, R.string.notifications,
        notify ? R.string.notifications_on : R.string.notifications_off, notify));
  }

  private static final int ASK_NEARBY = 1, ASK_NOTIFY = 2;

  /**
   * A permission and its state; when it's off, an Allow button under the words
   * (they keep the width at large text): Android's dialog while it still asks,
   * App info once the permission was refused for good.
   */
  private View permissionRow(int icon, int name, int state, boolean on) {
    LinearLayout r = Ui.cardRow(this, Ui.icon(this, icon, R.color.muted, 24), getString(name), getString(state), null, null);
    if (!on) {
      boolean nearby = name == R.string.nearby_devices;
      View b = Ui.flat(this, getString(nearby ? R.string.allow_nearby : R.string.allow_notifications), v -> {
        if (nearby) Permissions.allow(this, ASK_NEARBY, MainActivity.LOCAL_NETWORK);
        else Permissions.allow(this, ASK_NOTIFY, Manifest.permission.POST_NOTIFICATIONS);
      });
      LinearLayout texts = (LinearLayout) r.getChildAt(1);
      texts.addView(b, new LinearLayout.LayoutParams(LinearLayout.LayoutParams.WRAP_CONTENT,
          LinearLayout.LayoutParams.WRAP_CONTENT));
    }
    return r;
  }

  @Override
  public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] results) {
    showPermissions();
  }


  private void open(String url) {
    try {
      startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)));
    } catch (ActivityNotFoundException e) {
      say(getString(R.string.no_browser_title), getString(R.string.no_browser, url));
    }
  }

  private void email(String version) {
    Intent i = new Intent(Intent.ACTION_SENDTO, Uri.parse("mailto:" + CONTACT))
        .putExtra(Intent.EXTRA_SUBJECT, getString(R.string.about_title, version));
    try {
      startActivity(i);
    } catch (ActivityNotFoundException e) {
      say(getString(R.string.contact), getString(R.string.no_email, CONTACT));
    }
  }

  private void say(String title, String message) {
    TextView t = Ui.text(this, message, 15, 400);
    t.setTextIsSelectable(true);
    int pad = Ui.dp(this, 24);
    t.setPadding(pad, Ui.dp(this, 8), pad, 0);
    new AlertDialog.Builder(this).setTitle(title).setView(t).setPositiveButton(android.R.string.ok, null).show();
  }

  @Override
  public boolean onKeyUp(int keyCode, KeyEvent event) {
    // Escape closes the page, as Back does.
    if (keyCode == KeyEvent.KEYCODE_ESCAPE && event.hasNoModifiers()) {
      finish();
      return true;
    }
    return super.onKeyUp(keyCode, event);
  }
}
