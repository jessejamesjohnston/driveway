// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import com.merrimentlabs.driveway.engine.Engine;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * The kinds of server Driveway connects to. The four tested ones have
 * hand-made forms; every other rclone service gets a form built from the
 * engine's own description of its settings ("More services").
 */
final class Kinds {
  private Kinds() {}

  /**
   * TEXT, NUMBER; PASSWORD (one rclone scrambles in its settings; the app
   * keeps it as typed); CHOICE (picked from a list); KEY (SFTP's own key);
   * FILE (a key or settings file, chosen with Android's file picker; its
   * contents are the value).
   */
  enum Type { TEXT, NUMBER, PASSWORD, CHOICE, KEY, FILE }

  static final class Field {
    final String key;
    final String label;
    final String hint;
    final Type type;
    final boolean required;
    final boolean advanced;
    final String fallback;
    final List<String> choices;
    /** Typed out of sight (passwords, API keys, tokens). */
    boolean masked;
    /** A pasted link becomes the ID it carries ("drive-folder"). */
    String link;
    /** Used once while setting up, never kept (a single-use login token). */
    boolean ephemeral;

    Field(String key, String label, String hint, Type type, boolean required, boolean advanced, String fallback,
        String... choices) {
      this.key = key;
      this.label = label;
      this.hint = hint;
      this.type = type;
      this.required = required;
      this.advanced = advanced;
      this.fallback = fallback;
      this.choices = Arrays.asList(choices);
      this.masked = type == Type.PASSWORD;
    }
  }

  /** How a service without a browser sign-in in Driveway is set up instead. */
  static final class Alt {
    /** "Connect with <with>." */
    String with;
    /** Where to get it. */
    String how;
    /** The service's subtitle in the list. */
    String row;
    /** Setup trades the credential for a token (Jottacloud). */
    boolean makesToken;
  }

  static final String SIGN_IN = "signin", FORM = "form", ALTERNATIVE = "alternative", UNAVAILABLE = "unavailable";
  /** Where a service shows (engine/forms.go, State). */
  static final String LISTED = "listed", HIDDEN = "hidden";

  static final class Kind {
    final String backend;
    final String label;
    final String blurb;
    final List<Field> fields;
    /** Help shown under the form, or null. */
    final String note;
    /**
     * The service can sign in through its web page (a browser opens), and
     * a server set up that way has a token. Whether a given server signs in
     * that way: browser().
     */
    boolean signIn;
    /** Its sign-in uses an app registration built into rclone, not Merriment Labs' own. */
    boolean borrowedSignIn;
    /** Merriment Labs' own app registration with the service (Dropbox, OneDrive), or null. */
    String ownClientId;
    /** How it is set up: SIGN_IN, FORM, ALTERNATIVE or UNAVAILABLE (no way in). */
    String method = FORM;
    /** The way in instead of a browser sign-in (ALTERNATIVE). */
    Alt alt;
    /** Settings a new server starts with. */
    final java.util.Map<String, String> prefill = new java.util.HashMap<>();
    /** With the alternative's credential, connect without the service's setup. */
    boolean skipSetup;
    /** "Start in folder" hint where the top is buckets or containers, or null. */
    String folderHint;
    /** LISTED, or HIDDEN: listed nowhere, while a server of it added before keeps working. */
    String state = LISTED;
    /** The app version that first offered it (its New label), or null if offered before New labels began. */
    String since;
    /** The service can only be read (a web folder): nothing can be saved, renamed or deleted there. */
    boolean readOnly;

    Kind(String backend, String label, String blurb, String note, Field... fields) {
      this.backend = backend;
      this.label = label;
      this.blurb = blurb;
      this.note = note;
      this.fields = Arrays.asList(fields);
    }
  }

  static final Kind SFTP = new Kind("sftp", "SFTP", "Any computer you can SSH into: Linux, a Mac, a NAS", null,
      new Field("host", "Server", "nas.local or 192.168.1.10", Type.TEXT, true, false, ""),
      new Field("user", "User name", "", Type.TEXT, true, false, ""),
      new Field("key_pem", "Sign in with", "", Type.KEY, false, false, ""),
      new Field("pass", "Password", "Leave empty to use Driveway's key", Type.PASSWORD, false, false, ""),
      new Field("port", "Port", "", Type.NUMBER, false, true, "22"));

