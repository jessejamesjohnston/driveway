// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.provider.Settings;

import java.util.ArrayList;
import java.util.List;

/**
 * Asking for a permission the way Android allows it. Android shows its own
 * dialog until a permission has been refused twice; after that, asking shows
 * nothing and only Driveway's App info page can allow it. Android doesn't say
 * beforehand which it will be (shouldShowRequestPermissionRationale is false
 * both before the first ask and after the last refusal), so Driveway notes
 * each permission it has asked for (backups are off, so the note never
 * reaches another device).
 */
final class Permissions {
  private Permissions() {}

  private static SharedPreferences asked(Context c) {
    return c.getSharedPreferences("permissions_asked", Context.MODE_PRIVATE);
  }

  static boolean granted(Context c, String p) {
    if (MainActivity.LOCAL_NETWORK.equals(p)) return MainActivity.hasLocalNetwork(c);
    return c.checkSelfPermission(p) == PackageManager.PERMISSION_GRANTED;
  }

  /** Android's dialog for perms (shows nothing for one refused for good); use instead of requestPermissions. */
  static void request(Activity a, String[] perms, int requestCode) {
    SharedPreferences.Editor e = asked(a).edit();
    for (String p : perms) e.putBoolean(p, true);
    e.apply();
    a.requestPermissions(perms, requestCode);
  }

  /** Whether Android still shows its dialog for p: never asked, or refused only once. */
  static boolean canAsk(Activity a, String p) {
    return !asked(a).getBoolean(p, false) || a.shouldShowRequestPermissionRationale(p);
  }

  /**
   * An Allow button: Android's dialog for each of perms it still asks about
   * (the answer comes to onRequestPermissionsResult with requestCode), or,
   * when every missing one was refused for good, Driveway's App info page.
   */
  static void allow(Activity a, int requestCode, String... perms) {
    List<String> missing = new ArrayList<>(), ask = new ArrayList<>();
    for (String p : perms) {
      if (granted(a, p)) continue;
      missing.add(p);
      if (canAsk(a, p)) ask.add(p);
    }
    if (missing.isEmpty()) return;
    if (!ask.isEmpty()) {
      request(a, ask.toArray(new String[0]), requestCode);
    } else {
      appInfo(a);
    }
  }

  static void appInfo(Activity a) {
    a.startActivity(new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", a.getPackageName(), null)));
  }
}
