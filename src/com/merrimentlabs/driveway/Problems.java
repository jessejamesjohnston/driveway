// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.content.SharedPreferences;
import android.os.Handler;
import android.os.Looper;

import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * The last problem with each server that needs the person in Driveway, so its
 * card can say so: the service ended Driveway's sign-in, or an SFTP server
 * answered with a different identity (or one saved before its identity was
 * checked can't be used until it is). These are only learned when Files (or a
 * save) uses the server; Driveway never probes servers to find out. Recorded
 * from real use only (listings, reads, saves), never from a form's Test,
 * whose draft settings may not be the server's; cleared by any later success
 * and by saving the server (an SFTP server saved before its identity was
 * checked is noted as such right away), or the engine refused its saved
 * settings when Driveway started. Kept beside the server list (no
 * backup), holding no secrets: a server id and a word.
 */
final class Problems {
  private Problems() {}

  static final String SIGN_IN = "signin";
  static final String IDENTITY = "identity";
  /** An SFTP server saved without its identity checked (Save anyway while it was off). */
  static final String UNCONFIRMED = "unconfirmed";
  /**
   * Settings the engine refused (an address it can't use), noted as
   * "settings:" and the setting's key ("settings:url"), or "settings:" alone
   * when it didn't name one: Files gets the same refusal until the server is
   * saved again with Change.
   */
  static final String SETTINGS = "settings:";

  /** The setting a SETTINGS problem names ("" for none), or null for another problem. */
  static String settingsField(String problem) {
    return problem != null && problem.startsWith(SETTINGS) ? problem.substring(SETTINGS.length()) : null;
  }

  private static final List<Runnable> listeners = new CopyOnWriteArrayList<>();
  private static final Handler main = new Handler(Looper.getMainLooper());

  private static SharedPreferences prefs(Context c) {
    return c.getApplicationContext().getSharedPreferences("problems", Context.MODE_PRIVATE);
  }

  /** The server's problem (SIGN_IN or IDENTITY), or null. */
  static String of(Context c, String serverId) {
    return prefs(c).getString(serverId, null);
  }

  static void note(Context c, String serverId, String problem) {
    if (problem.equals(of(c, serverId))) return;
    prefs(c).edit().putString(serverId, problem).apply();
    changed();
  }

  static void clear(Context c, String serverId) {
    if (of(c, serverId) == null) return;
    prefs(c).edit().remove(serverId).apply();
    changed();
  }

  /**
   * What an error from real use says about the server: remembered if it is
   * one of the two problems a person fixes in Driveway.
   */
  static void observe(Context c, Servers.Server s, Throwable e) {
    if (s == null || !Errors.kind(e).equals("auth")) return;
    String d = Errors.detail(e);
    if (d.contains("knownhosts: key mismatch") || d.contains("host key mismatch")) {
      note(c, s.id, IDENTITY);
      return;
    }
    if (d.contains("knownhosts: key is unknown")) {
      note(c, s.id, UNCONFIRMED);
      return;
    }
    if (d.contains("knownhosts") || d.contains("non-privileged ports")) return;
    Kinds.Kind k = Kinds.of(s.backend);
    if (Kinds.browser(k, s.params)) note(c, s.id, SIGN_IN);
  }

  /** Runs on the main thread whenever a problem is noted or cleared. */
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