  static final Kind SMB = new Kind("smb", "SMB", "Windows and Mac file sharing, Synology, most NAS boxes", null,
      new Field("host", "Server", "nas.local or 192.168.1.10", Type.TEXT, true, false, ""),
      new Field("user", "User name", "", Type.TEXT, false, false, ""),
      new Field("pass", "Password", "", Type.PASSWORD, false, false, ""),
      new Field("domain", "Domain", "", Type.TEXT, false, true, "WORKGROUP"),
      new Field("port", "Port", "", Type.NUMBER, false, true, "445"));

  static final Kind NFS = new Kind("nfs", "NFS", "NFS version 3 exports from Linux servers and NAS boxes",
      "Driveway connects from a port above 1024, as every Android app must. Most NFS servers refuse that "
          + "unless the export allows it: add \"insecure\" to the export in /etc/exports, or on Synology tick "
          + "\"Allow connections from non-privileged ports\".",
      new Field("host", "Server", "nas.local or 192.168.1.10", Type.TEXT, true, false, ""),
      new Field("export", "Export", "/volume1/share", Type.TEXT, true, false, ""),
      new Field("uid", "User ID", "Files are read and written as this user", Type.NUMBER, false, true, "1000"),
      new Field("gid", "Group ID", "", Type.NUMBER, false, true, "1000"),
      new Field("port", "Port", "0 asks the server, which suits most", Type.NUMBER, false, true, "0"));

  static final Kind WEBDAV = new Kind("webdav", "WebDAV", "Nextcloud, ownCloud, Synology, and other web file servers",
      null,
      new Field("url", "Address", "https://cloud.example.com/remote.php/dav/files/you", Type.TEXT, true, false, ""),
      new Field("user", "User name", "", Type.TEXT, false, false, ""),
      new Field("pass", "Password", "", Type.PASSWORD, false, false, ""),
      new Field("vendor", "Server software", "", Type.CHOICE, false, true, "other",
          "other", "nextcloud", "owncloud", "infinitescale", "sharepoint-ntlm", "rclone", "fastmail"));

  static final List<Kind> MAIN = Arrays.asList(SFTP, SMB, NFS, WEBDAV);

  static Kind main(String backend) {
    for (Kind k : MAIN) if (k.backend.equals(backend)) return k;
    return null;
  }

  /**
   * What was typed in a Server field, split into the host and a port: many
   * people type "nas.local:2222", where the port has its own setting under
   * More settings. Only for kinds that have both (takesPort).
   */
  static final class Address {
    /**
     * The host without brackets (an IPv6 address as it is, "2001:db8::1"),
     * the one form saved; the engine adds the brackets a backend needs.
     */
    final String host;
    /** The port typed with it ("2222", leading zeros dropped), or null for none. */
    final String port;
    /** Why it can't be used as it is (an R.string id), or 0. */
    final int problem;
    /** The part of what was typed that the problem names. */
    final String bad;

    Address(String host, String port, int problem, String bad) {
      this.host = host;
      this.port = port;
      this.problem = problem;
      this.bad = bad;
    }
  }

  /**
   * What the form does with the Server field when focus leaves it (and on
   * Connect): a port typed after the name moves to Port, and wins over a
   * different one already there.
   */
  static final class Split {
    /** What was typed, split; when it has a problem, nothing changes. */
    final Address address;
    /** What Server shows afterwards. */
    final String host;
    /** What Port holds afterwards (as it was when no port was typed). */
    final String port;
    /**
     * The line saying where the port went (an R.string id), or 0 when it
     * didn't move: address_port_moved when it replaced another port (the
     * one in replaced), address_port_set when Port was empty or held the
     * default.
     */
    final int note;
    final String replaced;

    Split(Address address, String host, String port, int note, String replaced) {
      this.address = address;
      this.host = host;
      this.port = port;
      this.note = note;
      this.replaced = replaced;
    }
  }

  static Split split(String typed, String portNow, String fallback, String backend) {
    Address a = address(typed, backend);
    String before = portNow.trim();
    if (a.problem != 0) return new Split(a, typed, portNow, 0, null);
    if (a.port == null || a.port.equals(before)) return new Split(a, a.host, portNow, 0, null);
    boolean replaces = !before.isEmpty() && !before.equals(fallback);
    return new Split(a, a.host, a.port, replaces ? R.string.address_port_moved : R.string.address_port_set,
        replaces ? before : null);
  }

