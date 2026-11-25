// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import java.io.PrintWriter;
import java.io.StringWriter;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * The app's log, with credentials taken out. An error's text can carry one
 * (a service that takes its API key in the address puts it in every network
 * error), and people copy the log into bug reports. The engine already
 * redacts what it returns and logs (engine/redact.go); every line the app
 * logs with text it didn't write itself passes through here as well, with
 * the same rules, checked against the same cases (test/redact_cases.json).
 */
final class Logs {
  static final String TAG = "Driveway";

  private Logs() {}

  static void i(String msg) {
    android.util.Log.i(TAG, clean(msg));
  }

  static void w(String msg) {
    android.util.Log.w(TAG, clean(msg));
  }

  static void w(String msg, Throwable e) {
    android.util.Log.w(TAG, clean(msg + "\n" + trace(e)));
  }

  static void e(String msg) {
    android.util.Log.e(TAG, clean(msg));
  }

  static void e(String msg, Throwable e) {
    android.util.Log.e(TAG, clean(msg + "\n" + trace(e)));
  }

  /** The exception, its causes and where each was thrown, as Android's log prints them. */
  static String trace(Throwable e) {
    if (e == null) return "";
    StringWriter s = new StringWriter();
    e.printStackTrace(new PrintWriter(s));
    return s.toString().trim();
  }

  // The same rules as engine/redact.go (see there for why each exists).
  private static final String SECRET_NAME =
      "[a-z0-9_.-]*?(?:api[_-]?key|key|token|passw(?:or)?d|passphrase|pass|pwd|secret|signature|sig|credential|"
          + "sess(?:ion)?[_-]?id|sid|auth|authorization)";
  private static final Pattern URL = Pattern.compile("(?i)\\b[a-z][a-z0-9+.-]*://[^\\s\"'<>`\\\\]+");
  private static final Pattern SCHEME_START = Pattern.compile("(?i)\\b[a-z][a-z0-9+.-]*://");
  private static final Pattern INVALID_PORT = Pattern.compile("(invalid port \")(?:[^\"\\\\]|\\\\.)*(\" after host)");
  private static final Pattern BARE_PAIR = Pattern.compile(
      "(?i)(^|[\\s\"'(<\\[=,])[^\\s\"'<>/@:=,(\\[]+:[^\\s\"'<>/]{1,200}@([a-z0-9][a-z0-9.-]*|\\[[0-9a-f:.%]+\\])");
  private static final Pattern NAMED_VALUE =
      Pattern.compile("(?i)\\b(" + SECRET_NAME + ")(\\s*=\\s*)(\"[^\"]*\"|'[^']*'|[^\\s&\"',;)\\]}]+)");
  private static final Pattern JSON_VALUE =
      Pattern.compile("(?i)(\\\\?\"" + SECRET_NAME + "\\\\?\"\\s*:\\s*)(\\\\?\")((?:[^\"\\\\]|\\\\[^\"])*)(\\\\?\")");
  private static final Pattern XML_VALUE = Pattern.compile("(?i)(<(?:" + SECRET_NAME
      + "|awsaccesskeyid|stringtosign(?:bytes)?|canonicalrequest(?:bytes)?|signatureprovided)>)[^<]*(<)");
  private static final Pattern AUTH_HEADER = Pattern.compile("(?i)\\b((?:proxy-)?authorization|pm-storage-token|"
      + "x-[a-z0-9-]*?(?:token|key|auth|auth-data|auth-sign|sign|signature|secret|sess-id|session-id|password))"
      + "(\\s*[:=]\\s*\\[?)((?:(?:bearer|basic|digest|token|oauth|low|sharedkey(?:lite)?|aws4-hmac-sha256|aws|zoho-oauthtoken"
      + "|negotiate|ntlm|mac)[ \\t]+)?[^\\s\"',;\\]]+)");
  private static final Pattern COOKIE_HEADER = Pattern.compile("(?i)\\b((?:set-)?cookie)(\\s*:\\s*)[^\\r\\n\"]+");
  private static final Pattern BEARER = Pattern.compile("(?i)\\b(bearer)\\s+[a-z0-9._~+/=-]{8,}");
  private static final Pattern[] PATH_TOKENS = {
      Pattern.compile("(?i)(seafhttp/[a-z-]+/)([^/\\s'\"?#]+)"),
      Pattern.compile("(?i)(/upload-batch/private/)([^/\\s'\"?#]+)"),
      Pattern.compile("(?i)(/upload/(?:chunked|finalize)/)([^/\\s'\"?#]+)"),
      Pattern.compile("(?i)(/api/filesystem/)([^/\\s'\"?#]+)"),
  };
  /** Each with its replacement. */
  private static final Object[][] TAILS = {
      {Pattern.compile("(?i)(failed to get bearer token using )[^\\n]*"), "$1***"},
      {Pattern.compile("(?i)(fetched invalid link )[^\\n]*"), "$1***"},
      {Pattern.compile("(?i)(cannot unmarshal response(?: data)? )[^\\n]*"), "$1***"},
      {Pattern.compile("(?i)(\\bupload )\\S+( finalize)"), "$1***$2"},
  };

  /** s without the credentials it may hold; host, status and the kind of error stay. */
  static String clean(String s) {
    if (s == null) return null;
    s = INVALID_PORT.matcher(s).replaceAll("$1:***$2");
    s = looseUserinfo(s);
    s = replace(URL, s, m -> cleanUrl(m.group()));
    s = BARE_PAIR.matcher(s).replaceAll("$1***@$2");
    s = NAMED_VALUE.matcher(s).replaceAll("$1$2***");
    s = JSON_VALUE.matcher(s).replaceAll("$1$2***$4");
    s = XML_VALUE.matcher(s).replaceAll("$1***$2");
    s = AUTH_HEADER.matcher(s).replaceAll("$1$2***");
    s = COOKIE_HEADER.matcher(s).replaceAll("$1$2***");
    s = BEARER.matcher(s).replaceAll("$1 ***");
    for (Pattern p : PATH_TOKENS) {
      s = replace(p, s, m -> m.group(2).equals("me") || m.group(2).equals("***") ? m.group() : m.group(1) + "***");
    }
    for (Object[] t : TAILS) s = ((Pattern) t[0]).matcher(s).replaceAll((String) t[1]);
    return s;
  }

