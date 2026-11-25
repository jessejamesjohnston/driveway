// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.content.Context;
import android.graphics.Rect;
import android.view.View;
import android.widget.LinearLayout;

import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

/**
 * The "New" chip on Add a server's service rows, and when a row's label
 * counts as seen. What is new, and for how long, is NewLabels (plain Java,
 * checked on the JVM); this class puts the chip on screen, counts a visit
 * once a labeled row is at least half visible, and keeps the state in the
 * app's preferences.
 */
final class NewLabelRows {
  private final Activity a;

  NewLabelRows(Activity a) {
    this.a = a;
  }

  /**
   * Services whose New label was seen on this visit to Add a server. Each
   * counts once per visit (a turn of the screen or a search doesn't count
   * again), and keeps its label for the rest of the visit.
   */
  final Set<String> shown = new HashSet<>();
  /** Rows with a New label not yet seen on this visit: counted once on screen. */
  private final Map<String, View> unseen = new LinkedHashMap<>();
  private final Rect seenRect = new Rect();

  /** Whether the service's row carries the New label now. */
  boolean labeledNew(Kinds.Kind k) {
    if (!Kinds.offered(k) || Kinds.main(k.backend) != null) return false;
    return shown.contains(k.backend) || NewLabels.isNew(store(a), k.backend);
  }

  /**
   * Counts a visit for each New row now at least half on screen (a row
   * further down a long list counts only once it is scrolled to).
   */
  void countSeen() {
    if (unseen.isEmpty()) return;
    for (java.util.Iterator<Map.Entry<String, View>> it = unseen.entrySet().iterator(); it.hasNext(); ) {
      Map.Entry<String, View> e = it.next();
      View v = e.getValue();
      if (!v.isAttachedToWindow()) {
        it.remove();
        continue;
      }
      if (v.getHeight() == 0 || !v.isShown() || !v.getGlobalVisibleRect(seenRect)) continue;
      if (seenRect.height() * 2 < v.getHeight()) continue;
      it.remove();
      if (shown.add(e.getKey())) NewLabels.visited(store(a), e.getKey());
    }
  }

  /** A service row with the New chip after its name, read out as "<name>, new". */
  View labeled(LinearLayout row, boolean lead, Kinds.Kind k, String subtitle) {
    if (!labeledNew(k)) return row;
    LinearLayout texts = (LinearLayout) row.getChildAt(lead ? 1 : 0);
    View title = texts.getChildAt(0);
    texts.removeViewAt(0);
    Layouts.Flow line = new Layouts.Flow(a);
    line.addView(title);
    line.addView(Ui.chip(a, a.getString(R.string.new_label)));
    texts.addView(line, 0);
    String named = a.getString(R.string.new_named, k.label);
    row.setContentDescription(subtitle == null || subtitle.isEmpty() ? named : named + ", " + subtitle);
    if (!shown.contains(k.backend)) {
      unseen.put(k.backend, row);
      row.post(this::countSeen);
    }
    return row;
  }

  private static boolean newChecked;

  /**
   * The New labels' state, in the app's own preferences (never backed up:
   * allowBackup is off). Brought up to date for this version once per run.
   */
  static NewLabels.Store store(Context c) {
    android.content.SharedPreferences p = c.getApplicationContext().getSharedPreferences("new_labels", Context.MODE_PRIVATE);
    NewLabels.Store s = new NewLabels.Store() {
      @Override
      public String get(String key) {
        return p.getString(key, null);
      }

      @Override
      public void put(String key, String value) {
        p.edit().putString(key, value).apply();
      }
    };
    synchronized (NewLabelRows.class) {
      if (!newChecked) {
        newChecked = true;
        try {
          android.content.pm.PackageInfo pi = c.getPackageManager().getPackageInfo(c.getPackageName(), 0);
          Map<String, String> since = new java.util.HashMap<>();
          for (Kinds.Kind k : Kinds.others()) if (Kinds.offered(k)) since.put(k.backend, k.since);
          // Installed and never updated: everything is new to them, so nothing is labeled.
          NewLabels.update(s, pi.versionName, since, pi.firstInstallTime == pi.lastUpdateTime);
        } catch (android.content.pm.PackageManager.NameNotFoundException ignored) {
        }
      }
    }
    return s;
  }
}