  /**
   * An SSH host key type ("ssh-ed25519") as a person names it and the file
   * that holds the server's own copy: {"Ed25519", "ssh_host_ed25519_key.pub"}.
   * Null for a type with no usual file.
   */
  static String[] hostKeyFile(String type) {
    if (type == null) return null;
    if (type.equals("ssh-ed25519")) return new String[] {"Ed25519", "ssh_host_ed25519_key.pub"};
    if (type.equals("ssh-rsa")) return new String[] {"RSA", "ssh_host_rsa_key.pub"};
    if (type.startsWith("ecdsa-sha2-")) return new String[] {"ECDSA", "ssh_host_ecdsa_key.pub"};
    return null;
  }

  /** Whether the kind has a Server (host) setting and a Port setting of its own. */
  static boolean takesPort(Kind k) {
    boolean host = false, port = false;
    for (Field f : k.fields) {
      host |= f.key.equals("host");
      port |= f.key.equals("port");
    }
    return host && port;
  }

  /** The address schemes a kind's own links use (sftp://, smb://), which can be typed before the host. */
  private static List<String> schemes(String backend) {
    switch (backend) {
      case "sftp":
        return Arrays.asList("sftp", "ssh");
      case "smb":
        return Arrays.asList("smb", "cifs");
      case "nfs":
        return Arrays.asList("nfs");
      case "ftp":
        return Arrays.asList("ftp");
      default:
        return Arrays.asList();
    }
  }

  /**
   * Splits a Server field: "host", "host:port", "[IPv6]:port", or one of the
   * kind's own links without a folder ("sftp://host:22"). An IPv6 address
   * without brackets ("::1", two colons or more) is a host with no port. A
   * trailing colon has no port. Anything else that can't be a host (a port
   * that isn't 1 to 65535, another kind's link, a folder, a user name before
   * an "@") is a problem, said in plain words, never a host to look up. On
   * SMB, the Windows form "\\nas\share" is read as "nas/share": the
   * name, and a folder said in the form it was typed.
   */
  static Address address(String typed, String backend) {
    String s = typed.trim();
    boolean windows = backend.equals("smb") && s.indexOf('\\') >= 0;
    if (windows) s = s.replaceFirst("^\\\\\\\\", "").replace('\\', '/');
    java.util.regex.Matcher m = java.util.regex.Pattern.compile("^([A-Za-z][A-Za-z0-9+.-]*)://(.*)$").matcher(s);
    if (m.matches()) {
      String scheme = m.group(1).toLowerCase(java.util.Locale.ROOT);
      if (!schemes(backend).contains(scheme)) return new Address(s, null, R.string.address_scheme, scheme + "://");
      s = m.group(2);
    }
    int slash = s.indexOf('/');
    if (slash >= 0) {
      if (!s.substring(slash).matches("/*")) {
        String folder = s.substring(slash);
        return new Address(s, null, R.string.address_folder, windows ? folder.replace('/', '\\') : folder);
      }
      s = s.substring(0, slash);
    }
    int at = s.lastIndexOf('@');
    if (at >= 0) return new Address(s, null, R.string.address_user, s.substring(0, at + 1));
    String host, port;
    if (s.startsWith("[")) {
      int close = s.indexOf(']');
      String tail = close < 0 ? "" : s.substring(close + 1);
      if (close < 2 || !(tail.isEmpty() || tail.startsWith(":"))) return new Address(s, null, R.string.address_ipv6, s);
      host = s.substring(1, close);
      port = tail.isEmpty() ? "" : tail.substring(1);
    } else {
      int colon = s.indexOf(':');
      if (colon < 0 || s.indexOf(':', colon + 1) >= 0) return new Address(s, null, 0, null);
      host = s.substring(0, colon);
      port = s.substring(colon + 1);
    }
    if (port.isEmpty()) return new Address(host, null, 0, null);
    if (!port.matches("[0-9]+")) return new Address(host, null, R.string.address_port_bad, port);
    String n = port.replaceFirst("^0+(?=.)", "");
    if (n.length() > 5 || Integer.parseInt(n) < 1 || Integer.parseInt(n) > 65535) {
      return new Address(host, null, R.string.address_port_range, port);
    }
    return new Address(host, n, 0, null);
  }

