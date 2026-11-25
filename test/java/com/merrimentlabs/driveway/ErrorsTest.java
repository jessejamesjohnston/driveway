// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import java.io.File;
import java.lang.reflect.Field;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Reading the engine's errors, on a plain JVM (./build.sh test): the category
 * in brackets and the detail after it; then the sentence a person reads for
 * each kind of error (message(), filled in from the real strings.xml), which
 * never carries the engine's or a library's own words; and a scan of the
 * app's code for an exception's message put on screen.
 */
public final class ErrorsTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  private static Exception err(String message) {
    return new Exception(message);
  }

  public static void main(String[] args) {
    // kind()
    check(Errors.kind(err("[network] dial tcp 10.0.0.1:22: i/o timeout")).equals("network"), "kind: the category");
    check(Errors.kind(err("[notfound]")).equals("notfound"), "kind: a category alone");
    check(Errors.kind(err("dial tcp: refused")).equals("other"), "kind: none is other");
    check(Errors.kind(err("bad [auth] here")).equals("other"), "kind: only at the start");
    check(Errors.kind(err("[auth no end")).equals("other"), "kind: an unclosed bracket is other");
    check(Errors.kind(err(null)).equals("other") && Errors.kind(new RuntimeException()).equals("other"),
        "kind: no message is other");
    check(Errors.kind(err("[auth] one ] two")).equals("auth"), "kind: up to the first closing bracket");

    // detail()
    check(Errors.detail(err("[auth]   wrong password  ")).equals("wrong password"), "detail: after the category, trimmed");
    check(Errors.detail(err("[exists]")).isEmpty(), "detail: a category alone leaves nothing");
    check(Errors.detail(err(" plain message ")).equals(" plain message "), "detail: no category, the message as it is");
    check(Errors.detail(err("[unclosed message")).equals("[unclosed message"), "detail: an unclosed bracket stays");
    check(Errors.detail(err("bad [auth] here")).equals("bad [auth] here"), "detail: brackets later on stay");
    check(Errors.detail(new IllegalStateException()).equals("java.lang.IllegalStateException"),
        "detail: no message, the error's name");

    // isNotFound()
    check(Errors.isNotFound(err("[notfound] photos/a.jpg")), "isNotFound: notfound");
    check(!Errors.isNotFound(err("[network] notfound")) && !Errors.isNotFound(err("notfound")) && !Errors.isNotFound(err(null)),
        "isNotFound: anything else");

    // closed()
    check(Errors.closed("It's no longer on the server").equals("It's no longer on the server."), "closed: adds the period");
    check(Errors.closed("Try again.").equals("Try again.") && Errors.closed("(Dropbox said: no)").equals("(Dropbox said: no)")
        && Errors.closed("").isEmpty(), "closed: leaves an ended sentence alone");

    // credential(): only the file check's own tagged sentence is shown as it is.
    check("That file isn't a service account key".equals(Errors.credential(err("[credential] That file isn't a service account key"))),
        "credential: the file check's sentence");
    android.util.Log.lastMessage = null;
    check(Errors.credential(err("[other] " + RAW)) == null && android.util.Log.lastMessage != null
        && android.util.Log.lastMessage.startsWith("can't check the chosen file\n") && android.util.Log.lastMessage.contains("[other] "),
        "credential: another category goes to the log");
    check(Errors.credential(err(RAW)) == null && Errors.credential(err("[credential]")) == null
        && Errors.credential(err(null)) == null && Errors.credential(new org.json.JSONException("[credential] x")) == null,
        "credential: untagged, empty, no message or unreadable JSON is not shown");

    try {
      userTextNeverCarriesRawDetail();
      noScreenTextFromAnExceptionsMessage();
    } catch (Exception e) {
      e.printStackTrace();
      check(false, "the user-text checks ran");
    }

    System.out.println(failures == 0 ? "ErrorsTest: all passed" : "ErrorsTest: " + failures + " failed");
    if (failures > 0) System.exit(1);
  }

  /** A detail no sentence of Driveway's could hold: if it shows, the engine's words reached the screen. */
  private static final String RAW = "ssh: handshake failed: zq-raw-detail-7731";

  private static Map<Integer, String> templates() throws Exception {
    Map<String, String> byName = new HashMap<>();
    for (StringStyleTest.Entry e : StringStyleTest.entries(new File(System.getProperty("driveway.strings", "res/values/strings.xml")))) {
      byName.put(e.name, e.text);
    }
    Map<Integer, String> out = new HashMap<>();
    for (Field f : R.string.class.getFields()) out.put(f.getInt(null), byName.get(f.getName()));
    return out;
  }

  private static Servers.Server server(String backend, String params) throws Exception {
    Servers.Server s = new Servers.Server();
    s.label = "Family photos";
    s.backend = backend;
    s.params = new org.json.JSONObject(params);
    return s;
  }

  private static void userTextNeverCarriesRawDetail() throws Exception {
    Map<Integer, String> text = templates();
    Map<String, Servers.Server> servers = new LinkedHashMap<>();
    servers.put("sftp", server("sftp", "{\"host\":\"nas.local\"}"));
    servers.put("dropbox", server("dropbox", "{\"token\":\"{}\"}"));
    servers.put("none", null);
    List<String> kinds = List.of("network", "auth", "declined", "expired", "background", "nobrowser", "port", "signin",
        "signinfailed", "setup", "setupended", "badsettings", "notfound", "exists", "notempty", "bucketname", "denied", "full", "retry", "nosize",
        "changed", "norename", "bothnames", "partway", "keptold", "short", "other", "unsupported", "credential",
        "a-kind-from-later");
    List<Throwable> errors = new java.util.ArrayList<>();
    for (String k : kinds) errors.add(err("[" + k + "] " + RAW));
    errors.add(err("[auth] knownhosts: key mismatch " + RAW));
    errors.add(err("[auth] knownhosts: key is unknown " + RAW));
    errors.add(err("[auth] mount refused, non-privileged ports " + RAW));
    errors.add(err("[network] dial tcp: lookup nohost.invalid: no such host " + RAW));
    errors.add(err(RAW));
    errors.add(new IllegalStateException(new org.json.JSONException(RAW)));
    errors.add(new IllegalStateException(RAW));
    int shown = 0;
    for (Throwable e : errors) {
      for (Map.Entry<String, Servers.Server> s : servers.entrySet()) {
        for (boolean onForm : new boolean[] {false, true}) {
          for (boolean local : new boolean[] {true, false}) {
            android.util.Log.lastMessage = null;
            Errors.Message m = Errors.message(s.getValue(), e, onForm, () -> local, "The server");
            String template = text.get(m.res);
            String what = s.getKey() + (onForm ? " on the form" : "") + ": " + e.getMessage();
            if (template == null) {
              check(false, "a sentence from strings.xml for " + what);
              continue;
            }
            String said = String.format(template, m.args);
            boolean quotesService = Errors.kind(e).equals("signin");
            if (quotesService != said.contains(RAW)) {
              check(false, "the raw detail " + (quotesService ? "is quoted as the service's" : "stays off screen") + ": " + what + " -> " + said);
            }
            if (!String.valueOf(android.util.Log.lastMessage).contains("zq-raw-detail-7731")) {
              check(false, "the raw detail goes to the log: " + what);
            }
            if (said.contains("%")) check(false, "every placeholder filled: " + said);
            shown++;
          }
        }
      }
    }
    check(shown == errors.size() * 3 * 2 * 2, "user-facing error text never carries the engine's raw detail (" + shown + " cases)");
    // Can't reach: the next step names the port (a server that is on, answering
    // on another) and, for a name that doesn't look up, its spelling.
    Servers.Server sftp = servers.get("sftp");
    for (boolean onForm : new boolean[] {false, true}) {
      String where = onForm ? " on the form" : "";
      Errors.Message m = Errors.message(sftp, err("[network] ssh: handshake failed: EOF"), onForm, () -> true, "The server");
      check(m.res == (onForm ? R.string.err_network_form : R.string.err_network) && text.get(m.res).contains("port"),
          "a server that answers wrongly or not at all" + where + ": the next step names its port");
      m = Errors.message(sftp, err("[network] dial tcp: lookup nohost.invalid: no such host"), onForm, () -> true, "The server");
      check(m.res == (onForm ? R.string.err_no_host_form : R.string.err_no_host) && text.get(m.res).contains("spelling"),
          "a name that doesn't look up" + where + ": the next step is its spelling");
      m = Errors.message(sftp, err("[network] dial tcp: lookup nohost.invalid: no such host"), onForm, () -> false, "The server");
      check(m.res == R.string.err_local_network, "without Nearby devices" + where + ": that comes first");
      // A cloud service has no address of its own to check: the internet
      // connection is the next step, whatever the network error, and the
      // local network permission doesn't matter.
      for (String error : new String[] {"[network] ssh: handshake failed: EOF", "[network] dial tcp: lookup api.dropboxapi.com: no such host"}) {
        for (boolean local : new boolean[] {true, false}) {
          m = Errors.message(servers.get("dropbox"), err(error), onForm, () -> local, "The server");
          check(m.res == R.string.err_network_service && text.get(m.res).contains("internet") && !text.get(m.res).contains("address"),
              "Dropbox" + where + (local ? "" : " without Nearby devices") + ": check the internet connection, not an address (" + error + ")");
        }
      }
      m = Errors.message(server("s3", "{\"provider\":\"AWS\"}"), err("[network] dial tcp: i/o timeout"), onForm, () -> true, "The server");
      check(m.res == R.string.err_network_service, "S3 at Amazon" + where + ": the internet connection");
      m = Errors.message(server("s3", "{\"provider\":\"Minio\",\"endpoint\":\"http://nas.local:9000\"}"), err("[network] dial tcp: i/o timeout"),
          onForm, () -> true, "The server");
      check(m.res == (onForm ? R.string.err_network_form : R.string.err_network), "S3 at a typed endpoint" + where + ": its address and port");
      m = Errors.message(server("webdav", "{\"url\":\"https://dav.example.com\"}"), err("[network] dial tcp: i/o timeout"), onForm, () -> true, "The server");
      check(m.res == (onForm ? R.string.err_network_form : R.string.err_network), "WebDAV" + where + ": its address and port");
      // The two setup failures that used to say only "couldn't finish connecting":
      // each says what stopped and what to do.
      m = Errors.message(sftp, err("[setupended] No Shared Drives found in your account"), onForm, () -> true, "The server");
      check(m.res == R.string.err_setup_ended, "a service's setup that stopped on an error" + where + ": says so");
      m = Errors.message(sftp, err("[badsettings] bad settings: unexpected end of JSON input"), onForm, () -> true, "The server");
      check(m.res == (onForm ? R.string.err_bad_settings_form : R.string.err_bad_settings),
          "settings the engine couldn't read" + where + ": says so");
      // An address the engine refused (SetRemote): the form's own sentence,
      // naming the field; elsewhere (a card, Files), which setting to
      // correct and where. Never "no longer on the server".
      Servers.Server dav = server("webdav", "{\"url\":\"https://dav.example.com\"}");
      m = Errors.message(dav, err("[badsettings] bad settings: the address in url can't be used (unreadable)"), onForm, () -> true, "The server");
      check(onForm ? m.res == R.string.address_unreadable && "Address".equals(m.args[0])
          : m.res == R.string.err_bad_address && "Address".equals(m.args[1]) && text.get(m.res).contains("Change"),
          "an address the engine refused" + where + ": names Address" + (onForm ? "" : " and the next step"));
      m = Errors.message(dav, err("[badsettings] bad settings: the address in url can't be used (ambiguous)"), true, () -> true, "The server");
      check(m.res == R.string.address_ambiguous_login && "User name".equals(m.args[1]) && "Password".equals(m.args[2]),
          "an address the engine refused, on the form: the same sentence the form shows" + where);
    }
    check(String.format(text.get(Errors.message(null, err("[signin] " + RAW), false, () -> true, "The server").res),
        Errors.message(null, err("[signin] " + RAW), false, () -> true, "The server").args).contains("said: " + RAW),
        "a service's own words are labeled as the service's");
  }

  /**
   * Exceptions' messages in the app's code, line by line: every line that
   * reads one (getMessage(), or the engine's detail) either only logs it or
   * is listed below with why its text is Driveway's own or never shown. So a
   * message kept in a variable and shown a few lines later (String m =
   * e.getMessage(); ... error = m;) is caught where it is read. Errors.java
   * is checked above, case by case.
   */
  private static final java.util.regex.Pattern RAW_SOURCE = java.util.regex.Pattern.compile("getMessage\\(\\)|\\bdetail\\(");
  private static final java.util.regex.Pattern LOGS_ONLY = java.util.regex.Pattern.compile("^((android\\.util\\.)?Log|Logs)\\.[ewid]\\(");
  /**
   * Lines allowed to read a message, each with how many times it appears and
   * why its text is Driveway's own or never shown.
   */
  private static final Map<String, String> ALLOWED_LINES = Map.of(
      "ServerActivity.java: formStatus.setText(e.getMessage());",
      "2: e is collect()'s IllegalArgumentException, whose message is always one of Driveway's strings (needs, "
          + "needs_login, form_failed, the address problems)",
      "ServerActivity.java: markProblem(field, e.getMessage());",
      "1: the same collect() exception, as the state of the field it names",
      "ServerActivity.java: error = e.getMessage();",
      "1: e is an Errors.Worded (the catch above it), Driveway's own sentence",
      "DrivewayProvider.java: throw new UnsupportedOperationException(Errors.detail(e));",
      "2: Files takes UnsupportedOperationException from a move or copy as a sign to do it itself, and never shows "
          + "its message",
      "Problems.java: String d = Errors.detail(e);",
      "1: only compared with known words to choose which problem to remember, never shown");

  /** The line as an ALLOWED_LINES key when it reads an exception's message for more than the log, else null. */
  static String readsMessage(String file, String line) {
    String t = line.trim();
    if (t.startsWith("//") || t.startsWith("*") || !RAW_SOURCE.matcher(t).find() || LOGS_ONLY.matcher(t).find()) return null;
    return file + ": " + t;
  }

  private static void noScreenTextFromAnExceptionsMessage() throws Exception {
    // The scan itself: the two-line hop is caught where the message is read.
    check(readsMessage("A.java", "        String m = e.getMessage();") != null, "scan: a message kept in a variable is caught");
    check(readsMessage("A.java", "  Log.w(TAG, \"x: \" + e.getMessage());") == null
        && readsMessage("A.java", "android.util.Log.w(\"Driveway\", e.getMessage());") == null, "scan: logging a message is fine");
    check(readsMessage("A.java", "    // e.getMessage() is never shown") == null, "scan: a comment is fine");
    Map<String, Integer> used = new HashMap<>();
    File[] files = new File("src/com/merrimentlabs/driveway").listFiles((d, n) -> n.endsWith(".java"));
    check(files != null && files.length > 20, "the app's code is read");
    for (File f : files) {
      if (f.getName().equals("Errors.java")) continue;
      for (String line : Files.readAllLines(f.toPath(), StandardCharsets.UTF_8)) {
        String key = readsMessage(f.getName(), line);
        if (key == null) continue;
        if (ALLOWED_LINES.containsKey(key)) used.merge(key, 1, Integer::sum);
        else check(false, "an exception's message read for more than the log: " + key);
      }
    }
    for (Map.Entry<String, String> a : ALLOWED_LINES.entrySet()) {
      String[] countWhy = a.getValue().split(": ", 2);
      check(used.getOrDefault(a.getKey(), 0) == Integer.parseInt(countWhy[0]),
          "allowed line appears exactly " + countWhy[0] + " times: " + a.getKey() + " (" + used.get(a.getKey()) + ")");
      check(!countWhy[1].isBlank(), "allowed line says why: " + a.getKey());
    }
  }
}
