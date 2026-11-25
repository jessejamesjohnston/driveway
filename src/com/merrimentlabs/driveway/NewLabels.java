// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import java.util.LinkedHashMap;
import java.util.Map;

/**
 * The "New" label on a service an update made available.
 * When the app's version changes, the services first offered after the
 * version the person last ran (each service's "since", from the catalog in
 * engine/forms.go) become new. A new service is labeled in the lists until
 * the person has seen it there on two separate visits to Add a server. A
 * fresh install labels nothing: everything is new to them.
 *
 * Kept on the device only (the app's preferences, never backed up or sent).
 * No Android types here, so the logic is checked on a plain JVM
 * (test/java/NewLabelsTest.java, run by ./build.sh test).
 */
final class NewLabels {
  private NewLabels() {}

  /** Where the state lives: the app's preferences, or a map in the tests. */
  interface Store {
    String get(String key);

    void put(String key, String value);
  }

  /** Visits to Add a server that show a service's label before it goes. */
  static final int VISITS = 2;
  /** The last version that had no New labels: what an update from before them counts from. */
  static final String BEFORE_LABELS = "0.2.3";
  static final String KEY_VERSION = "new_version", KEY_PENDING = "new_pending";

  /**
   * Brings the new services up to date for the app's version. since: every
   * offered service with its version ("" or null for one offered before New
   * labels began). freshInstall: this is the first run after installing
   * (not an update), so nothing is new.
   */
  static void update(Store s, String current, Map<String, String> since, boolean freshInstall) {
    String last = s.get(KEY_VERSION);
    if (last == null) {
      if (freshInstall) {
        s.put(KEY_VERSION, current);
        return;
      }
      // An update from a version that didn't keep track yet.
      last = BEFORE_LABELS;
    }
    if (compare(current, last) == 0) return;
    if (compare(current, last) > 0) {
      Map<String, Integer> pending = pending(s);
      for (Map.Entry<String, String> e : since.entrySet()) {
        String v = e.getValue();
        if (v == null || v.isEmpty()) continue;
        if (compare(v, last) > 0 && compare(v, current) <= 0 && !pending.containsKey(e.getKey())) {
          pending.put(e.getKey(), 0);
        }
      }
      save(s, pending);
    }
    // A downgrade only moves the version back.
    s.put(KEY_VERSION, current);
  }

  /** Whether the service still carries its New label. */
  static boolean isNew(Store s, String service) {
    Integer seen = pending(s).get(service);
    return seen != null && seen < VISITS;
  }

  /** The person saw the service's label on a visit (once per visit). */
  static void visited(Store s, String service) {
    Map<String, Integer> pending = pending(s);
    Integer seen = pending.get(service);
    if (seen == null) return;
    if (seen + 1 >= VISITS) {
      pending.remove(service);
    } else {
      pending.put(service, seen + 1);
    }
    save(s, pending);
  }

  /** Marks a service new (the debug harness, for screenshots and checks). */
  static void markNew(Store s, String service) {
    Map<String, Integer> pending = pending(s);
    pending.put(service, 0);
    save(s, pending);
  }

  /** Dotted versions compared as numbers ("0.2.10" is after "0.2.9"); anything after a "-" is ignored. */
  static int compare(String a, String b) {
    String[] x = a.split("-", 2)[0].split("\\."), y = b.split("-", 2)[0].split("\\.");
    for (int i = 0; i < Math.max(x.length, y.length); i++) {
      int p = i < x.length ? number(x[i]) : 0, q = i < y.length ? number(y[i]) : 0;
      if (p != q) return p < q ? -1 : 1;
    }
    return 0;
  }

  private static int number(String s) {
    try {
      return Integer.parseInt(s.trim());
    } catch (NumberFormatException e) {
      return 0;
    }
  }

  // Kept as "name=visits" lines (service names have no '=' or newline).
  private static Map<String, Integer> pending(Store s) {
    Map<String, Integer> out = new LinkedHashMap<>();
    String raw = s.get(KEY_PENDING);
    if (raw == null || raw.isEmpty()) return out;
    for (String line : raw.split("\n")) {
      int eq = line.lastIndexOf('=');
      if (eq <= 0) continue;
      out.put(line.substring(0, eq), number(line.substring(eq + 1)));
    }
    return out;
  }

  private static void save(Store s, Map<String, Integer> pending) {
    StringBuilder b = new StringBuilder();
    for (Map.Entry<String, Integer> e : pending.entrySet()) {
      if (b.length() > 0) b.append('\n');
      b.append(e.getKey()).append('=').append(e.getValue());
    }
    s.put(KEY_PENDING, b.toString());
  }
}