  static String label(String backend) {
    Kind k = main(backend);
    if (k != null) return k.label;
    Kind other = other(backend);
    if (other != null) return other.label;
    return REMOVED.containsKey(backend) ? REMOVED.get(backend) : backend;
  }

  /**
   * Services taken out of Driveway (Google Photos, ShareFile, and the ones
   * that layer on top of another server), by rclone name, with
   * the name people know them by. The engine no longer has them
   * (engine/rclone_backends.go): they aren't listed or offered, and a server
   * of one added before shows on its card that it can't open, with Remove.
   */
  static final java.util.Map<String, String> REMOVED = new java.util.HashMap<>();

  static {
    REMOVED.put("google photos", "Google Photos");
    REMOVED.put("sharefile", "ShareFile");
    // Those that only layer on top of another server.
    REMOVED.put("crypt", "Encrypted folder (crypt)");
    REMOVED.put("union", "Union");
    REMOVED.put("chunker", "Chunker");
    REMOVED.put("combine", "Combine");
    REMOVED.put("compress", "Compress");
    REMOVED.put("hasher", "Hasher");
    REMOVED.put("archive", "Archive files");
    REMOVED.put("alias", "Alias");
    REMOVED.put("cache", "Cache");
  }

  static boolean removed(String backend) {
    return REMOVED.containsKey(backend);
  }

  /** A removed service that sat on top of another server (no account of its own). */
  static boolean layered(String backend) {
    return removed(backend) && !backend.equals("google photos") && !backend.equals("sharefile");
  }

  private static List<Kind> others;

  /**
   * Every other service, with a form from its settings. Services that sign
   * in through a web page (Google Drive, Dropbox, OneDrive and the like) get
   * their token from rclone's own sign-in, which opens the browser; their app
   * ID and secret are optional (rclone's are used otherwise).
   */
  static synchronized List<Kind> others() {
    if (others != null) return others;
    List<Kind> out = new ArrayList<>();
    try {
      JSONArray arr = new JSONArray(Engine.backends());
      for (int i = 0; i < arr.length(); i++) {
        JSONObject b = arr.getJSONObject(i);
        String name = b.getString("name");
        if (main(name) != null) continue;
        List<Field> fields = new ArrayList<>();
        // The essentials, above the Connect button, in the curated order.
        JSONArray ess = b.optJSONArray("essentials");
        for (int j = 0; ess != null && j < ess.length(); j++) {
          JSONObject e = ess.getJSONObject(j);
          List<String> choices = strings(e.optJSONArray("examples"));
          Type type = !e.optString("file").isEmpty() ? Type.FILE : e.optBoolean("choice") && !choices.isEmpty() ? Type.CHOICE
              : e.optBoolean("password") ? Type.PASSWORD : Type.TEXT;
          // A default rclone takes from where it runs (FTP's user name is
          // the device's account) is never shown.
          String fallback = type == Type.CHOICE ? e.optString("default", choices.isEmpty() ? "" : choices.get(0)) : "";
          Field f = new Field(e.getString("key"), e.optString("label"), e.optString("hint", ""), type,
              e.optBoolean("required"), false, fallback, choices.toArray(new String[0]));
          f.masked = e.optBoolean("secret") || type == Type.PASSWORD;
          f.link = e.optString("link", null);
          f.ephemeral = e.optBoolean("ephemeral");
          fields.add(f);
        }
        // Everything else, under More settings.
        JSONArray opts = b.optJSONArray("options");
        for (int j = 0; opts != null && j < opts.length(); j++) {
          JSONObject o = opts.getJSONObject(j);
          String key = o.getString("name");
          String help = o.optString("help", "");
          Type type = o.optBoolean("password") ? Type.PASSWORD : Type.TEXT;
          // rclone's words for these ("OAuth Client Id.") say nothing to most people.
          String hint = key.startsWith("client_") ? "Leave empty to use the usual sign-in" : hint(help);
          fields.add(new Field(key, sentence(key), hint, type, false, true, o.optString("default", "")));
        }
        String description = b.optString("description", name);
        String label = b.optString("label", description);
        Kind k = new Kind(name, label, b.optString("blurb", description.equals(label) ? "" : description), null,
            fields.toArray(new Field[0]));
        k.signIn = b.optBoolean("browserSignIn");
        k.borrowedSignIn = b.optBoolean("borrowedSignIn");
        k.ownClientId = b.optString("ownClientId", null);
        k.method = b.optString("method", FORM);
        k.skipSetup = b.optBoolean("skipSetup");
        k.folderHint = b.optString("folderHint", null);
        k.state = b.optString("state", LISTED);
        k.since = b.optString("since", null);
        k.readOnly = b.optBoolean("readOnly");
        JSONObject alt = b.optJSONObject("alt");
        if (alt != null) {
          k.alt = new Alt();
          k.alt.with = alt.optString("with");
          k.alt.how = alt.optString("how");
          k.alt.row = alt.optString("row");
          k.alt.makesToken = alt.optBoolean("makesToken");
        }
        putAll(k.prefill, b.optJSONObject("prefill"));
        out.add(k);
      }
    } catch (Exception e) {
      android.util.Log.e("Driveway", "can't list services", e);
    }
    out.sort((a, b) -> a.label.compareToIgnoreCase(b.label));
    others = out;
    return others;
  }