  private static String replace(Pattern p, String s, java.util.function.Function<Matcher, String> f) {
    Matcher m = p.matcher(s);
    StringBuilder b = new StringBuilder();
    while (m.find()) m.appendReplacement(b, Matcher.quoteReplacement(f.apply(m)));
    m.appendTail(b);
    return b.toString();
  }

  private static String cleanUrl(String m) {
    int keep = m.length();
    while (keep > 0 && ".,;:!?)]}".indexOf(m.charAt(keep - 1)) >= 0) keep--;
    String core = m.substring(0, keep), trail = m.substring(keep);
    int i = core.indexOf("://");
    if (i < 0) return m;
    String scheme = core.substring(0, i + 3), rest = core.substring(i + 3);
    int user = userinfoEnd(rest);
    if (user >= 0) rest = rest.substring(user + 1);
    int end = indexOfAny(rest, "/?#");
    if (end < 0) end = rest.length();
    String authority = rest.substring(0, end), tail = rest.substring(end);
    int q = indexOfAny(tail, "?#");
    if (q >= 0) tail = tail.substring(0, q) + "?***";
    tail = cleanPath(authority, tail);
    int c = authority.lastIndexOf(':');
    if (c >= 0 && !authority.contains("[") && !authority.substring(c + 1).matches("[0-9]*")) {
      authority = authority.substring(0, c + 1) + "***";
    }
    if (user >= 0) authority = "***@" + authority;
    return scheme + authority + tail + trail;
  }

  /** A user name and password holding a character URL stops at; see engine/redact.go. */
  private static String looseUserinfo(String s) {
    Matcher m = SCHEME_START.matcher(s);
    StringBuilder b = new StringBuilder();
    int last = 0;
    while (m.find()) {
      if (m.start() < last) continue;
      int start = m.end(), end = userinfoAt(s, start);
      if (end < 0) continue;
      b.append(s, last, start).append("***");
      last = end;
    }
    if (last == 0) return s;
    return b.append(s.substring(last)).toString();
  }

  private static int userinfoAt(String s, int i) {
    int limit = Math.min(s.length(), i + 512);
    for (int j = i; j < limit; j++) {
      char c = s.charAt(j);
      if (c == '\n' || c == '\r') return -1;
      if (c != '@' || !hostFollows(s, j + 1)) continue;
      String u = s.substring(i, j);
      if (u.equals("***") || u.contains("://") || u.contains("\": ")) return -1;
      if (containsAny(u, " \t\"'<>`\\") && userPass(u)) return j;
    }
    return -1;
  }

  private static boolean userPass(String u) {
    int c = u.indexOf(':');
    if (c <= 0 || containsAny(u.substring(0, c), " \t\"'/\\")) return false;
    String p = u.substring(c + 1);
    int k = indexOfAny(p, "/?#");
    if (k >= 0) p = p.substring(0, k);
    return !p.matches("[0-9]*");
  }

  private static boolean hostFollows(String s, int i) {
    int j = i;
    while (j < s.length() && isHostChar(s.charAt(j))) j++;
    if (j == i) return false;
    return j == s.length() || "/?#\"'\\ \t\r\n),;>`".indexOf(s.charAt(j)) >= 0;
  }

  private static boolean isHostChar(char c) {
    return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || ".-:[]%_".indexOf(c) >= 0;
  }

  private static boolean containsAny(String s, String chars) {
    return indexOfAny(s, chars) >= 0;
  }

  /** Where the user name and password before the host end (the @), or -1; see engine/redact.go. */
  private static int userinfoEnd(String rest) {
    int at = rest.indexOf('@');
    if (at < 0) return -1;
    int end = indexOfAny(rest, "/?#");
    if (end < 0 || at < end) return rest.substring(0, end < 0 ? rest.length() : end).lastIndexOf('@');
    String host = rest.substring(0, end);
    int c = host.indexOf(':');
    if (c < 0 || host.contains("[") || host.substring(c + 1).matches("[0-9]*")) return -1;
    return at;
  }

  /** Services that put a credential in the path, by the end of the host. */
  private static final String[] SECRET_PATH_HOSTS = {"opendrive.com", "1drv.com", "1fichier.com"};
  private static final Pattern[] SECRET_PATHS = {
      Pattern.compile("(\\.json/)[^/?#]+"), Pattern.compile("^(/up/)[^/?#]+"), Pattern.compile("^(/dir/)[^/?#]+"),
  };

  private static String cleanPath(String authority, String p) {
    String host = authority.toLowerCase(java.util.Locale.ROOT);
    int c = host.lastIndexOf(':');
    if (c >= 0 && !host.endsWith("]")) host = host.substring(0, c);
    for (int i = 0; i < SECRET_PATHS.length; i++) {
      String h = SECRET_PATH_HOSTS[i];
      if (host.equals(h) || host.endsWith("." + h)) p = SECRET_PATHS[i].matcher(p).replaceAll("$1***");
    }
    return p;
  }

  private static int indexOfAny(String s, String chars) {
    for (int i = 0; i < s.length(); i++) {
      if (chars.indexOf(s.charAt(i)) >= 0) return i;
    }
    return -1;
  }
}
