// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import com.sun.net.httpserver.HttpServer;

import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * The update check, on a plain JVM (./build.sh test): how versions compare,
 * that the weekly check is off until turned on and announces each version
 * once, and what the check makes of each answer GitHub can give, against a
 * local stand-in for GitHub's API.
 */
public final class UpdateCheckTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  private static final class MapStore implements UpdateCheck.Store {
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

  /** A stand-in for GitHub: answers every request with code and body, and records what was asked. */
  private static final class FakeGitHub implements AutoCloseable {
    final HttpServer server;
    final List<String> requests = new ArrayList<>();
    volatile int code = 200;
    volatile String body = "";

    FakeGitHub() throws Exception {
      server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
      server.createContext("/", ex -> {
        StringBuilder r = new StringBuilder(ex.getRequestMethod() + " " + ex.getRequestURI());
        ex.getRequestHeaders().forEach((k, v) -> r.append("\n").append(k.toLowerCase()).append(": ").append(String.join(",", v)));
        synchronized (requests) {
          requests.add(r.toString());
        }
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        ex.sendResponseHeaders(code, b.length == 0 ? -1 : b.length);
        if (b.length > 0) ex.getResponseBody().write(b);
        ex.close();
      });
      server.start();
    }

    String base() {
      return "http://127.0.0.1:" + server.getAddress().getPort();
    }

    UpdateCheck.Result answer(int code, String body, String installed) {
      this.code = code;
      this.body = body;
      return UpdateCheck.check(installed, base());
    }

    @Override
    public void close() {
      server.stop(0);
    }
  }

  private static String release(String tag) {
    return "{\"tag_name\":\"" + tag + "\",\"name\":\"Driveway " + tag + "\",\"html_url\":\"https://example.com/elsewhere\"}";
  }