  private static List<String> strings(JSONArray a) {
    List<String> out = new ArrayList<>();
    for (int i = 0; a != null && i < a.length(); i++) out.add(a.optString(i));
    return out;
  }

  private static void putAll(java.util.Map<String, String> to, JSONObject from) {
    if (from == null) return;
    for (java.util.Iterator<String> it = from.keys(); it.hasNext(); ) {
      String key = it.next();
      to.put(key, from.optString(key));
    }
  }

  /**
   * rclone's Title Case hints, in sentence case. Exact text only: a general
   * rule would lowercase product names and acronyms.
   */
  private static final Map<String, String> SENTENCE_CASE = new HashMap<>();
  static {
    SENTENCE_CASE.put("Storage Connection String", "Storage connection string");
    SENTENCE_CASE.put("Box App Primary Access Token", "Box app primary access token");
    SENTENCE_CASE.put("IAS3 Endpoint", "IAS3 endpoint");
    SENTENCE_CASE.put("Siad User Agent", "Siad user agent");
    SENTENCE_CASE.put("Sugarsync App ID", "Sugarsync app ID");
    SENTENCE_CASE.put("Sugarsync Access Key ID", "Sugarsync access key ID");
    SENTENCE_CASE.put("Sugarsync Private Access Key", "Sugarsync private access key");
    SENTENCE_CASE.put("Master Keys (internal use only)", "Master keys (internal use only)");
    SENTENCE_CASE.put("Private RSA Key (internal use only)", "Private RSA key (internal use only)");
    SENTENCE_CASE.put("Public RSA Key (internal use only)", "Public RSA key (internal use only)");
    SENTENCE_CASE.put("Authentication Version (internal use only)", "Authentication version (internal use only)");
    SENTENCE_CASE.put("Use Implicit FTPS (FTP over TLS)", "Use implicit FTPS (FTP over TLS)");
    SENTENCE_CASE.put("Use Explicit FTPS (FTP over TLS)", "Use explicit FTPS (FTP over TLS)");
    SENTENCE_CASE.put("Application Credential ID (OS_APPLICATION_CREDENTIAL_ID)",
        "Application credential ID (OS_APPLICATION_CREDENTIAL_ID)");
    SENTENCE_CASE.put("Application Credential Name (OS_APPLICATION_CREDENTIAL_NAME)",
        "Application credential name (OS_APPLICATION_CREDENTIAL_NAME)");
    SENTENCE_CASE.put("Application Credential Secret (OS_APPLICATION_CREDENTIAL_SECRET)",
        "Application credential secret (OS_APPLICATION_CREDENTIAL_SECRET)");
  }

  static Set<String> sentenceCaseKeys() {
    return SENTENCE_CASE.keySet();
  }

