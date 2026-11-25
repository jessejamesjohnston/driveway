// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.regex.Pattern;

/**
 * The app's redacting log (Logs), on a plain JVM (./build.sh test): the
 * cases the engine is checked against too, a logged exception, and a scan
 * of the app's code for log lines that bypass it.
 */
public final class LogsTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  /** A log call that bypasses Logs is fine only with text the app wrote itself, whole on one line. */
  private static final Pattern LOG_CALL = Pattern.compile("\\bLog\\.(v|d|i|w|e|wtf)\\(");
  private static final Pattern CONSTANT_LOG =
      Pattern.compile("^\\s*(android\\.util\\.)?Log\\.[vdiwe]\\((TAG|\"[^\"]*\"), \"[^\"\\\\]*\"\\);\\s*$");

  static boolean bypasses(String line) {
    String t = line.trim();
    if (t.startsWith("//") || t.startsWith("*")) return false;
    return LOG_CALL.matcher(t).find() && !CONSTANT_LOG.matcher(line).matches();
  }

  public static void main(String[] args) throws Exception {
    JSONArray cases = new JSONObject(new String(Files.readAllBytes(new File("test/redact_cases.json").toPath()),
        StandardCharsets.UTF_8)).getJSONArray("cases");
    check(cases.length() >= 30, "the shared cases are read (" + cases.length() + ")");
    int same = 0;
    for (int i = 0; i < cases.length(); i++) {
      JSONObject c = cases.getJSONObject(i);
      String in = c.getString("in"), out = c.getString("out"), got = Logs.clean(in);
      if (got.equals(out) && Logs.clean(out).equals(out)) {
        same++;
      } else {
        check(false, c.getString("why") + ": got " + got + ", want " + out);
      }
    }
    check(same == cases.length(), "every shared case comes out as the engine's does (" + same + ")");
    check(Logs.clean(null) == null, "no text stays no text");

    // An exception's message and its cause's, as Logs.w writes them.
    Exception e = new RuntimeException("[network] Get \"https://www.premiumize.me/api/folder/list?apikey=SECRETAPIKEY123\": EOF",
        new IllegalStateException("token=SECRETTOKEN456"));
    Logs.w("Home server: listing failed", e);
    String logged = android.util.Log.lastMessage;
    check("Driveway".equals(android.util.Log.lastTag), "logged under the app's tag");
    check(logged.startsWith("Home server: listing failed\njava.lang.RuntimeException: [network] Get \"https://www.premiumize.me/api/folder/list?***\": EOF"),
        "the exception is logged with its host and kind: " + logged.split("\n")[1]);
    check(logged.contains("Caused by: java.lang.IllegalStateException: token=***"), "and its cause");
    check(!logged.contains("SECRETAPIKEY123") && !logged.contains("SECRETTOKEN456"), "with neither credential");
    Logs.i("ftp://bob:hunter2@192.0.2.10/pub");
    check("ftp://***@192.0.2.10/pub".equals(android.util.Log.lastMessage), "Logs.i redacts");
    Logs.e("can't list services", new IOException("password=x"));
    check(android.util.Log.lastMessage.contains("password=***"), "Logs.e redacts");

    // The scan.
    check(bypasses("      Log.w(TAG, \"upload failed: \" + last);"), "scan: dynamic text is caught");
    check(bypasses("    android.util.Log.w(\"Driveway\", \"can't make a key\", e);"), "scan: an exception is caught");
    check(!bypasses("      Log.i(TAG, \"no servers yet\");"), "scan: the app's own constant text is fine");
    check(!bypasses("    // Log.w(TAG, e.getMessage()) would leak"), "scan: a comment is fine");
    File[] files = new File("src/com/merrimentlabs/driveway").listFiles((d, n) -> n.endsWith(".java"));
    check(files != null && files.length > 20, "the app's code is read");
    int lines = 0;
    for (File f : files) {
      if (f.getName().equals("Logs.java")) continue;
      for (String line : Files.readAllLines(f.toPath(), StandardCharsets.UTF_8)) {
        if (bypasses(line)) check(false, "a log line that bypasses Logs: " + f.getName() + ": " + line.trim());
        if (line.contains("Logs.")) lines++;
      }
    }
    check(lines >= 30, "the app logs through Logs (" + lines + " lines)");

    if (failures > 0) {
      System.out.println("LogsTest: " + failures + " failed");
      System.exit(1);
    }
    System.out.println("LogsTest: all passed");
  }
}
