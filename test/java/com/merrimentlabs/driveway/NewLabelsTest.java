// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * The New label's life, on a plain JVM (./build.sh test): absent on a fresh
 * install, shown after an update that made a service available, gone after
 * two visits.
 */
public final class NewLabelsTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  private static final class MapStore implements NewLabels.Store {
    final Map<String, String> m = new HashMap<>();

    @Override
    public String get(String key) {
      return m.get(key);
    }

    @Override
    public void put(String key, String value) {
      m.put(key, value);
    }
  }

  /** The catalog: services offered before New labels (no since), and pCloud from 0.2.5. */
  private static Map<String, String> catalog(boolean withPcloud) {
    Map<String, String> since = new LinkedHashMap<>();
    since.put("dropbox", "");
    since.put("koofr", null);
    if (withPcloud) since.put("pcloud", "0.2.5");
    return since;
  }

  /** A visit to Add a server that shows the service. */
  private static boolean visit(NewLabels.Store s, String service) {
    boolean labeled = NewLabels.isNew(s, service);
    if (labeled) NewLabels.visited(s, service);
    return labeled;
  }

  public static void main(String[] args) {
    // Versions.
    check(NewLabels.compare("0.2.10", "0.2.9") > 0, "0.2.10 is after 0.2.9");
    check(NewLabels.compare("0.2", "0.2.0") == 0, "0.2 equals 0.2.0");
    check(NewLabels.compare("0.2.4-debug", "0.2.4") == 0, "a suffix is ignored");
    check(NewLabels.compare("1.0", "0.9.9") > 0, "1.0 is after 0.9.9");

    // A fresh install of 0.2.5: nothing is new, pCloud included.
    MapStore fresh = new MapStore();
    NewLabels.update(fresh, "0.2.5", catalog(true), true);
    check(!NewLabels.isNew(fresh, "pcloud") && !NewLabels.isNew(fresh, "dropbox"), "fresh install: no New labels");
    // ...and stays so on later starts of the same version.
    NewLabels.update(fresh, "0.2.5", catalog(true), false);
    check(!NewLabels.isNew(fresh, "pcloud"), "fresh install, started again: no New labels");

    // 0.2.4 installed fresh, then the update to 0.2.5 that adds pCloud.
    MapStore up = new MapStore();
    NewLabels.update(up, "0.2.4", catalog(false), true);
    check(!NewLabels.isNew(up, "pcloud"), "0.2.4: pCloud not offered, not new");
    NewLabels.update(up, "0.2.5", catalog(true), false);
    check(NewLabels.isNew(up, "pcloud"), "after the update: pCloud is new");
    check(!NewLabels.isNew(up, "dropbox") && !NewLabels.isNew(up, "koofr"), "after the update: the others aren't");
    // Started again (same version): still new, not added twice.
    NewLabels.update(up, "0.2.5", catalog(true), false);
    check(visit(up, "pcloud"), "visit 1 shows New");
    NewLabels.update(up, "0.2.5", catalog(true), false);
    check(visit(up, "pcloud"), "visit 2 shows New");
    check(!visit(up, "pcloud"), "visit 3: New is gone");
    NewLabels.update(up, "0.2.6", catalog(true), false);
    check(!NewLabels.isNew(up, "pcloud"), "a later update doesn't bring it back");

    // An update from a version before New labels (no stored version, not a
    // fresh install) counts from 0.2.3.
    MapStore old = new MapStore();
    NewLabels.update(old, "0.2.5", catalog(true), false);
    check(NewLabels.isNew(old, "pcloud") && !NewLabels.isNew(old, "dropbox"), "update from 0.2.3: only pCloud is new");

    // Skipping a version: 0.2.4 straight to 0.2.6 still labels 0.2.5's pCloud.
    MapStore skip = new MapStore();
    NewLabels.update(skip, "0.2.4", catalog(false), true);
    NewLabels.update(skip, "0.2.6", catalog(true), false);
    check(NewLabels.isNew(skip, "pcloud"), "skipped a version: pCloud is new");

    // A service whose since is after the running version isn't new yet.
    MapStore early = new MapStore();
    NewLabels.update(early, "0.2.4", catalog(false), true);
    NewLabels.update(early, "0.2.4.1", catalog(true), false);
    check(!NewLabels.isNew(early, "pcloud"), "since after the running version: not new");

    // A downgrade labels nothing and moves the version back.
    MapStore down = new MapStore();
    NewLabels.update(down, "0.2.6", catalog(true), true);
    NewLabels.update(down, "0.2.5", catalog(true), false);
    check(!NewLabels.isNew(down, "pcloud") && "0.2.5".equals(down.get(NewLabels.KEY_VERSION)), "downgrade: nothing new");

    // The harness's mark, for screenshots.
    MapStore marked = new MapStore();
    NewLabels.update(marked, "0.2.4", catalog(false), true);
    NewLabels.markNew(marked, "koofr");
    check(NewLabels.isNew(marked, "koofr") && visit(marked, "koofr") && visit(marked, "koofr") && !visit(marked, "koofr"),
        "marked new: two visits, then gone");

    // Two services new at once: each counts its own visits.
    MapStore two = new MapStore();
    NewLabels.update(two, "0.2.4", catalog(false), true);
    NewLabels.markNew(two, "koofr");
    NewLabels.markNew(two, "dropbox");
    visit(two, "koofr");
    visit(two, "koofr");
    check(!NewLabels.isNew(two, "koofr") && NewLabels.isNew(two, "dropbox"), "two New labels: each counts its own visits");
    String kept = two.get(NewLabels.KEY_PENDING);
    NewLabels.visited(two, "pcloud");
    check(kept.equals(two.get(NewLabels.KEY_PENDING)), "a visit to a service without a label changes nothing");
    // An update that lists a service already marked new keeps its count.
    NewLabels.visited(two, "dropbox");
    Map<String, String> listsDropbox = new LinkedHashMap<>();
    listsDropbox.put("dropbox", "0.2.5");
    NewLabels.update(two, "0.2.5", listsDropbox, false);
    check(visit(two, "dropbox") && !visit(two, "dropbox"), "already new at the update: one visit left, not two");

    // Stored state that isn't what the app writes: garbled lines are skipped.
    MapStore garbled = new MapStore();
    garbled.put(NewLabels.KEY_PENDING, "nonsense\n=1\npcloud=x\nkoofr=1\nbox=2");
    check(NewLabels.isNew(garbled, "koofr") && NewLabels.isNew(garbled, "pcloud") && !NewLabels.isNew(garbled, "nonsense")
        && !NewLabels.isNew(garbled, ""), "garbled lines skipped; a count that isn't a number is 0");
    check(!NewLabels.isNew(garbled, "box"), "a count of two visits is no longer new");
    check(NewLabels.compare("0.x.5", "0.0.5") == 0, "a version part that isn't a number is 0");

    System.out.println(failures == 0 ? "NewLabelsTest: all passed" : "NewLabelsTest: " + failures + " failed");
    if (failures > 0) System.exit(1);
  }
}
