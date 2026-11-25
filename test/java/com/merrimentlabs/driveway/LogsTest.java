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

  /**
   * Every way to write to Android's log or the console: android.util.Log's
   * calls (spaced or not), its stack trace text, a statically imported Log
   * method, printStackTrace(), System.out and System.err.
   */
  private static final Pattern LOG_CALL = Pattern.compile("\\bLog\\s*\\.\\s*(v|d|i|w|e|wtf|println|getStackTraceString)\\s*\\("
      + "|\\.\\s*printStackTrace\\s*\\(\\s*\\)|\\bSystem\\s*\\.\\s*(out|err)\\b"
      + "|(?<![\\w.])(v|d|i|w|e|wtf|println)\\s*\\(\\s*TAG\\b|^\\s*import\\s+static\\s+android\\.util\\.Log\\b");
  /** Fine: a Log call with the app's own constant text whole on one line, or with text through Logs.clean. */
  private static final Pattern ALLOWED_LOG = Pattern.compile(
      "^\\s*(android\\.util\\.)?Log\\.[vdiwe]\\((TAG|\"[^\"]*\"), (\"[^\"\\\\]*\"\\);\\s*$|Logs\\.clean\\()");

  /** A line of code (comments already taken out, see code()) that logs around Logs. */
  static boolean bypasses(String code) {
    return LOG_CALL.matcher(code).find() && !ALLOWED_LOG.matcher(code).find();
  }

  /**
   * The lines with their comments blanked (// to the end, and /* ... *\/
   * across lines), and the text inside quotes blanked too, so neither a
   * comment nor a string that mentions a log call counts as one.
   */
  static java.util.List<String> code(java.util.List<String> lines) {
    java.util.List<String> out = new java.util.ArrayList<>();
    boolean block = false;
    for (String line : lines) {
      StringBuilder b = new StringBuilder();
      char quote = 0;
      for (int i = 0; i < line.length(); i++) {
        char c = line.charAt(i), next = i + 1 < line.length() ? line.charAt(i + 1) : 0;
        if (block) {
          if (c == '*' && next == '/') {
            block = false;
            i++;
          }
          b.append(' ');
        } else if (quote != 0) {
          if (c == '\\') {
            b.append("  ");
            i++;
          } else if (c == quote) {
            b.append(c);
            quote = 0;
          } else {
            b.append(' ');
          }
        } else if (c == '/' && next == '/') {
          break;
        } else if (c == '/' && next == '*') {
          block = true;
          i++;
          b.append(' ');
        } else {
          if (c == '"' || c == '\'') quote = c;
          b.append(c);
        }
      }
      out.add(b.toString());
    }
    return out;
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

    // Time: a server's answer can put a megabyte of text in an error
    // (rclone quotes a failed request's body), and redaction runs on the
    // thread that reports it. Every shape must take time in proportion to
    // its length; the bound is generous (a quadratic pattern takes minutes).
    String[] shapes = {"a.", "a-", "x-x-", "a:", "a@", "a=", "a ", "a/", "%2", "\\\"", "https://", "h:\\/\\/a\\/", "key", "x-a"};
    long worst = 0;
    String slowest = "";
    for (String shape : shapes) {
      String big = shape.repeat(1_000_000 / shape.length());
      long t0 = System.nanoTime();
      Logs.clean(big);
      long ms = (System.nanoTime() - t0) / 1_000_000;
      if (ms > worst) {
        worst = ms;
        slowest = shape;
      }
    }
    // One long value inside each rule's own frame, which a pattern that
    // recursed per character would overflow the stack on.
    String run = "a\\".repeat(250_000) + "b".repeat(500_000);
    String[][] frames = {{"\"token\":\"", "\""}, {"invalid port \"", "\" after host"}, {"https:\\/\\/", ""},
        {"token=\"", "\""}, {"Cookie: ", ""}, {"https://alice:p s", "@host/"}, {"<password>", "</password>"}};
    for (String[] f : frames) {
      String big = f[0] + run + f[1];
      long t0 = System.nanoTime();
      String out;
      try {
        out = Logs.clean(big);
      } catch (StackOverflowError overflow) {
        out = null;
      }
      long ms = (System.nanoTime() - t0) / 1_000_000;
      check(out != null, "a megabyte inside " + f[0] + " doesn't overflow the stack");
      if (ms > worst) {
        worst = ms;
        slowest = f[0];
      }
    }
    check(worst < 3000, "a megabyte of any shape is redacted in under 3 s (slowest " + slowest + ": " + worst + " ms)");
    // What reaches the log is cut to Logs.MAX characters, after redaction.
    StringBuilder longLine = new StringBuilder("listing failed: ");
    while (longLine.length() < 20_000) longLine.append("word ");
    Logs.w(longLine + "token=SECRETTAIL");
    String cut = android.util.Log.lastMessage;
    check(cut.length() <= Logs.MAX + 40 && cut.startsWith("listing failed: word") && cut.contains("[cut:")
        && !cut.contains("SECRETTAIL"), "a long line is cut, and says so (" + cut.length() + " characters)");
    Logs.w("short");
    check(android.util.Log.lastMessage.equals("short"), "a short line is whole");

    // The scan.
    String[][] caught = {
        {"      Log.w(TAG, \"upload failed: \" + last);", "dynamic text"},
        {"    android.util.Log.w(\"Driveway\", \"can't make a key\", e);", "an exception"},
        {"      e.printStackTrace();", "printStackTrace()"},
        {"      System.err.println(\"failed: \" + e);", "System.err"},
        {"      System.out.print(s);", "System.out"},
        {"      Log.println(Log.WARN, TAG, msg);", "Log.println"},
        {"import static android.util.Log.w;", "a static import of Log"},
        {"      w(TAG, msg);", "a statically imported call"},
        {"      Log .w(TAG, msg);", "a spaced call"},
        {"      Log.\tw (TAG, msg);", "a call spaced with a tab"},
        {"      String s = Log.getStackTraceString(e);", "Log.getStackTraceString"},
        {"      *Log.w(TAG, msg);", "a code line that starts with *"},
        {"      x = 1; /* note */ Log.w(TAG, msg);", "code after a closed comment"},
    };
    for (String[] c : caught) {
      check(bypasses(code(java.util.Collections.singletonList(c[0])).get(0)), "scan: " + c[1] + " is caught");
    }
    check(!bypasses("      Log.i(TAG, \"no servers yet\");"), "scan: the app's own constant text is fine");
    check(!bypasses("      Log.i(TAG, Logs.clean(\"HARNESS PASS \" + id));"), "scan: text through Logs.clean is fine");
    check(!bypasses(code(java.util.Collections.singletonList("    // Log.w(TAG, e.getMessage()) would leak")).get(0)),
        "scan: a comment is fine");
    check(!bypasses(code(java.util.Arrays.asList("  /*", "   * Log.w(TAG, e.getMessage()) would leak", "   */")).get(1)),
        "scan: a block comment's line is fine");
    check(!bypasses(code(java.util.Collections.singletonList("    String s = \"// Log.w(TAG, x) in a string\";")).get(0)),
        "scan: a string that mentions it is fine");
    // The app and the debug-only test harness.
    java.util.List<File> files = new java.util.ArrayList<>();
    for (String dir : new String[] {"src/com/merrimentlabs/driveway", "src-debug/com/merrimentlabs/driveway"}) {
      File[] in = new File(dir).listFiles((d, n) -> n.endsWith(".java"));
      if (in != null) files.addAll(java.util.Arrays.asList(in));
    }
    check(files.size() > 23, "the app's and the harness's code is read (" + files.size() + " files)");
    int lines = 0;
    for (File f : files) {
      if (f.getName().equals("Logs.java")) continue;
      for (String line : code(Files.readAllLines(f.toPath(), StandardCharsets.UTF_8))) {
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