  /**
   * The hint shown under a setting from rclone's help: its first line. A
   * single sentence loses its closing period (Driveway's own hints have
   * none); two or more keep theirs. "e.g." and "i.e." don't end a sentence.
   */
  static String hint(String help) {
    String first = help.split("\n", 2)[0].trim();
    String bare = first.replaceAll("\\b(e\\.g|i\\.e)\\.", "");
    int ends = 0;
    for (java.util.regex.Matcher m = java.util.regex.Pattern.compile("[.!?](?=\\s|$)").matcher(bare); m.find(); ) ends++;
    if (ends == 1 && first.endsWith(".") && !first.endsWith("..")) first = first.substring(0, first.length() - 1);
    return SENTENCE_CASE.getOrDefault(first, first);
  }

  /** rclone's setting name as a label: "chunk_size" is "Chunk size". */
  static String sentence(String key) {
    switch (key) {
      case "client_id":
        return "Client ID of your own app";
      case "client_secret":
        return "Client secret of your own app";
      default:
        break;
    }
    StringBuilder b = new StringBuilder();
    for (String w : key.split("_")) {
      if (w.isEmpty()) continue;
      if (b.length() > 0) b.append(' ');
      switch (w) {
        case "id": case "url": case "api": case "acl": case "sse": case "kms": case "tls": case "md5": case "2fa":
        case "oci": case "sas": case "uid": case "gid": case "http": case "http2": case "jwt": case "ftp":
          b.append(w.toUpperCase(java.util.Locale.ROOT));
          break;
        default:
          b.append(w);
      }
    }
    String s = b.toString();
    return s.isEmpty() ? s : Character.toUpperCase(s.charAt(0)) + s.substring(1);
  }

  static Kind other(String backend) {
    for (Kind k : others()) if (k.backend.equals(backend)) return k;
    return null;
  }

  static Kind of(String backend) {
    Kind k = main(backend);
    return k != null ? k : other(backend);
  }

  /** Whether servers of the service can only be read. */
  static boolean readOnly(String backend) {
    Kind k = of(backend);
    return k != null && k.readOnly;
  }

  /**
   * The cloud services offered by name at the start of Add a server, in this
   * order: only those that sign in with one tap, A to Z. Box is one when the build carries Merriment Labs' Box
   * registration; Google Drive (a service account key) is in More services.
   */
  static final List<String> CLOUD = Arrays.asList("box", "dropbox", "onedrive");

  /** Whether the service belongs in the cloud list at the start of Add a server. */
  static boolean oneTap(Kind k) {
    return k != null && SIGN_IN.equals(k.method) && !HIDDEN.equals(k.state);
  }

  /**
   * A service that can be added now: listed, and with a way in. Only these
   * are shown. A HIDDEN service (state) or one with no way in (UNAVAILABLE:
   * a sign-in that would borrow an app registration built into rclone and
   * no other way in, or a config file on disk) is listed nowhere; a server
   * of one added before keeps working: its card, Change and Files. The
   * engine's catalog decides (engine/forms.go).
   */
  static boolean offered(Kind k) {
    if (k == null) return false;
    if (main(k.backend) != null) return true;
    return !HIDDEN.equals(k.state) && !UNAVAILABLE.equals(k.method);
  }

  /**
   * Whether this server signs in through the service's web page: a service
   * offered that way (Dropbox, OneDrive), or a server that signed in that way
   * before Driveway stopped offering its service's browser sign-in (it has a token,
   * and none of the alternative's credential).
   */
  static boolean browser(Kind k, JSONObject params) {
    if (k == null || !k.signIn) return false;
    // A Box server set up with an app's settings file before Box signed in
    // with one tap keeps that way in (engine: usesOwnRegistration).
    if (SIGN_IN.equals(k.method)) return params == null || !params.has("config_credentials");
    if (k.alt != null && (k.alt.makesToken || altPresent(k, params))) return false;
    return params != null && params.has("token");
  }

  /**
   * Whether the server can sign in again through the browser: only where
   * Driveway has a sign-in of its own (Dropbox, OneDrive, and servers of them
   * that signed in as rclone before). A server that signed in before Driveway
   * stopped offering its service's browser sign-in keeps its token, but signing
   * in again would borrow rclone's registration: it is changed instead (its
   * service's other way in, where there is one).
   */
  static boolean signsInAgain(Kind k, JSONObject params) {
    return k != null && SIGN_IN.equals(k.method) && browser(k, params);
  }

