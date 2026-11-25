// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import com.merrimentlabs.driveway.engine.Engine;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * The kinds of server, on a plain JVM (./build.sh test): the forms built from
 * the engine's real catalog (written by the engine's TestBackendsJSON, so the
 * two can't drift), then made-up catalogs for what the real one doesn't have,
 * and the rules for listing, signing in and laying out a form.
 */
public final class KindsTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  private static boolean eq(Object a, Object b) {
    return a == null ? b == null : a.equals(b);
  }

  /** Kinds reads the catalog once; a made-up one needs the cache cleared. */
  private static void catalog(String json) throws Exception {
    Engine.json = json;
    java.lang.reflect.Field cache = Kinds.class.getDeclaredField("others");
    cache.setAccessible(true);
    cache.set(null, null);
  }

  private static Kinds.Field field(Kinds.Kind k, String key) {
    for (Kinds.Field f : k.fields) if (f.key.equals(key)) return f;
    return null;
  }

  private static JSONObject params(String... kv) throws Exception {
    JSONObject p = new JSONObject();
    for (int i = 0; i < kv.length; i += 2) p.put(kv[i], kv[i + 1]);
    return p;
  }

  /** A service set up in the test: sign-in flags and an optional credential field. */
  private static Kinds.Kind kind(String method, boolean signIn, String ownClientId, Kinds.Field... fields) {
    Kinds.Kind k = new Kinds.Kind("t-service", "Test service", "", null, fields);
    k.method = method;
    k.signIn = signIn;
    k.ownClientId = ownClientId;
    return k;
  }

  private static Kinds.Alt alt(boolean makesToken) {
    Kinds.Alt a = new Kinds.Alt();
    a.makesToken = makesToken;
    return a;
  }

  private static Kinds.Field text(String key) {
    return new Kinds.Field(key, key, "", Kinds.Type.TEXT, false, false, "");
  }

  public static void main(String[] args) throws Exception {
    realCatalog();
    addresses();
    madeUpCatalogs();
    names();
    listing();
    signIn();
    setupAnswers();
    layout();
    System.out.println(failures == 0 ? "KindsTest: all passed" : "KindsTest: " + failures + " failed");
    if (failures > 0) System.exit(1);
  }

  /** One Server field typed into a kind's form: the host, port and problem it splits into. */
  private static void address(String backend, String typed, String host, String port, int problem, String bad) {
    Kinds.Address a = Kinds.address(typed, backend);
    boolean ok = problem != 0 ? a.problem == problem && eq(a.bad, bad)
        : a.problem == 0 && eq(a.host, host) && eq(a.port, port);
    check(ok, backend + " Server \"" + typed + "\" -> " + (problem != 0 ? "problem " + bad
        : host + (port == null ? " (no port)" : " port " + port))
        + (ok ? "" : " (got " + a.host + ", " + a.port + ", " + a.problem + ", " + a.bad + ")"));
  }

  /**
   * Focus leaving the Server field (or Connect): what Server and Port then
   * hold, and the line that says where the port went.
   */
  private static void leavingTheServerField() {
    Kinds.Split s = Kinds.split("nas.local:2222", "", "22", "sftp");
    check(s.address.problem == 0 && s.host.equals("nas.local") && s.port.equals("2222")
        && s.note == R.string.address_port_set && s.replaced == null,
        "leaving Server: nas.local:2222 with Port empty -> Server nas.local, Port 2222, says the port moved");
    s = Kinds.split(" nas.local:2222 ", "22", "22", "sftp");
    check(s.host.equals("nas.local") && s.port.equals("2222") && s.note == R.string.address_port_set,
        "leaving Server: Port holding the default counts as empty");
    s = Kinds.split("nas.local:2222", "2200", "22", "sftp");
    check(s.host.equals("nas.local") && s.port.equals("2222") && s.note == R.string.address_port_moved
        && "2200".equals(s.replaced), "leaving Server: the typed port replaces another, and says which");
    s = Kinds.split("nas.local:2222", "2222", "22", "sftp");
    check(s.host.equals("nas.local") && s.port.equals("2222") && s.note == 0, "leaving Server: the same port, nothing to say");
    s = Kinds.split("nas.local", "2200", "22", "sftp");
    check(s.host.equals("nas.local") && s.port.equals("2200") && s.note == 0, "leaving Server: no port typed, Port as it was");
    s = Kinds.split("[2001:db8::1]:445", "", "445", "smb");
    check(s.host.equals("2001:db8::1") && s.port.equals("445") && s.note == R.string.address_port_set,
        "leaving Server: a bracketed IPv6 address and its port");
    s = Kinds.split("nas.local:ssh", "2200", "22", "sftp");
    check(s.address.problem == R.string.address_port_bad && s.host.equals("nas.local:ssh") && s.port.equals("2200")
        && s.note == 0, "leaving Server: what can't be used stays as typed, Port untouched");
  }

  /**
   * "host:port" in the Server field (the port has its own setting): which
   * kinds split it, and how every shape of address splits, against the real
   * catalog.
   */
  private static void addresses() {
    // The kinds with a Server and a Port setting, and only those.
    Set<String> splits = new HashSet<>();
    List<Kinds.Kind> all = new ArrayList<>(Kinds.MAIN);
    all.addAll(Kinds.others());
    for (Kinds.Kind k : all) if (Kinds.takesPort(k)) splits.add(k.backend);
    check(splits.equals(new HashSet<>(Arrays.asList("sftp", "smb", "nfs", "ftp"))),
        "Server fields that take a port: SFTP, SMB, NFS, FTP (got " + splits + ")");
    check(Kinds.other("quatrix") != null && !Kinds.takesPort(Kinds.other("quatrix")),
        "Quatrix's account address (no Port setting) is left as typed");

    leavingTheServerField();

    // Name in Files follows the host, and never keeps what the form refuses.
    check(Kinds.followedName("192.0.2.10:2222", Kinds.SFTP, true).equals("192.0.2.10")
        && Kinds.followedName("[2001:db8::1]:2222", Kinds.SMB, true).equals("2001:db8::1")
        && Kinds.followedName("\\\\nas", Kinds.SMB, true).equals("nas"), "Name in Files: the host typed");
    check(Kinds.followedName("driveway@192.0.2.10", Kinds.SFTP, true).isEmpty()
        && Kinds.followedName("nas.local/share", Kinds.SMB, true).isEmpty()
        && Kinds.followedName("https://192.0.2.10", Kinds.SFTP, true).isEmpty()
        && Kinds.followedName("nas.local:99999", Kinds.NFS, true).isEmpty(), "Name in Files: empty for what the form refuses");
    check(Kinds.followedName("http://[fe80::1%25wlan0]:5005/", Kinds.WEBDAV, false).equals("fe80::1%wlan0")
        && Kinds.followedName("http://[2001:db8::1]/dav", Kinds.WEBDAV, false).equals("2001:db8::1")
        && Kinds.followedName("HTTPS://[::1]:8443", Kinds.WEBDAV, false).equals("::1")
        && Kinds.followedName("http://[fe80::1", Kinds.WEBDAV, false).equals("fe80::1"),
        "Name in Files: an IPv6 WebDAV address is named for its whole address, without brackets");
    check(Kinds.followedName("https://alice@dav.example.com:8443/remote.php", Kinds.WEBDAV, false).equals("dav.example.com")
        && Kinds.followedName("https://dav.example.com?x=1", Kinds.WEBDAV, false).equals("dav.example.com"),
        "Name in Files: a URL's user name, port and query aren't part of the name");
    check(Kinds.followedName("https://cloud.example.com/dav", Kinds.WEBDAV, false).equals("cloud.example.com"),
        "Name in Files: a URL is named for its host");

    // The identity step names the file of the key type the server presented.
    check(Arrays.equals(Kinds.hostKeyFile("ssh-rsa"), new String[] {"RSA", "ssh_host_rsa_key.pub"})
        && Arrays.equals(Kinds.hostKeyFile("ssh-ed25519"), new String[] {"Ed25519", "ssh_host_ed25519_key.pub"})
        && Arrays.equals(Kinds.hostKeyFile("ecdsa-sha2-nistp256"), new String[] {"ECDSA", "ssh_host_ecdsa_key.pub"}),
        "host key types name their files: RSA, Ed25519, ECDSA");
    check(Kinds.hostKeyFile("x-made-up-key-type") == null && Kinds.hostKeyFile("") == null && Kinds.hostKeyFile(null) == null,
        "a host key type with no usual file names none");

    // A host alone, and with a port.
    address("sftp", "nas.local", "nas.local", null, 0, null);
    address("sftp", "  192.0.2.10  ", "192.0.2.10", null, 0, null);
    address("sftp", "192.0.2.10:2222", "192.0.2.10", "2222", 0, null);
    address("smb", "nas.local:1445", "nas.local", "1445", 0, null);
    address("nfs", "nas.local:2049", "nas.local", "2049", 0, null);
    address("ftp", "ftp.example.com:2121", "ftp.example.com", "2121", 0, null);
    address("sftp", "nas.local:02222", "nas.local", "2222", 0, null);
    address("sftp", "nas.local:65535", "nas.local", "65535", 0, null);
    // A trailing colon is no port.
    address("sftp", "nas.local:", "nas.local", null, 0, null);
    // IPv6: bracketed with or without a port, bare is a host (no port to split off).
    address("sftp", "[2001:db8::1]:2222", "2001:db8::1", "2222", 0, null);
    address("sftp", "[::1]:2222", "::1", "2222", 0, null);
    address("sftp", "[2001:db8::1]", "2001:db8::1", null, 0, null);
    address("sftp", "[2001:db8::1]:", "2001:db8::1", null, 0, null);
    address("sftp", "::1", "::1", null, 0, null);
    address("sftp", "2001:db8::1", "2001:db8::1", null, 0, null);
    address("sftp", "fe80::1%wlan0", "fe80::1%wlan0", null, 0, null);
    address("sftp", "[2001:db8::1", null, null, R.string.address_ipv6, "[2001:db8::1");
    address("sftp", "[2001:db8::1]2222", null, null, R.string.address_ipv6, "[2001:db8::1]2222");
    address("sftp", "[]:22", null, null, R.string.address_ipv6, "[]:22");
    // One saved form for a host, whatever was typed: an IPv6 address bare (the
    // engine adds the brackets each backend needs, engine/hosts.go). What a
    // split leaves in Server splits again to the same host and no port, so a
    // saved server opened and saved again keeps its address.
    for (String backend : Arrays.asList("sftp", "smb", "nfs", "ftp")) {
      for (String typed : Arrays.asList("[fd00::5]", "fd00::5", "[2001:db8::1]:2222", "::1", "[::1]",
          "fe80::1%wlan0", "[fe80::1%wlan0]:445", "nas.local:2222", "192.0.2.10")) {
        Kinds.Address first = Kinds.address(typed, backend);
        Kinds.Address again = Kinds.address(first.host, backend);
        check(first.problem == 0 && !first.host.startsWith("[") && again.problem == 0
            && again.host.equals(first.host) && again.port == null,
            backend + " Server \"" + typed + "\" is saved as " + first.host + ", which reads back the same");
      }
    }
    // A port that isn't one: said, never dropped.
    address("sftp", "nas.local:ssh", null, null, R.string.address_port_bad, "ssh");
    address("sftp", "nas.local:+22", null, null, R.string.address_port_bad, "+22");
    address("sftp", "nas.local:22 22", null, null, R.string.address_port_bad, "22 22");
    address("sftp", "nas.local:0", null, null, R.string.address_port_range, "0");
    address("sftp", "nas.local:65536", null, null, R.string.address_port_range, "65536");
    address("sftp", "nas.local:99999999999", null, null, R.string.address_port_range, "99999999999");
    address("sftp", "[2001:db8::1]:x", null, null, R.string.address_port_bad, "x");
    // The kind's own links, without a folder.
    address("sftp", "sftp://nas.local:2222", "nas.local", "2222", 0, null);
    address("sftp", "SFTP://nas.local/", "nas.local", null, 0, null);
    address("sftp", "ssh://[2001:db8::1]:2222", "2001:db8::1", "2222", 0, null);
    address("smb", "smb://nas.local", "nas.local", null, 0, null);
    address("ftp", "ftp://ftp.example.com:21", "ftp.example.com", "21", 0, null);
    // A folder, a user name or another kind's link: said, never looked up as a name.
    address("sftp", "sftp://nas.local:22/srv/files", null, null, R.string.address_folder, "/srv/files");
    address("smb", "smb://nas.local/share", null, null, R.string.address_folder, "/share");
    address("smb", "nas.local/share", null, null, R.string.address_folder, "/share");
    // SMB's Windows form: the name is understood, a share is pointed out as typed.
    address("smb", "\\\\nas", "nas", null, 0, null);
    address("smb", "\\\\nas.local\\", "nas.local", null, 0, null);
    address("smb", "\\\\nas\\share", null, null, R.string.address_folder, "\\share");
    address("smb", "\\\\192.0.2.10\\share\\photos", null, null, R.string.address_folder, "\\share\\photos");
    address("smb", "nas\\share", null, null, R.string.address_folder, "\\share");
    address("sftp", "https://cloud.example.com/dav", null, null, R.string.address_scheme, "https://");
    address("sftp", "smb://nas.local", null, null, R.string.address_scheme, "smb://");
    address("nfs", "ftp://nas.local", null, null, R.string.address_scheme, "ftp://");
    address("sftp", "driveway@nas", null, null, R.string.address_user, "driveway@");
    address("sftp", "sftp://driveway@nas:2222", null, null, R.string.address_user, "driveway@");
  }

  /** Every service the engine lists, as the app builds it. */
  private static void realCatalog() throws Exception {
    JSONArray engine = new JSONArray(Engine.backends());
    List<Kinds.Kind> others = Kinds.others();

    // Every service but the four hand-made ones, once each, with the engine's fields in order.
    Set<String> want = new HashSet<>(), mainSeen = new HashSet<>();
    boolean fieldsMatch = true;
    for (int i = 0; i < engine.length(); i++) {
      JSONObject b = engine.getJSONObject(i);
      String name = b.getString("name");
      if (Kinds.main(name) != null) {
        mainSeen.add(name);
        continue;
      }
      want.add(name);
      List<String> keys = new ArrayList<>();
      for (String list : new String[] {"essentials", "options"}) {
        JSONArray a = b.optJSONArray(list);
        for (int j = 0; a != null && j < a.length(); j++) {
          keys.add(a.getJSONObject(j).optString(list.equals("essentials") ? "key" : "name"));
        }
      }
      List<String> got = new ArrayList<>();
      Kinds.Kind k = Kinds.other(name);
      if (k != null) for (Kinds.Field f : k.fields) got.add(f.key);
      if (!keys.equals(got)) {
        fieldsMatch = false;
        System.out.println("  " + name + ": engine " + keys + ", app " + got);
      }
    }
    Set<String> got = new HashSet<>();
    for (Kinds.Kind k : others) got.add(k.backend);
    check(want.size() > 40 && got.equals(want) && others.size() == want.size(),
        "others(): every service in the engine's catalog but the hand-made four (" + want.size() + ")");
    check(mainSeen.size() == 4 && Kinds.other("sftp") == null && Kinds.other("webdav") == null,
        "the engine lists the hand-made four; others() leaves them out");
    check(fieldsMatch, "each service's fields: its essentials, then its other settings, in the engine's order");
    check(Kinds.others() == others, "the list is read once");

    // Sorted by label, ignoring case (the engine sorts by rclone name).
    boolean sorted = true;
    for (int i = 1; i < others.size(); i++) {
      if (others.get(i - 1).label.compareToIgnoreCase(others.get(i).label) > 0) sorted = false;
    }
    check(sorted && others.get(0).backend.equals("fichier"), "sorted by label: 1Fichier first, not azureblob");

    // Labels and subtitles.
    check(Kinds.other("s3").label.equals("S3 storage") && Kinds.other("s3").blurb.equals("Amazon S3 and services that work like it"),
        "S3: the form's label and blurb");
    check(Kinds.other("azureblob").blurb.equals("Microsoft Azure Blob Storage"),
        "no blurb: rclone's description, where it differs from the label");
    check(Kinds.other("ftp").blurb.isEmpty(), "no blurb and a description that is the label: none");
    check(Kinds.other("s3").folderHint.startsWith("A bucket") && Kinds.other("ftp").folderHint == null,
        "folder hint where the top is buckets, none elsewhere");
    check(Kinds.readOnly("http") && Kinds.readOnly("doi") && !Kinds.readOnly("s3") && !Kinds.readOnly("sftp")
        && !Kinds.readOnly("no-such-service"), "read-only: a web folder and DOI datasets, nothing else");

    // Essentials.
    Kinds.Field ftpUser = field(Kinds.other("ftp"), "user"), ftpPass = field(Kinds.other("ftp"), "pass");
    check(ftpUser.type == Kinds.Type.TEXT && ftpUser.required && !ftpUser.advanced && ftpUser.fallback.isEmpty()
        && ftpUser.label.equals("User name") && ftpUser.hint.startsWith("anonymous"),
        "FTP user: required text, without rclone's default (the device's account)");
    check(ftpPass.type == Kinds.Type.PASSWORD && ftpPass.masked && !ftpPass.required, "FTP password: a masked password");
    Kinds.Field provider = field(Kinds.other("s3"), "provider");
    check(provider.type == Kinds.Type.CHOICE && provider.fallback.equals("AWS") && provider.choices.contains("Wasabi"),
        "S3 provider: a choice starting at its first example");
    Kinds.Field endpoint = field(Kinds.other("s3"), "endpoint");
    check(endpoint.type == Kinds.Type.TEXT && endpoint.fallback.isEmpty() && !endpoint.choices.isEmpty(),
        "S3 endpoint: examples without choice stay text, with no default");
    Kinds.Field koofr = field(Kinds.other("koofr"), "provider");
    check(koofr.type == Kinds.Type.CHOICE && koofr.fallback.equals("koofr")
        && koofr.choices.equals(Arrays.asList("koofr", "digistorage", "other")), "Koofr: its services as a choice");
    Kinds.Field icloud = field(Kinds.other("iclouddrive"), "service");
    check(icloud.type == Kinds.Type.CHOICE && icloud.fallback.equals("drive"), "iCloud: the choice's own default");
    Kinds.Field b2Key = field(Kinds.other("b2"), "key");
    check(b2Key.type == Kinds.Type.TEXT && b2Key.masked, "a secret that isn't a password: text, masked");
    check(!field(Kinds.other("b2"), "account").masked, "and the account beside it isn't masked");
    Kinds.Field boxFile = field(Kinds.other("box"), "config_credentials");
    check(boxFile.type == Kinds.Type.FILE && boxFile.required, "Box: an app settings file");
    check(field(Kinds.other("drive"), "root_folder_id").link.equals("drive-folder")
        && field(Kinds.other("drive"), "service_account_credentials").link == null, "Drive: the shared folder takes a link");
    Kinds.Field token = field(Kinds.other("jottacloud"), "config_login_token");
    check(token.ephemeral && token.masked && !field(Kinds.other("filen"), "email").ephemeral,
        "Jottacloud's login token is used once, and masked");

    // More settings.
    rcloneHintsFollowTheWordingRules();
    Kinds.Field chunk = field(Kinds.other("s3"), "chunk_size");
    check(chunk.label.equals("Chunk size") && chunk.hint.equals("Chunk size to use for uploading")
        && chunk.fallback.equals("5Mi") && chunk.advanced && !chunk.required && chunk.type == Kinds.Type.TEXT,
        "a setting under More: its name as a label, help's first line, rclone's default");
    Kinds.Field filePass = field(Kinds.other("fichier"), "file_password");
    check(filePass.type == Kinds.Type.PASSWORD && filePass.masked && filePass.advanced, "a password under More is masked");

    // How each is set up.
    Kinds.Kind dropbox = Kinds.other("dropbox");
    check(Kinds.SIGN_IN.equals(dropbox.method) && dropbox.signIn && !dropbox.borrowedSignIn
        && dropbox.ownClientId != null && !dropbox.ownClientId.isEmpty() && dropbox.alt == null,
        "Dropbox: our own browser sign-in");
    Kinds.Kind box = Kinds.other("box");
    check(Kinds.ALTERNATIVE.equals(box.method) && box.signIn && box.borrowedSignIn && box.ownClientId == null
        && box.alt != null && box.alt.with.contains("settings file")
        && !box.alt.how.isEmpty() && box.alt.row.equals("Connect with an app settings file") && !box.alt.makesToken
        && "enterprise".equals(box.prefill.get("box_sub_type")) && !box.skipSetup,
        "Box: the alternative, its words, and the setting it starts with");
    check(Kinds.other("jottacloud").alt.makesToken, "Jottacloud's alternative trades its token");
    check(Kinds.other("premiumizeme").skipSetup && Kinds.other("premiumizeme").fields.size() == 1,
        "premiumize.me: connects without the service's setup (no other settings)");
    Kinds.Kind pcloud = Kinds.other("pcloud");
    check(Kinds.UNAVAILABLE.equals(pcloud.method) && Kinds.HIDDEN.equals(pcloud.state) && pcloud.signIn && pcloud.borrowedSignIn,
        "pCloud: a borrowed sign-in, so no way in, and hidden");
    check(Kinds.LISTED.equals(Kinds.other("ftp").state) && Kinds.FORM.equals(Kinds.other("ftp").method)
        && Kinds.other("ftp").prefill.isEmpty() && Kinds.other("ftp").since == null, "FTP: a listed form");
  }

  /** What the real catalog doesn't show: defaults, Since, odd entries, a broken catalog. */
  private static void madeUpCatalogs() throws Exception {
    catalog("[{\"name\":\"t-bare\"},"
        + "{\"name\":\"t-described\",\"description\":\"A described service\"},"
        + "{\"name\":\"t-new\",\"label\":\"beta\",\"description\":\"Beta, the long name\",\"state\":\"hidden\","
        + "\"since\":\"0.2.5\",\"method\":\"unavailable\"},"
        + "{\"name\":\"sftp\",\"label\":\"Hand-made\"},"
        + "{\"name\":\"t-nowayin\",\"label\":\"No way in\",\"method\":\"unavailable\",\"state\":\"listed\"},"
        + "{\"name\":\"t-hiddenform\",\"label\":\"Hidden form\",\"method\":\"form\",\"state\":\"hidden\"},"
        + "{\"name\":\"t-alpha\",\"label\":\"Alpha\",\"essentials\":["
        + "  {\"key\":\"pick\",\"choice\":true},"
        + "  {\"key\":\"pick2\",\"choice\":true,\"examples\":[\"x\",\"y\"],\"default\":\"y\"},"
        + "  {\"key\":\"keyfile\",\"file\":\"json\",\"password\":true,\"choice\":true,\"examples\":[\"z\"]},"
        + "  {\"key\":\"pw\",\"password\":true,\"label\":\"Word\",\"hint\":\"Shh\"}],"
        + "\"options\":["
        + "  {\"name\":\"client_secret\",\"help\":\"OAuth Client Secret.\\nLeave blank normally.\"},"
        + "  {\"name\":\"region\",\"help\":\"Where.\\nMore about it.\",\"default\":\"eu\"},"
        + "  {\"name\":\"nohelp\"},"
        + "  {\"name\":\"key_password\",\"password\":true}],"
        + "\"prefill\":{\"a\":\"1\",\"b\":\"2\"}}]");
    List<Kinds.Kind> got = Kinds.others();
    List<String> order = new ArrayList<>();
    for (Kinds.Kind k : got) order.add(k.label);
    check(order.equals(Arrays.asList("A described service", "Alpha", "beta", "Hidden form", "No way in", "t-bare")),
        "made up: sorted ignoring case, the hand-made name left out (" + order + ")");

    Kinds.Kind bare = Kinds.other("t-bare");
    check(bare.label.equals("t-bare") && bare.blurb.isEmpty() && Kinds.FORM.equals(bare.method)
        && Kinds.LISTED.equals(bare.state) && bare.since == null && bare.alt == null && bare.ownClientId == null
        && bare.folderHint == null && !bare.signIn && !bare.borrowedSignIn && !bare.skipSetup && bare.fields.isEmpty()
        && bare.prefill.isEmpty() && bare.note == null, "a bare entry: its name, a listed form, nothing else");
    Kinds.Kind described = Kinds.other("t-described");
    check(described.label.equals("A described service") && described.blurb.isEmpty(),
        "no label: the description, and no blurb repeating it");
    Kinds.Kind fresh = Kinds.other("t-new");
    check("0.2.5".equals(fresh.since) && Kinds.HIDDEN.equals(fresh.state) && fresh.blurb.equals("Beta, the long name"),
        "Since and state are read");
    check(!Kinds.HIDDEN.equals(Kinds.other("t-nowayin").state) && !Kinds.offered(Kinds.of("t-nowayin")),
        "listed by mistake with no way in: still not offered, so not shown");
    check(Kinds.HIDDEN.equals(Kinds.other("t-hiddenform").state) && !Kinds.offered(Kinds.of("t-hiddenform")),
        "a hidden form: not offered");

    Kinds.Kind a = Kinds.other("t-alpha");
    Kinds.Field pick = field(a, "pick"), pick2 = field(a, "pick2"), keyfile = field(a, "keyfile"), pw = field(a, "pw");
    check(pick.type == Kinds.Type.TEXT && pick.fallback.isEmpty() && pick.label.isEmpty() && pick.hint.isEmpty()
        && !pick.required, "a choice with no examples is text");
    check(pick2.type == Kinds.Type.CHOICE && pick2.fallback.equals("y") && pick2.choices.equals(Arrays.asList("x", "y")),
        "a choice with a default starts there");
    check(keyfile.type == Kinds.Type.FILE && keyfile.fallback.isEmpty() && !keyfile.masked,
        "a file wins over password and choice (its contents aren't typed, so not masked)");
    check(pw.type == Kinds.Type.PASSWORD && pw.masked && pw.label.equals("Word") && pw.hint.equals("Shh")
        && pw.link == null && !pw.ephemeral, "a password essential: masked, with its label and hint");
    Kinds.Field secret = field(a, "client_secret"), region = field(a, "region"), nohelp = field(a, "nohelp"),
        keyPw = field(a, "key_password");
    check(secret.label.equals("Client secret of your own app") && secret.hint.equals("Leave empty to use the usual sign-in"),
        "client_ settings: plain words instead of rclone's");
    check(region.hint.equals("Where") && region.fallback.equals("eu") && region.advanced, "help's first line only, without its period");
    check(nohelp.hint.isEmpty() && nohelp.fallback.isEmpty(), "no help: no hint");
    check(keyPw.type == Kinds.Type.PASSWORD && keyPw.masked && keyPw.label.equals("Key password"),
        "a password setting under More");
    check(a.prefill.size() == 2 && "1".equals(a.prefill.get("a")) && "2".equals(a.prefill.get("b")), "prefill is read");
    check(a.fields.indexOf(pw) == 3 && a.fields.indexOf(secret) == 4, "essentials first, then the rest");

    // A broken entry stops the list there; the ones before it stay, and it is logged.
    android.util.Log.lastTag = null;
    catalog("[{\"name\":\"t-ok\"},{\"description\":\"no name\"},{\"name\":\"t-after\"}]");
    check(Kinds.others().size() == 1 && Kinds.other("t-ok") != null && Kinds.other("t-after") == null,
        "a broken entry: the services before it are kept");
    check("Driveway".equals(android.util.Log.lastTag) && android.util.Log.lastMessage.startsWith("can't list services\norg.json.JSONException"),
        "and it is logged, with the exception");
    android.util.Log.lastTag = null;
    catalog("not json");
    check(Kinds.others().isEmpty() && "Driveway".equals(android.util.Log.lastTag), "a broken catalog: no services, logged");

    // Back to the real one for what follows.
    catalog(null);
  }

  private static void names() {
    check(Kinds.sentence("chunk_size").equals("Chunk size"), "sentence: chunk_size");
    check(Kinds.sentence("client_id").equals("Client ID of your own app"), "sentence: client_id");
    check(Kinds.sentence("client_secret").equals("Client secret of your own app"), "sentence: client_secret");
    check(Kinds.sentence("id_url_api_acl_sse_kms_tls_md5_2fa_oci_sas_uid_gid_http_http2_jwt_ftp")
        .equals("ID URL API ACL SSE KMS TLS MD5 2FA OCI SAS UID GID HTTP HTTP2 JWT FTP"), "sentence: initialisms in capitals");
    check(Kinds.sentence("upload__cutoff_").equals("Upload cutoff"), "sentence: empty words dropped");
    check(Kinds.sentence("").isEmpty() && Kinds.sentence("_").isEmpty(), "sentence: nothing stays nothing");
    check(Kinds.sentence("sse_customer_key").equals("SSE customer key"), "sentence: only the first letter is raised");

    check(Kinds.main("sftp") == Kinds.SFTP && Kinds.main("smb") == Kinds.SMB && Kinds.main("nfs") == Kinds.NFS
        && Kinds.main("webdav") == Kinds.WEBDAV && Kinds.main("ftp") == null, "main: the hand-made four only");
    check(Kinds.of("nfs") == Kinds.NFS && Kinds.of("nfs").label.equals("NFS") && Kinds.of("dropbox") == Kinds.other("dropbox")
        && Kinds.of("t-nothing") == null, "of: hand-made first, then the engine's");
    check(Kinds.label("sftp").equals("SFTP") && Kinds.label("dropbox").equals("Dropbox")
        && Kinds.label("crypt").equals("Encrypted folder (crypt)") && Kinds.label("t-unknown").equals("t-unknown"),
        "label: hand-made, engine, removed, else the rclone name");

    boolean noneInEngine = true;
    for (String r : Kinds.REMOVED.keySet()) if (Kinds.of(r) != null) noneInEngine = false;
    check(noneInEngine && Kinds.REMOVED.size() == 11, "the removed services aren't in the engine's catalog");
    check(Kinds.removed("google photos") && !Kinds.layered("google photos") && Kinds.removed("sharefile")
        && !Kinds.layered("sharefile"), "Google Photos and ShareFile: removed, not layered");
    check(Kinds.removed("crypt") && Kinds.layered("crypt") && Kinds.layered("alias") && Kinds.layered("union"),
        "crypt, alias, union: removed and layered");
    check(!Kinds.removed("dropbox") && !Kinds.layered("dropbox") && !Kinds.layered("t-unknown"), "others: neither");
  }

  private static void listing() {
    check(Kinds.HIDDEN.equals(Kinds.other("pcloud").state) && !Kinds.offered(Kinds.of("pcloud")),
        "pCloud: no way in, hidden, not offered");
    check(Kinds.offered(Kinds.of("dropbox")),
        "Dropbox: offered");
    check(Kinds.offered(Kinds.of("box")), "Box: offered through its alternative");
    check(Kinds.offered(Kinds.SFTP), "SFTP: hand-made, offered");
    check(!Kinds.offered(null), "an unknown service: none");

    Kinds.Kind noWayIn = kind(Kinds.UNAVAILABLE, true, null);
    check(!Kinds.offered(noWayIn), "a listed service with no way in isn't offered");
    Kinds.Kind hiddenForm = kind(Kinds.FORM, false, null);
    hiddenForm.state = Kinds.HIDDEN;
    check(!Kinds.offered(hiddenForm), "a hidden form isn't offered");
    check(Kinds.offered(kind(Kinds.FORM, false, null)), "a listed form is");
    Kinds.Kind sftpHidden = new Kinds.Kind("sftp", "SFTP", "", null);
    sftpHidden.state = Kinds.HIDDEN;
    check(Kinds.offered(sftpHidden), "a hand-made kind always is");
  }

  private static void signIn() throws Exception {
    JSONObject token = params("token", "{}"), none = new JSONObject();

    // browser()
    check(!Kinds.browser(null, token), "browser: no kind");
    check(!Kinds.browser(kind(Kinds.SIGN_IN, false, null), token), "browser: a service without browser sign-in");
    check(Kinds.browser(kind(Kinds.SIGN_IN, true, "id"), null) && Kinds.browser(kind(Kinds.SIGN_IN, true, "id"), none),
        "browser: offered that way, whatever the server has");
    check(!Kinds.browser(kind(Kinds.SIGN_IN, true, "id"), params("config_credentials", "{}", "box_sub_type", "enterprise")),
        "browser: not a server set up with an app's settings file (Box before one-tap sign-in)");

    // oneTap() and the cloud list at the start of Add a server
    check(Kinds.oneTap(kind(Kinds.SIGN_IN, true, "id")), "oneTap: a sign-in service");
    Kinds.Kind hiddenSignIn = kind(Kinds.SIGN_IN, true, "id");
    hiddenSignIn.state = Kinds.HIDDEN;
    check(!Kinds.oneTap(hiddenSignIn) && !Kinds.oneTap(null), "oneTap: not hidden, not missing");
    check(!Kinds.oneTap(kind(Kinds.ALTERNATIVE, true, null)) && !Kinds.oneTap(kind(Kinds.FORM, false, null)),
        "oneTap: not an alternative or a form");
    check(!Kinds.CLOUD.contains("drive"), "the cloud list leaves Google Drive to More services (a key, not one tap)");
    check(Kinds.oneTap(Kinds.of("dropbox")) && Kinds.oneTap(Kinds.of("onedrive")), "real: Dropbox and OneDrive are one tap");
    check(!Kinds.oneTap(Kinds.of("drive")), "real: Google Drive is not");
    check(Kinds.CLOUD.equals(java.util.Arrays.asList("box", "dropbox", "onedrive")), "the cloud list is A to Z");
    check("B".equals(Kinds.letter("box")) && "D".equals(Kinds.letter("dropbox")) && "O".equals(Kinds.letter("onedrive")),
        "Box, Dropbox and OneDrive get letter tiles (no company logos: docs/BRAND-MARKS.md)");
    check(Kinds.letter("drive") == null && Kinds.letter("sftp") == null && Kinds.letter(null) == null,
        "every other service keeps its glyph");
    Kinds.Field cred = text("api_key");
    Kinds.Kind alt = kind(Kinds.ALTERNATIVE, true, null, cred);
    alt.alt = alt(false);
    check(Kinds.browser(alt, token), "browser: an alternative, but signed in through the browser before (a token)");
    check(!Kinds.browser(alt, params("token", "{}", "api_key", "k")), "browser: not with the alternative's credential");
    check(!Kinds.browser(alt, none) && !Kinds.browser(alt, null), "browser: not without a token");
    Kinds.Kind trades = kind(Kinds.ALTERNATIVE, true, null, text("login_token"));
    trades.alt = alt(true);
    check(!Kinds.browser(trades, token), "browser: not when the alternative makes the token (Jottacloud)");
    Kinds.Kind gone = kind(Kinds.UNAVAILABLE, true, null);
    check(Kinds.browser(gone, token) && !Kinds.browser(gone, none), "browser: no way in, no alternative: the token");

    // oldSignIn()
    Kinds.Kind own = kind(Kinds.SIGN_IN, true, "our-id");
    check(Kinds.oldSignIn(own, token), "oldSignIn: a token and no client ID of its own");
    check(!Kinds.oldSignIn(own, params("token", "{}", "client_id", "our-id")), "oldSignIn: not once it has ours");
    check(Kinds.oldSignIn(own, params("token", "{}", "client_id", "")), "oldSignIn: an empty client ID is none");
    check(!Kinds.oldSignIn(own, params("token", "{}", "drive_type", "documentLibrary")),
        "oldSignIn: a SharePoint library keeps rclone's");
    check(Kinds.oldSignIn(own, params("token", "{}", "drive_type", "personal")), "oldSignIn: another drive type doesn't");
    check(!Kinds.oldSignIn(own, none) && !Kinds.oldSignIn(own, null), "oldSignIn: never without a token");
    check(!Kinds.oldSignIn(kind(Kinds.SIGN_IN, true, null), token), "oldSignIn: not for a service without our registration");
    check(!Kinds.oldSignIn(kind(Kinds.SIGN_IN, false, "our-id"), token), "oldSignIn: not without browser sign-in");
    check(!Kinds.oldSignIn(null, token), "oldSignIn: no kind");

    // signsInAgain(): only a sign-in of Driveway's own, never one that would borrow rclone's.
    check(Kinds.signsInAgain(own, token) && Kinds.signsInAgain(own, params("token", "{}", "client_id", "our-id")),
        "signsInAgain: our own sign-in, signed in as rclone before or as Driveway");
    check(!Kinds.signsInAgain(alt, token), "signsInAgain: not a token from before Driveway stopped offering its sign-in");
    check(!Kinds.signsInAgain(gone, token), "signsInAgain: not a service with no way in");
    check(!Kinds.signsInAgain(kind(Kinds.SIGN_IN, false, "id"), token), "signsInAgain: not without browser sign-in");
    check(!Kinds.signsInAgain(null, token), "signsInAgain: no kind");
    for (String b : new String[] {"dropbox", "onedrive"}) {
      check(Kinds.signsInAgain(Kinds.of(b), token) && Kinds.signsInAgain(Kinds.of(b), none),
          "real: " + b + " signs in again (Driveway's own sign-in)");
      check(Kinds.oldSignIn(Kinds.of(b), token) && Kinds.signsInAgain(Kinds.of(b), token),
          "real: " + b + " signed in as rclone before still signs in again");
    }
    for (String b : new String[] {"drive", "google cloud storage", "box", "premiumizeme", "pcloud", "jottacloud"}) {
      check(Kinds.of(b) != null && !Kinds.signsInAgain(Kinds.of(b), token) && !Kinds.signsInAgain(Kinds.of(b), none),
          "real: " + b + " with a token from before never signs in again");
    }
    check(Kinds.browser(Kinds.of("drive"), token) && Kinds.browser(Kinds.of("pcloud"), token),
        "real: those servers still sign in with their token");

    // changeRestores(): Change can bring back a server whose sign-in ended
    // only where the form has another way in (a key file, an API key).
    for (String b : new String[] {"drive", "google cloud storage", "box", "premiumizeme", "jottacloud"}) {
      check(Kinds.changeRestores(Kinds.of(b)), "real: " + b + "'s form has another way in");
    }
    for (String b : new String[] {"pcloud", "hidrive", "yandex", "zoho", "putio", "huaweidrive", "dropbox", "sftp"}) {
      check(!Kinds.changeRestores(Kinds.of(b)), "real: " + b + " has no other way in on its form");
    }
    check(!Kinds.changeRestores(null), "changeRestores: no kind");

    // altPresent()
    check(Kinds.altPresent(alt, params("api_key", "k")), "altPresent: the first field has a value");
    check(!Kinds.altPresent(alt, params("api_key", "")) && !Kinds.altPresent(alt, params("other", "k")),
        "altPresent: empty or missing");
    check(!Kinds.altPresent(alt, null) && !Kinds.altPresent(null, params("api_key", "k")), "altPresent: nothing to look at");
    check(!Kinds.altPresent(kind(Kinds.ALTERNATIVE, true, null, text("api_key")), params("api_key", "k")),
        "altPresent: a service without an alternative");
    Kinds.Kind empty = kind(Kinds.ALTERNATIVE, true, null);
    empty.alt = alt(false);
    check(!Kinds.altPresent(empty, params("api_key", "k")), "altPresent: an alternative with no fields");
    Kinds.Field once = text("login_token");
    once.ephemeral = true;
    Kinds.Kind onceKind = kind(Kinds.ALTERNATIVE, true, null, once);
    onceKind.alt = alt(true);
    check(!Kinds.altPresent(onceKind, params("login_token", "t")), "altPresent: a used-once credential doesn't count");

    // required()
    Kinds.Field must = new Kinds.Field("api_key", "", "", Kinds.Type.TEXT, true, false, "");
    Kinds.Field extra = new Kinds.Field("region", "", "", Kinds.Type.TEXT, true, false, "");
    Kinds.Kind altMust = kind(Kinds.ALTERNATIVE, true, null, must, extra);
    altMust.alt = alt(false);
    check(Kinds.required(altMust, must, none) && Kinds.required(altMust, must, null),
        "required: the alternative's credential, for a new server");
    check(!Kinds.required(altMust, must, token) && Kinds.required(altMust, extra, token),
        "required: not the credential for a server that signed in through the browser before; its other fields still");
    check(Kinds.required(altMust, must, params("token", "{}", "api_key", "k")),
        "required: the credential, once the server uses it");
    check(!Kinds.required(altMust, text("optional"), none), "required: never a field that isn't");
    Kinds.Field onceMust = new Kinds.Field("login_token", "", "", Kinds.Type.TEXT, true, false, "");
    onceMust.ephemeral = true;
    Kinds.Kind tradesMust = kind(Kinds.ALTERNATIVE, true, null, onceMust);
    tradesMust.alt = alt(true);
    check(Kinds.required(tradesMust, onceMust, none) && !Kinds.required(tradesMust, onceMust, token),
        "required: a single-use token until the service has given its own access");
    Kinds.Kind signInMust = kind(Kinds.SIGN_IN, true, "id", must);
    check(Kinds.required(signInMust, must, token), "required: no waiver without an alternative");
    for (String b : new String[] {"drive", "google cloud storage", "box", "premiumizeme"}) {
      Kinds.Kind k = Kinds.of(b);
      check(Kinds.required(k, k.fields.get(0), none) && !Kinds.required(k, k.fields.get(0), token),
          "real: " + b + "'s credential is required, except on a server that signed in through the browser before");
    }

    // The real ones.
    check(Kinds.browser(Kinds.of("dropbox"), none) && !Kinds.browser(Kinds.of("box"), params("config_credentials", "{}")),
        "real: Dropbox signs in in the browser; Box with its file doesn't");
    check(!Kinds.browser(Kinds.of("jottacloud"), token), "real: Jottacloud's token came from its login token");
  }

  /** What setup is given only once and never kept with the server. */
  private static void setupAnswers() throws Exception {
    JSONObject box = params("config_credentials", "{}", "box_sub_type", "enterprise", "token", "{}", "config_x", "y");
    Kinds.dropSetupAnswers(Kinds.of("box"), box);
    check(box.has("config_credentials") && box.has("box_sub_type") && box.has("token") && !box.has("config_x"),
        "Box keeps its app settings file (a setting named config_), not a setup answer: " + box.names());
    JSONObject jotta = params("config_login_token", "t", "config_type", "standard", "config_non_standard", "false",
        "token", "{}");
    Kinds.dropSetupAnswers(Kinds.of("jottacloud"), jotta);
    check(jotta.length() == 1 && jotta.has("token"), "Jottacloud: the single-use login token and the presets go");
    JSONObject drive = params("config_change_team_drive", "true", "service_account_credentials", "{}", "team_drive", "0A");
    Kinds.dropSetupAnswers(Kinds.of("drive"), drive);
    check(drive.length() == 2 && !drive.has("config_change_team_drive"), "Google Drive: the preset goes, the drive stays");
    JSONObject oracle = params("config_file", "~/.oci/config", "config_profile", "Default", "config_x", "y");
    Kinds.dropSetupAnswers(Kinds.of("oracleobjectstorage"), oracle);
    check(oracle.length() == 2 && oracle.has("config_file") && oracle.has("config_profile"),
        "Oracle: its config_file and config_profile settings stay");
    Kinds.Field once = text("login_token");
    once.ephemeral = true;
    JSONObject made = params("login_token", "t", "other", "o");
    Kinds.dropSetupAnswers(kind(Kinds.ALTERNATIVE, false, null, once, text("other")), made);
    check(made.length() == 1 && made.has("other"), "a field used once goes, whatever its name");
    JSONObject unknown = params("config_a", "1", "b", "2");
    Kinds.dropSetupAnswers(null, unknown);
    check(unknown.length() == 1 && unknown.has("b"), "no kind: every config_ answer goes");
  }

  private static void layout() {
    check(Kinds.tileText(Kinds.SFTP).equals("Computers you SSH into") && Kinds.tileText(Kinds.SMB).startsWith("Windows")
        && Kinds.tileText(Kinds.NFS).equals("Linux and NAS exports")
        && Kinds.tileText(Kinds.WEBDAV).equals("Nextcloud and web servers"), "tileText: the hand-made four");
    check(Kinds.tileText(Kinds.of("s3")).equals(Kinds.of("s3").blurb), "tileText: others show their blurb");

    Set<Integer> icons = new HashSet<>(Arrays.asList(Kinds.icon("sftp"), Kinds.icon("smb"), Kinds.icon("nfs"),
        Kinds.icon("webdav"), Kinds.icon("s3")));
    check(Kinds.icon("sftp") == R.drawable.ic_terminal && Kinds.icon("smb") == R.drawable.ic_folder
        && Kinds.icon("nfs") == R.drawable.ic_dns && Kinds.icon("webdav") == R.drawable.ic_language
        && icons.size() == 5, "icon: one for each hand-made kind");
    check(Kinds.icon("s3") == R.drawable.ic_cloud && Kinds.icon(null) == R.drawable.ic_cloud, "icon: a cloud otherwise");

    check(Kinds.signInField(new Kinds.Field("x", "", "", Kinds.Type.PASSWORD, false, false, ""))
        && Kinds.signInField(new Kinds.Field("x", "", "", Kinds.Type.KEY, false, false, ""))
        && Kinds.signInField(new Kinds.Field("x", "", "", Kinds.Type.FILE, false, false, "")),
        "signInField: passwords, keys and files");
    Kinds.Field masked = text("token_x");
    masked.masked = true;
    check(Kinds.signInField(masked), "signInField: anything masked");
    boolean names = true;
    for (String k : new String[] {"user", "username", "pass", "password", "access_key_id", "secret_access_key", "account", "key"}) {
      if (!Kinds.signInField(text(k))) names = false;
    }
    check(names, "signInField: the account and credential names");
    check(!Kinds.signInField(text("host")) && !Kinds.signInField(text("region"))
        && !Kinds.signInField(new Kinds.Field("x", "", "", Kinds.Type.CHOICE, false, false, ""))
        && !Kinds.signInField(new Kinds.Field("x", "", "", Kinds.Type.NUMBER, false, false, "")),
        "signInField: the rest belong to the server");
    check(!Kinds.SFTP.fields.get(0).masked && field(Kinds.SFTP, "pass").masked, "a hand-made password field is masked");
    check(eq(field(Kinds.SFTP, "port").fallback, "22") && field(Kinds.SFTP, "port").advanced, "SFTP's port: 22, under More");
  }

  /**
   * The hints under More settings come from rclone's help: shown without a
   * single sentence's closing period, and in sentence case, across the whole
   * real catalog.
   */
  private static void rcloneHintsFollowTheWordingRules() {
    check(Kinds.hint("Chunk size to use for uploading.\nMore here.").equals("Chunk size to use for uploading"),
        "hint: a single sentence loses its period");
    check(Kinds.hint("Cutoff for chunked upload. Larger files go in chunks.").equals("Cutoff for chunked upload. Larger files go in chunks."),
        "hint: two sentences keep theirs");
    check(Kinds.hint("Tags to add, e.g. \"a,b\".").equals("Tags to add, e.g. \"a,b\""), "hint: e.g. doesn't end a sentence");
    check(Kinds.hint("User name (usually an email address)").equals("User name (usually an email address)"), "hint: no period, as it is");
    check(Kinds.hint("Storage Connection String.").equals("Storage connection string"), "hint: rclone's Title Case in sentence case");
    check(Kinds.hint("Wait...").equals("Wait..."), "hint: an ellipsis stays");
    check(Kinds.hint("").isEmpty(), "hint: none");
    check(Kinds.hint("The API endpoint to connect to. It's fine to leave\nthis at default. Only for testing.")
        .equals("The API endpoint to connect to. It's fine to leave this at default."),
        "hint: a sentence that goes on to the next line is finished there");
    check(Kinds.hint("A key associated\nwith  the object, e.g. a file. More.").equals("A key associated with the object, e.g. a file"),
        "hint: finished at the first sentence end on the next line, not at e.g.");
    check(Kinds.hint("Choose one:\n1) this").equals("Choose one:") && Kinds.hint("Mid sentence\n\nNext paragraph").equals("Mid sentence"),
        "hint: a list or a new paragraph isn't joined");
    java.util.Set<String> seen = new HashSet<>();
    List<String> periods = new ArrayList<>(), titleCase = new ArrayList<>();
    for (Kinds.Kind k : Kinds.others()) {
      for (Kinds.Field f : k.fields) {
        if (!f.advanced || f.hint.isEmpty()) continue;
        seen.add(f.hint);
        java.util.Map<StringStyleTest.Rule, String> v = StringStyleTest.violations(f.hint);
        if (v.containsKey(StringStyleTest.Rule.SINGLE_SENTENCE_PERIOD)) periods.add(k.backend + "." + f.key + ": " + f.hint);
        String[] words = f.hint.replaceAll("\\(.*\\)", "").trim().split("\\s+");
        boolean title = words.length >= 2;
        boolean anyLower = false;
        for (int i = 1; i < words.length; i++) {
          String w = words[i];
          if (w.isEmpty() || !Character.isLetter(w.charAt(0))) continue;
          if (!Character.isUpperCase(w.charAt(0))) title = false;
          else if (!w.equals(w.toUpperCase(java.util.Locale.ROOT))) anyLower = true;
        }
        if (title && anyLower) titleCase.add(k.backend + "." + f.key + ": " + f.hint);
      }
    }
    // Driveway's own words for each kind (the four main kinds, and the
    // labels, blurbs and settings the engine's catalog curates) follow every
    // wording rule.
    List<String> own = new ArrayList<>();
    List<Kinds.Kind> kinds = new ArrayList<>(Kinds.MAIN);
    kinds.addAll(Kinds.others());
    for (Kinds.Kind k : kinds) {
      List<String> texts = new ArrayList<>(Arrays.asList(k.label, k.blurb, k.note, k.folderHint));
      for (Kinds.Field f : k.fields) if (!f.advanced) texts.addAll(Arrays.asList(f.label, f.hint));
      for (String t : texts) {
        if (t == null || t.isEmpty()) continue;
        java.util.Map<StringStyleTest.Rule, String> v = StringStyleTest.violations(t);
        if (!v.isEmpty()) own.add(k.backend + ": " + v.keySet() + " " + t);
      }
    }
    check(own.isEmpty(), "every kind's own label, blurb, note and setting follows the wording rules " + own);
    check(seen.size() > 300, "the real catalog's hints are read (" + seen.size() + ")");
    java.util.Map<String, String> finished = new java.util.HashMap<>();
    for (Kinds.Kind k : Kinds.others()) for (Kinds.Field f : k.fields) finished.put(k.backend + "." + f.key, f.hint);
    check(String.valueOf(finished.get("oracleobjectstorage.sse_customer_key_file")).endsWith("key associated with the object")
        && String.valueOf(finished.get("pixeldrain.api_url")).endsWith("fine to leave this at default.")
        && String.valueOf(finished.get("ulozto.root_folder_slug")).endsWith("without any root slug set."),
        "hints that went on to a second line end where their sentence ends");
    check(periods.isEmpty(), "no single-sentence hint ends in a period " + periods);
    check(titleCase.isEmpty(), "no hint is in Title Case " + titleCase);
    for (String from : Kinds.sentenceCaseKeys()) {
      check(seen.contains(Kinds.hint(from)), "the sentence-case fix is still needed by the catalog: " + from);
    }
  }
}
