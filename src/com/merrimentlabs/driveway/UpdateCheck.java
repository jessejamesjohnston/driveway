// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Whether a newer Driveway has been published on GitHub. One unauthenticated
 * GET to GitHub's public "latest release" endpoint, carrying no cookies, no
 * account and nothing about the device or the installed version: only a fixed
 * User-Agent, which GitHub requires. The app downloads nothing; when there is
 * a newer version it opens {@link #RELEASES_PAGE} in the browser.
 *
 * <p>Plain Java with no Android classes, so its logic runs in the JVM checks
 * (test/java/.../UpdateCheckTest.java). The Android side (the About row, the
 * weekly job, the notification) is {@link UpdateJob} and SettingsActivity.
 */
final class UpdateCheck {
  private UpdateCheck() {}

  /** The public repository releases are published from. */
  static final String REPO = "jessejamesjohnston/driveway";
  /** Where a person reads about and downloads the newest release. Built here, never taken from a response. */
  static final String RELEASES_PAGE = "https://github.com/" + REPO + "/releases/latest";
  static final String API = "https://api.github.com";
  static final String USER_AGENT = "Driveway";
  private static final int TIMEOUT_MS = 15_000;
  /** A release description is small; anything bigger isn't one. */
  private static final int MAX_BODY = 1 << 20;

  /** What a check found. */
  enum Outcome {
    /** A later version than the installed one ({@link Result#version}). */
    AVAILABLE,
    /** The latest release is the installed version, or older. */
    UP_TO_DATE,
    /** The repository has no published release yet (GitHub answers 404). */
    NO_RELEASE,
    /** No connection, or GitHub couldn't be reached. */
    OFFLINE,
    /** GitHub answered, but not with a release that can be read (a rate limit, a server error, an odd tag). */
    FAILED
  }

  /** An outcome, and for AVAILABLE the newer version without its "v". */
  static final class Result {
    final Outcome outcome;
    final String version;

    Result(Outcome outcome, String version) {
      this.outcome = outcome;
      this.version = version;
    }
  }

  // The weekly check's settings, kept by the app (UpdateJob) through Store.

  /** Where the weekly check's two settings are kept: the app's preferences, or a map in the checks. */
  interface Store {
    String get(String key);

    void put(String key, String value);
  }

  static final String KEY_WEEKLY = "update_weekly", KEY_ANNOUNCED = "update_announced";

  /** Whether the weekly check is on. Off until the person turns it on. */
  static boolean weekly(Store s) {
    return "true".equals(s.get(KEY_WEEKLY));
  }

  static void setWeekly(Store s, boolean on) {
    s.put(KEY_WEEKLY, on ? "true" : "false");
  }

  /**
   * Whether the weekly check should tell the person about this result: a
   * newer version not announced before, so each version is announced once.
   */
  static boolean shouldAnnounce(Store s, Result r) {
    return r.outcome == Outcome.AVAILABLE && !r.version.equals(s.get(KEY_ANNOUNCED));
  }

  static void announced(Store s, Result r) {
    s.put(KEY_ANNOUNCED, r.version);
  }

  // Versions.

  private static final Pattern SHAPE = Pattern.compile("^[vV]?(\\d+(?:\\.\\d+)*)(?:[-+][0-9A-Za-z.-]*)?$");

  /**
   * The numbers in a release version ("0.2.13", or a tag "v0.2.13"; a suffix
   * such as "-beta1" or "-debug" is ignored), or null when it isn't one.
   */
  static List<Integer> parse(String text) {
    if (text == null) return null;
    Matcher m = SHAPE.matcher(text.trim());
    if (!m.matches()) return null;
    List<Integer> parts = new ArrayList<>();
    for (String p : m.group(1).split("\\.")) {
      try {
        parts.add(Integer.parseInt(p));
      } catch (NumberFormatException e) {
        return null;
      }
    }
    return parts;
  }

  /**
   * Whether candidate is a later version than current, compared number by
   * number (0.2.10 is after 0.2.9; 1.0 equals 1.0.0). null when either can't
   * be read.
   */
  static Boolean isNewer(String candidate, String current) {
    List<Integer> a = parse(candidate), b = parse(current);
    if (a == null || b == null) return null;
    for (int i = 0; i < Math.max(a.size(), b.size()); i++) {
      int x = i < a.size() ? a.get(i) : 0, y = i < b.size() ? b.get(i) : 0;
      if (x != y) return x > y;
    }
    return false;
  }

  /** A tag without its leading "v", for display ("Driveway 0.3.0 is available"). */
  static String display(String tag) {
    String t = tag.trim();
    return t.startsWith("v") || t.startsWith("V") ? t.substring(1) : t;
  }

  // The check.

  /** Asks GitHub (at apiBase; tests point it at a local server) and compares. Never throws. Blocks: not on the main thread. */
  static Result check(String installed, String apiBase) {
    HttpURLConnection c = null;
    try {
      c = (HttpURLConnection) new URL(apiBase + "/repos/" + REPO + "/releases/latest").openConnection();
      c.setConnectTimeout(TIMEOUT_MS);
      c.setReadTimeout(TIMEOUT_MS);
      c.setUseCaches(false);
      c.setInstanceFollowRedirects(true);
      c.setRequestProperty("Accept", "application/vnd.github+json");
      c.setRequestProperty("User-Agent", USER_AGENT);
      int code = c.getResponseCode();
      if (code == 404) return new Result(Outcome.NO_RELEASE, null);
      if (code != 200) return new Result(Outcome.FAILED, null);
      String tag;
      try (InputStream in = c.getInputStream()) {
        String body = read(in);
        if (body == null) return new Result(Outcome.FAILED, null);
        tag = new JSONObject(body).optString("tag_name", null);
      } catch (org.json.JSONException e) {
        return new Result(Outcome.FAILED, null);
      }
      Boolean newer = isNewer(tag, installed);
      if (newer == null) return new Result(Outcome.FAILED, null);
      return newer ? new Result(Outcome.AVAILABLE, display(tag)) : new Result(Outcome.UP_TO_DATE, null);
    } catch (IOException | RuntimeException e) {
      return new Result(Outcome.OFFLINE, null);
    } finally {
      if (c != null) c.disconnect();
    }
  }

  /** The answer's text, or null when it is too large to be a release description. */
  private static String read(InputStream in) throws IOException {
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    byte[] buf = new byte[8192];
    for (int n; (n = in.read(buf)) > 0; ) {
      out.write(buf, 0, n);
      if (out.size() > MAX_BODY) return null;
    }
    return out.toString(StandardCharsets.UTF_8.name());
  }
}