  /**
   * Whether Change can bring back a server whose sign-in ended: its form has
   * another way in (a key file, an API key). For a service with no way in
   * (pCloud and the like) nothing on the form can.
   */
  static boolean changeRestores(Kind k) {
    return k != null && k.alt != null;
  }

  /**
   * A server that signed in to Dropbox or OneDrive before Driveway had its
   * own registration: it still signs in as rclone (no client ID of its own),
   * and keeps working that way until it signs in again.
   */
  static boolean oldSignIn(Kind k, JSONObject params) {
    return k != null && k.ownClientId != null && browser(k, params) && params != null && params.has("token")
        && params.optString("client_id", "").isEmpty()
        // A OneDrive SharePoint library keeps rclone's sign-in (Driveway's can't reach it).
        && !"documentLibrary".equals(params.optString("drive_type"));
  }

  /** The server has the alternative's credential (the form's first essential). */
  static boolean altPresent(Kind k, JSONObject params) {
    if (k == null || k.alt == null || params == null || k.fields.isEmpty()) return false;
    Field credential = k.fields.get(0);
    return !credential.ephemeral && !params.optString(credential.key).isEmpty();
  }

  /**
   * Whether the form can't go on without the field, for the server as it is
   * saved: a single-use token is needed only until the service has given its
   * own access, and a server that signed in through the browser before
   * Driveway stopped offering that keeps that sign-in without the
   * alternative's credential.
   */
  static boolean required(Kind k, Field f, JSONObject saved) {
    if (!f.required) return false;
    if (f.ephemeral && saved != null && saved.has("token")) return false;
    return k == null || k.alt == null || k.fields.isEmpty() || k.fields.get(0) != f || !browser(k, saved);
  }

  /**
   * rclone's setup answers go to setup only, never into the saved server: a
   * field used once (a single-use login token), and any config_* answer that
   * isn't one of the service's settings (its questions, a form's presets).
   * A setting whose name only starts with config_ (Box's app settings file,
   * Oracle's config file) is the server's and stays.
   */
  static void dropSetupAnswers(Kind k, JSONObject p) {
    List<String> drop = new ArrayList<>();
    for (java.util.Iterator<String> it = p.keys(); it.hasNext(); ) {
      String key = it.next();
      Field f = null;
      for (Field x : k == null ? java.util.Collections.<Field>emptyList() : k.fields) if (x.key.equals(key)) f = x;
      if (f != null ? f.ephemeral : key.startsWith("config_")) drop.add(key);
    }
    for (String key : drop) p.remove(key);
  }

  /** A few words for a kind's tile in Add a server. */
  static String tileText(Kind k) {
    switch (k.backend) {
      case "sftp":
        return "Computers you SSH into";
      case "smb":
        return "Windows, Mac, most NAS boxes";
      case "nfs":
        return "Linux and NAS exports";
      case "webdav":
        return "Nextcloud and web servers";
      default:
        return k.blurb;
    }
  }

  /**
   * The letter on a one-tap cloud service's tile, in Driveway's own colors:
   * no company's logo ships (docs/BRAND-MARKS.md), and a letter tells the three apart where one cloud glyph didn't. null: the
   * service's glyph (Kinds.icon).
   */
  static String letter(String backend) {
    switch (backend == null ? "" : backend) {
      case "box":
        return "B";
      case "dropbox":
        return "D";
      case "onedrive":
        return "O";
      default:
        return null;
    }
  }

  /** The kind's icon (Material Symbols). */
  static int icon(String backend) {
    switch (backend == null ? "" : backend) {
      case "sftp":
        return R.drawable.ic_terminal;
      case "smb":
        return R.drawable.ic_folder;
      case "nfs":
        return R.drawable.ic_dns;
      case "webdav":
        return R.drawable.ic_language;
      default:
        return R.drawable.ic_cloud;
    }
  }

  /** A field that belongs under "Sign in" on the form (the rest go under "Server"). */
  static boolean signInField(Field f) {
    if (f.type == Type.PASSWORD || f.type == Type.KEY || f.type == Type.FILE || f.masked) return true;
    switch (f.key) {
      case "user":
      case "username":
      case "pass":
      case "password":
      case "access_key_id":
      case "secret_access_key":
      case "account":
      case "key":
        return true;
      default:
        return false;
    }
  }
}
