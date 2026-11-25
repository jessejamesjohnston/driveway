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

  /**
   * The most of one message that reaches the log: Android's log keeps about
   * 4 KB of an entry anyway, and a server's answer quoted whole in an error
   * can be megabytes. The cut comes after redaction, so a credential cut in
   * two is never left half-matched (engine/redact.go cuts its lines the same
   * way).
   */
  static final int MAX = 8000;

  static void i(String msg) {
    android.util.Log.i(TAG, forLog(msg));
  }

  static void w(String msg) {
    android.util.Log.w(TAG, forLog(msg));
  }

  static void w(String msg, Throwable e) {
    android.util.Log.w(TAG, forLog(msg + "\n" + trace(e)));
  }

  static void e(String msg) {
    android.util.Log.e(TAG, forLog(msg));
  }

  static void e(String msg, Throwable e) {
    android.util.Log.e(TAG, forLog(msg + "\n" + trace(e)));
  }

  /** msg redacted, then cut to MAX characters (saying how many more there were). */
  static String forLog(String msg) {
    String s = clean(msg);
    if (s == null || s.length() <= MAX) return s;
    int end = Character.isLowSurrogate(s.charAt(MAX)) ? MAX - 1 : MAX;
    return s.substring(0, end) + " [cut: " + (s.length() - end) + " more characters]";
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
      "[a-z0-9_.-]*?(?:api[_-]?key|keys?|key_pem|key_base64|tokens?|passw(?:or)?d|passphrase|pass|pwd|secrets?|"
          + "signature|sig|credentials?|sess(?:ion)?[_-]?id|sid|auth|authorization|mnemonic|grant|cookies?|connection_string|sas_url)";
  private static final Pattern URL = Pattern.compile("(?i)\\b[a-z][a-z0-9+.-]{0,30}://[^\\s\"'<>`\\\\]+");
  private static final Pattern ESCAPED_START = Pattern.compile("(?i)\\b[a-z][a-z0-9+.-]{0,30}:\\\\\\\\?/\\\\\\\\?/");
  private static final Pattern SCHEME_START = Pattern.compile("(?i)\\b[a-z][a-z0-9+.-]{0,30}://");
  // The same language as the engine's patterns, written so that the app's
  // regex engine doesn't recurse once per character (a long value would
  // overflow its stack): runs taken whole, without backtracking.
  private static final Pattern INVALID_PORT = Pattern.compile("(invalid port \")[^\"\\\\]*+(?:\\\\.[^\"\\\\]*+)*+(\" after host)");
  private static final Pattern BARE_PAIR = Pattern.compile(
      "(?i)(^|[\\s\"'(<\\[=,]|:/+|//)[^\\s\"'<>/@:=,(\\[]{1,200}:[^\\s\"'<>/]{1,200}@([a-z0-9][a-z0-9.-]*|\\[[0-9a-f:.%]+\\])");
  private static final Pattern MULTI_WORD =
      Pattern.compile("(?i)(^|[^a-z0-9_.-])([a-z0-9_.-]*?(?:mnemonic|passphrase))[ \\t]*=[ \\t]*");
  private static final String AUTH_SCHEMES =
      "(?:bearer|basic|digest|token|oauth|low|sharedkey(?:lite)?|aws4-hmac-sha256|aws|zoho-oauthtoken|negotiate|ntlm|mac)";
  private static final Pattern AUTH_SETTING = Pattern.compile("(?i)(^|[^a-z0-9_-])((?:proxy-|x-)?authorization)(\\s*=\\s*)(?:"
      + AUTH_SCHEMES + "[ \\t]+)?[^\\s&\"',;)\\]}]+");
  private static final Pattern NAMED_VALUE =
      Pattern.compile("(?i)(^|[^a-z0-9_.-])(" + SECRET_NAME + ")(\\s*=\\s*)(\"[^\"]*\"|'[^']*'|[^\\s&\"',;)\\]}]+)");
  private static final Pattern JSON_VALUE =
      Pattern.compile("(?i)(\\\\?\"" + SECRET_NAME + "\\\\?\"\\s*:\\s*)(\\\\?\")([^\"\\\\]*+(?:\\\\[^\"][^\"\\\\]*+)*+)(\\\\?\")");
  private static final Pattern XML_VALUE = Pattern.compile("(?i)(<(?:" + SECRET_NAME
      + "|awsaccesskeyid|stringtosign(?:bytes)?|canonicalrequest(?:bytes)?|signatureprovided)>)[^<]*(<)");
  private static final Pattern AUTH_HEADER = Pattern.compile("(?i)(^|[^a-z0-9_-])(pm-storage-token|"
      + "x-[a-z0-9-]*?(?:token|key|auth|auth-data|auth-sign|sign|signature|secret|sess-id|session-id|password)|"
      + "[a-z0-9-]*?(?:api-?key|auth-token|access-token))"
      + "(\\s*[:=]\\s*\\[?)((?:" + AUTH_SCHEMES + "[ \\t]+)?[^\\s\"',;\\]]+)");
  private static final Pattern PEM_BLOCK =
      Pattern.compile("(?s)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----).*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|\\z)");
  private static final Pattern AUTH_VALUE =
      Pattern.compile("(?i)(^|[^a-z0-9_-])((?:proxy-|x-)?authorization)([ \\t]*:[ \\t]*\\[?)[^\\r\\n\\]]+");
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
    s = PEM_BLOCK.matcher(s).replaceAll("$1***$2");
    s = INVALID_PORT.matcher(s).replaceAll("$1:***$2");
    s = looseUserinfo(s);
    s = cleanEscaped(s);
    s = replace(URL, s, m -> cleanUrl(m.group()));
    s = BARE_PAIR.matcher(s).replaceAll("$1***@$2");
    s = multiWord(s);
    s = AUTH_SETTING.matcher(s).replaceAll("$1$2$3***");
    s = NAMED_VALUE.matcher(s).replaceAll("$1$2$3***");
    s = JSON_VALUE.matcher(s).replaceAll("$1$2***$4");
    s = XML_VALUE.matcher(s).replaceAll("$1***$2");
    s = AUTH_VALUE.matcher(s).replaceAll("$1$2$3***");
    s = AUTH_HEADER.matcher(s).replaceAll("$1$2$3***");
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
    int a = tail.lastIndexOf('@');
    if (a > 0 && !tail.substring(0, a).equals("/***")) tail = "/***" + tail.substring(a);
    tail = cleanPath(authority, tail);
    int c = authority.lastIndexOf(':');
    if (c >= 0 && !authority.contains("[") && !authority.substring(c + 1).matches("[0-9]*")) {
      authority = authority.substring(0, c + 1) + "***";
    }
    if (user >= 0) authority = "***@" + authority;
    return scheme + authority + tail + trail;
  }

  /** A multi-word setting's whole value (a mnemonic, a passphrase); see engine/redact.go. */
  private static String multiWord(String s) {
    Matcher m = MULTI_WORD.matcher(s);
    StringBuilder b = new StringBuilder();
    int last = 0;
    while (m.find()) {
      if (m.start() < last) continue;
      int end = multiWordEnd(s, m.end());
      if (end == m.end()) continue;
      b.append(s, last, m.end()).append("***");
      last = end;
    }
    if (last == 0) return s;
    return b.append(s.substring(last)).toString();
  }

  private static int multiWordEnd(String s, int i) {
    int end = i;
    for (int j = i; j <= s.length(); ) {
      int k = j;
      while (k < s.length() && " \t\r\n\"'&".indexOf(s.charAt(k)) < 0) k++;
      if (k == j || s.substring(j, k).indexOf('=') >= 0) break;
      end = k;
      while (k < s.length() && (s.charAt(k) == ' ' || s.charAt(k) == '\t')) k++;
      j = k;
    }
    return end;
  }

  /** Each address whose slashes are escaped (https:\\/\\/host), redacted; see engine/redact.go. */
  private static String cleanEscaped(String s) {
    Matcher m = ESCAPED_START.matcher(s);
    StringBuilder b = new StringBuilder();
    int last = 0;
    while (m.find()) {
      if (m.start() < last) continue;
      int end = escapedEnd(s, m.end());
      String a = s.substring(m.start(), end);
      String esc = a.contains("\\\\/") ? "\\\\/" : "\\/";
      b.append(s, last, m.start()).append(cleanUrl(a.replace(esc, "/")).replace("/", esc));
      last = end;
    }
    if (last == 0) return s;
    return b.append(s.substring(last)).toString();
  }

  private static int escapedEnd(String s, int i) {
    while (i < s.length()) {
      if (s.startsWith("\\/", i)) {
        i += 2;
      } else if (s.startsWith("\\\\/", i)) {
        i += 3;
      } else if (" \t\n\r\f\u000B\"'<>`\\".indexOf(s.charAt(i)) >= 0) {
        return i;
      } else {
        i++;
      }
    }
    return i;
  }

  /** A user name and password holding a character URL stops at; see engine/redact.go. */
  private static String looseUserinfo(String s) {
    Matcher m = SCHEME_START.matcher(s);
    StringBuilder b = new StringBuilder();
    int last = 0;
    while (m.find()) {
      if (m.start() < last) continue;
      int start = m.end(), end = userinfoAt(s, start, m.start() > 0 && s.charAt(m.start() - 1) == '"');
      if (end < 0) continue;
      b.append(s, last, start).append("***");
      last = end;
    }
    if (last == 0) return s;
    return b.append(s.substring(last)).toString();
  }

  private static int userinfoAt(String s, int i, boolean quoted) {
    int limit = Math.min(s.length(), i + 512);
    for (int j = i; j < limit; j++) {
      char c = s.charAt(j);
      if (c == '\n' || c == '\r') return -1;
      if (c != '@' || !hostFollows(s, j + 1)) continue;
      String u = s.substring(i, j);
      if (u.equals("***") || u.contains("://") || u.contains("\": ")) return -1;
      if (containsAny(u, " \t\"'<>`\\") && (userPass(u) || quoted && ambiguousAt(s.substring(i, j + 1)) >= 0)) return j;
    }
    return -1;
  }

  private static boolean userPass(String u) {
    int c = u.indexOf(':');
    if (c < 0 || containsAny(u.substring(0, c), " \t\"'/\\")) return false;
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

  /** Where the user name and password before the host end (the @), or -1; see engine/redact.go userinfoEnd. */
  private static int userinfoEnd(String rest) {
    int amb = ambiguousAt(rest);
    if (amb >= 0) return amb;
    int end = indexOfAny(rest, "/?#");
    if (end < 0) end = rest.length();
    String hostport = rest.substring(0, end);
    int at = hostport.lastIndexOf('@');
    if (at >= 0) hostport = hostport.substring(at + 1);
    int c = hostport.lastIndexOf(':');
    if (c >= 0 && !hostport.substring(c).contains("]") && !hostport.substring(c + 1).matches("[0-9]*")) {
      int a = rest.lastIndexOf('@');
      if (a >= end) return a;
    }
    return rest.substring(0, end).lastIndexOf('@');
  }

  /**
   * The address rule's reading for the redactor (engine/address.go ambiguousAt with anywhere true): when
   * what follows the last colon of the authority is empty or only digits and an @ comes after it, the last @.
   */
  private static int ambiguousAt(String rest) {
    int end = indexOfAny(rest, "/?#");
    if (end < 0) return -1;
    String hostport = rest.substring(0, end);
    int at = hostport.lastIndexOf('@');
    if (at >= 0) hostport = hostport.substring(at + 1);
    int c = hostport.lastIndexOf(':');
    if (c < 0 || hostport.substring(c).contains("]") || !hostport.substring(c + 1).matches("[0-9]*")) return -1;
    int a = rest.substring(end).lastIndexOf('@');
    return a < 0 ? -1 : end + a;
  }

  /** Services that put a credential in the path, by the end of the host. */
  /** Credentials in a path, on one host's or (when token-like) any host (""); see engine/redact.go. */
  private static final String[] SECRET_PATH_HOSTS = {"opendrive.com", "1drv.com", "1fichier.com", "", ""};
  private static final Pattern[] SECRET_PATHS = {
      Pattern.compile("(\\.json/)([^/?#]+)"), Pattern.compile("^(/up/)([^/?#]+)"), Pattern.compile("^(/dir/)([^/?#]+)"),
      Pattern.compile("^(/(?:up|rup)/)([^/?#]+)"), Pattern.compile("(\\.json/)([^/?#]+)"),
  };

  private static boolean tokenLike(String seg) {
    return seg.length() >= 24 && seg.matches("[A-Za-z0-9_.!~-]+") && seg.matches(".*[0-9].*") && seg.matches(".*[A-Za-z].*");
  }

  private static String cleanPath(String authority, String p) {
    String host = authority.toLowerCase(java.util.Locale.ROOT);
    int c = host.lastIndexOf(':');
    if (c >= 0 && !host.endsWith("]")) host = host.substring(0, c);
    for (int i = 0; i < SECRET_PATHS.length; i++) {
      String h = SECRET_PATH_HOSTS[i];
      boolean any = h.isEmpty();
      if (!any && !host.equals(h) && !host.endsWith("." + h)) continue;
      p = replace(SECRET_PATHS[i], p, m -> any && !tokenLike(m.group(2)) ? m.group() : m.group(1) + "***");
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
