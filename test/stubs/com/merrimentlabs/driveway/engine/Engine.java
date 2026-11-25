// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway.engine;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;

/**
 * The JVM checks' stand-in for gomobile's Engine class: backends() is the
 * real engine's catalog, written by engine/backends_json_test.go to the file
 * named by -Ddriveway.backends, and checkSettings() answers with the real
 * engine's verdicts on test/address_cases.json, written by
 * engine/address_test.go to the file named by -Ddriveway.verdicts
 * (./build.sh does both). A question it has no verdict for fails loudly:
 * it never makes one up.
 */
public abstract class Engine {
  /** What backends() returns: the engine's catalog, unless a check sets its own. */
  public static String json;

  public static String backends() {
    if (json == null) {
      String path = System.getProperty("driveway.backends");
      if (path == null) throw new IllegalStateException("no catalog: run with -Ddriveway.backends=FILE");
      try {
        json = new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8);
      } catch (IOException e) {
        // Loudly: Kinds would otherwise list no services and look like a different failure.
        throw new UncheckedIOException("no catalog at " + path, e);
      }
    }
    return json;
  }

  /** The real engine's verdicts: backend, settings, verdict. */
  private static JSONArray verdicts;

  public static String checkSettings(String backend, String params) throws JSONException {
    if (verdicts == null) {
      String path = System.getProperty("driveway.verdicts");
      if (path == null) throw new IllegalStateException("no verdicts: run with -Ddriveway.verdicts=FILE");
      try {
        verdicts = new JSONArray(new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8));
      } catch (IOException e) {
        throw new UncheckedIOException("no verdicts at " + path, e);
      }
    }
    JSONObject asked = new JSONObject(params);
    for (int i = 0; i < verdicts.length(); i++) {
      JSONObject v = verdicts.getJSONObject(i);
      if (v.getString("backend").equals(backend) && same(v.getJSONObject("settings"), asked)) {
        return v.getJSONObject("verdict").toString();
      }
    }
    throw new IllegalStateException("no engine verdict for " + backend + " " + asked.names()
        + ": add the case to test/address_cases.json");
  }

  private static boolean same(JSONObject a, JSONObject b) throws JSONException {
    if (a.length() != b.length()) return false;
    for (java.util.Iterator<String> it = a.keys(); it.hasNext(); ) {
      String k = it.next();
      if (!b.has(k) || !a.getString(k).equals(b.getString(k))) return false;
    }
    return true;
  }
}