  public static void main(String[] args) throws Exception {
    // Versions.
    check(Boolean.TRUE.equals(UpdateCheck.isNewer("0.2.14", "0.2.13")), "0.2.14 is newer than 0.2.13");
    check(Boolean.TRUE.equals(UpdateCheck.isNewer("v0.2.10", "0.2.9")), "a v tag: 0.2.10 is newer than 0.2.9, as numbers");
    check(Boolean.TRUE.equals(UpdateCheck.isNewer("1.0", "0.9.9")), "1.0 is newer than 0.9.9");
    check(Boolean.FALSE.equals(UpdateCheck.isNewer("0.2.13", "0.2.13")), "the same version isn't newer");
    check(Boolean.FALSE.equals(UpdateCheck.isNewer("1.0", "1.0.0")), "1.0 equals 1.0.0");
    check(Boolean.FALSE.equals(UpdateCheck.isNewer("0.2.12", "0.2.13")), "an older release isn't newer");
    check(Boolean.FALSE.equals(UpdateCheck.isNewer("0.2.13", "0.2.13-debug")), "a suffix is ignored");
    check(UpdateCheck.isNewer("latest", "0.2.13") == null, "a tag that isn't a version can't be compared");
    check(UpdateCheck.isNewer(null, "0.2.13") == null, "no tag can't be compared");
    check(UpdateCheck.isNewer("0.2.99999999999", "0.2.13") == null, "a part too large to read can't be compared");
    check("0.3.0".equals(UpdateCheck.display(" v0.3.0 ")), "shown without its v");

    // The weekly check's settings.
    MapStore s = new MapStore();
    check(!UpdateCheck.weekly(s), "the weekly check is off by default");
    UpdateCheck.setWeekly(s, true);
    check(UpdateCheck.weekly(s), "turned on, it is on");
    UpdateCheck.setWeekly(s, false);
    check(!UpdateCheck.weekly(s), "turned off again, it is off");
    s.put(UpdateCheck.KEY_WEEKLY, "garbled");
    check(!UpdateCheck.weekly(s), "a setting it can't read means off");
    UpdateCheck.Result newer = new UpdateCheck.Result(UpdateCheck.Outcome.AVAILABLE, "0.3.0");
    check(UpdateCheck.shouldAnnounce(s, newer), "a newer version is announced");
    UpdateCheck.announced(s, newer);
    check(!UpdateCheck.shouldAnnounce(s, newer), "the same version isn't announced twice");
    check(UpdateCheck.shouldAnnounce(s, new UpdateCheck.Result(UpdateCheck.Outcome.AVAILABLE, "0.3.1")),
        "the next version is announced");
    check(!UpdateCheck.shouldAnnounce(s, new UpdateCheck.Result(UpdateCheck.Outcome.UP_TO_DATE, null)),
        "nothing is announced when up to date");

    // The check, against a stand-in for GitHub.
    try (FakeGitHub gh = new FakeGitHub()) {
      UpdateCheck.Result r = gh.answer(200, release("v0.3.0"), "0.2.13");
      check(r.outcome == UpdateCheck.Outcome.AVAILABLE && "0.3.0".equals(r.version), "a newer release: available, 0.3.0");
      String asked = gh.requests.get(0);
      check(asked.startsWith("GET /repos/jessejamesjohnston/driveway/releases/latest\n"),
          "one GET of the public latest-release endpoint, with no query");
      check(asked.contains("\nuser-agent: Driveway\n") || asked.endsWith("\nuser-agent: Driveway"),
          "the User-Agent is the app's name only");
      check(!asked.contains("0.2.13") && !asked.contains("authorization") && !asked.contains("cookie"),
          "the request carries no version, account or cookie");
      check(gh.answer(200, release("v0.2.13"), "0.2.13").outcome == UpdateCheck.Outcome.UP_TO_DATE, "the same release: up to date");
      check(gh.answer(200, release("0.2.12"), "0.2.13").outcome == UpdateCheck.Outcome.UP_TO_DATE, "an older release: up to date");
      check(gh.answer(404, "{\"message\":\"Not Found\"}", "0.2.13").outcome == UpdateCheck.Outcome.NO_RELEASE,
          "404: no release published yet");
      check(gh.answer(403, "{\"message\":\"API rate limit exceeded\"}", "0.2.13").outcome == UpdateCheck.Outcome.FAILED,
          "a rate limit: failed");
      check(gh.answer(500, "", "0.2.13").outcome == UpdateCheck.Outcome.FAILED, "a server error: failed");
      check(gh.answer(200, "not json", "0.2.13").outcome == UpdateCheck.Outcome.FAILED, "an answer that isn't JSON: failed");
      check(gh.answer(200, "{}", "0.2.13").outcome == UpdateCheck.Outcome.FAILED, "a release with no tag: failed");
      check(gh.answer(200, release("nightly"), "0.2.13").outcome == UpdateCheck.Outcome.FAILED, "a tag that isn't a version: failed");
      StringBuilder big = new StringBuilder("{\"tag_name\":\"v9.0.0\",\"body\":\"");
      while (big.length() < (2 << 20)) big.append("xxxxxxxxxxxxxxxx");
      check(gh.answer(200, big.append("\"}").toString(), "0.2.13").outcome == UpdateCheck.Outcome.FAILED,
          "an answer too large to be a release: failed");
    }
    int closed;
    try (ServerSocket ss = new ServerSocket(0)) {
      closed = ss.getLocalPort();
    }
    check(UpdateCheck.check("0.2.13", "http://127.0.0.1:" + closed).outcome == UpdateCheck.Outcome.OFFLINE,
        "nothing listening: offline");
    check("https://github.com/jessejamesjohnston/driveway/releases/latest".equals(UpdateCheck.RELEASES_PAGE),
        "the page opened is the repository's own release page, never a link from the answer");

    if (failures > 0) {
      System.out.println("UpdateCheckTest: " + failures + " failed");
      System.exit(1);
    }
    System.out.println("UpdateCheckTest: all passed");
  }
}
