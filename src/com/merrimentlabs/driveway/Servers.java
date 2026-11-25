// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.provider.DocumentsContract;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;

import com.merrimentlabs.driveway.engine.Engine;
import com.merrimentlabs.driveway.engine.Host;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.StandardCopyOption;
import java.security.KeyStore;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.UUID;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * The servers you've added, with their passwords and keys. Kept in one file
 * encrypted with an AES key that lives in the Android Keystore, so the file
 * alone (a backup, another app) reveals nothing. Every server is also handed
 * to the engine, which holds it in memory only.
 */
final class Servers {
  private static final String KEY_ALIAS = "driveway-servers";
  static final String AUTHORITY = "com.merrimentlabs.driveway.documents";

  /** One server. {@code params} are the engine's settings for {@code backend}. */
  static final class Server {
    String id;
    String label;
    String backend;
    JSONObject params = new JSONObject();
    /** Where the server's top folder starts, e.g. "share/photos" ("" for the very top). */
    String folder = "";

    String summary() {
      String host = params.optString("host", "");
      if (host.isEmpty()) host = Uri.parse(params.optString("url", "")).getHost();
      String kind = Kinds.label(backend);
      return host == null || host.isEmpty() ? kind : kind + " · " + host;
    }

    JSONObject toJson() throws JSONException {
      // (A server being set up may have no name or kind yet: keep them as "".)
      return new JSONObject().put("id", id).put("label", label == null ? "" : label)
          .put("backend", backend == null ? "" : backend).put("params", params).put("folder", folder == null ? "" : folder);
    }

    static Server fromJson(JSONObject o) throws JSONException {
      Server s = new Server();
      s.id = o.getString("id");
      s.label = o.optString("label", "");
      s.backend = o.optString("backend", "");
      // A copy of its own: a screen's edits to a server mustn't reach the stored one.
      s.params = o.optJSONObject("params") != null ? new JSONObject(o.getJSONObject("params").toString()) : new JSONObject();
      s.folder = o.optString("folder", "");
      return s;
    }

    Server copy() {
      try {
        return fromJson(toJson());
      } catch (JSONException e) {
        throw new IllegalStateException(e);
      }
    }
  }

  private static Servers instance;

  /** The sign-in page opened last, for "Open the page again". */
  static volatile String lastSignInUrl;

  /**
   * Why the current sign-in's page couldn't open, or null: "background"
   * (Driveway wasn't on screen, and Android doesn't let an app open the
   * browser from the background) or "nobrowser" (no app opens web pages).
   * Cleared when a sign-in starts; the sign-in then ends at once with that
   * reason instead of waiting ten minutes for a page that never opened.
   */
  static volatile String browserProblem;

  /** Whether one of Driveway's windows is on screen, so Android lets it open the browser. */
  static boolean onScreen() {
    android.app.ActivityManager.RunningAppProcessInfo info = new android.app.ActivityManager.RunningAppProcessInfo();
    android.app.ActivityManager.getMyMemoryState(info);
    // Not FOREGROUND_SERVICE (125): the sign-in's own service doesn't count.
    return info.importance == android.app.ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
        || info.importance == android.app.ActivityManager.RunningAppProcessInfo.IMPORTANCE_VISIBLE;
  }

  static synchronized Servers get(Context context) {
    if (instance == null) instance = new Servers(context.getApplicationContext());
    return instance;
  }

  private final Context context;
  private final File file;
  private final List<Server> servers = new ArrayList<>();

  private Servers(Context context) {
    this.context = context;
    this.file = new File(context.getNoBackupFilesDir(), "servers.bin");
    load();
    // Keys trusted for edits that were never saved (Driveway was stopped
    // with the screen open) go back to the ones pinned before.
    File[] journals = new File(context.getNoBackupFilesDir(), "known_hosts").listFiles((d, n) -> n.endsWith(".saved"));
    for (int i = 0; journals != null && i < journals.length; i++) {
      String n = journals[i].getName();
      restorePin(n.substring(0, n.length() - ".saved".length()));
    }
    Engine.setHost(new Host() {
      @Override
      public void openURL(String url) {
        if (Connection.setupLeft()) {
          // The screen that asked for it was left before the page opened:
          // no page, and the sign-in ends now.
          Engine.configCancel();
          return;
        }
        // A service's sign-in page: the browser comes back to the engine on
        // http://127.0.0.1:53682 when the person has signed in.
        lastSignInUrl = url;
        if (!onScreen()) {
          // Android would drop the browser silently: end the wait now
          // rather than after ten minutes.
          Logs.w("can't open the sign-in page: Driveway isn't on screen");
          browserProblem = "background";
          Engine.configCancel();
          return;
        }
        Intent i = new Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        try {
          Servers.this.context.startActivity(i);
        } catch (RuntimeException e) {
          // Nothing will come back to the sign-in's server: end the wait now
          // rather than after ten minutes.
          Logs.w("can't open the browser: " + e);
          browserProblem = "nobrowser";
          Engine.configCancel();
        }
      }

      @Override
      public void saved(String id, String key, String value) {
        Servers.this.saved(id, key, value);
      }
    });
    for (Server s : servers) pushSaved(s);
  }

  /**
   * Hands a saved server to the engine when Driveway starts, or puts one
   * back. Nobody is at a form then: settings the engine refuses go on the
   * server's card (Problems.SETTINGS), and Files gets the engine's refusal,
   * which names the setting, until the server is saved again.
   */
  void pushSaved(Server s) {
    try {
      push(s);
      // Accepted now (a newer Driveway reads what an older one refused).
      if (Problems.settingsField(Problems.of(context, s.id)) != null) Problems.clear(context, s.id);
    } catch (Exception e) {
      Logs.w("can't set up " + s.label + ": " + e.getMessage());
      String field = Errors.refusedSetting(e);
      Problems.note(context, s.id, Problems.SETTINGS + (field == null ? "" : field));
    }
  }

  /**
   * The engine changed a setting by itself: a service refreshed its sign-in
   * token (some replace the refresh token every time, so it must be kept or
   * the next start would need a new sign-in).
   */
  synchronized void saved(String id, String key, String value) {
    for (Server s : servers) {
      if (!s.id.equals(id) || value.equals(s.params.optString(key, null))) continue;
      Kinds.Kind k = Kinds.of(s.backend);
      if (k != null) {
        for (Kinds.Field f : k.fields) if (f.key.equals(key) && f.type == Kinds.Type.PASSWORD) return; // kept in the clear by the app
      }
      try {
        s.params.put(key, value);
        write();
      } catch (Exception e) {
        // The new value stays in memory (the engine has it, and won't say it
        // again), so the next write of the list, for any change, keeps it.
        Logs.w("can't keep the new " + key + " for " + s.label + " yet: " + e);
      }
    }
  }

  static String newId() {
    return UUID.randomUUID().toString().substring(0, 8);
  }

  synchronized List<Server> all() {
    List<Server> out = new ArrayList<>();
    for (Server s : servers) out.add(s.copy());
    return Collections.unmodifiableList(out);
  }

  synchronized Server find(String id) {
    for (Server s : servers) if (s.id.equals(id)) return s.copy();
    return null;
  }

  /** Adds the server, or replaces the one with the same id. */
  synchronized void save(Server server) throws Exception {
    List<Server> before = new ArrayList<>(servers);
    push(server);
    Server copy = server.copy();
    boolean replaced = false;
    for (int i = 0; i < servers.size(); i++) {
      if (servers.get(i).id.equals(copy.id)) {
        servers.set(i, copy);
        replaced = true;
      }
    }
    if (!replaced) servers.add(copy);
    try {
      write();
    } catch (Exception e) {
      // Not kept: the list and the engine go back to how they were, so
      // nothing works now that a restart would undo.
      servers.clear();
      servers.addAll(before);
      Server old = null;
      for (Server s : servers) if (s.id.equals(server.id)) old = s;
      if (old != null) pushSaved(old);
      else Engine.removeRemote(server.id);
      throw e;
    }
    keepPin(server.id);
    // Saved after a change (a new sign-in, a trusted identity): its card
    // shows the server as fine until real use says otherwise. An SFTP server
    // saved before its identity was checked (Save anyway while it was off)
    // is refused until it is: its card says so at once, with Check identity.
    if (server.backend.equals("sftp") && knownHosts(server.id).length() == 0) {
      Problems.note(context, server.id, Problems.UNCONFIRMED);
    } else {
      Problems.clear(context, server.id);
    }
    changed();
  }

  synchronized void remove(String id) throws Exception {
    // The list is written first: if that fails, nothing else is touched
    // (its unsent saves, its pinned key), and the server stays as it was.
    List<Server> before = new ArrayList<>(servers);
    servers.removeIf(s -> s.id.equals(id));
    try {
      write();
    } catch (Exception e) {
      servers.clear();
      servers.addAll(before);
      throw e;
    }
    Uploads.forgetServer(context, id);
    Engine.removeRemote(id);
    knownHosts(id).delete();
    keepPin(id);
    Problems.clear(context, id);
    changed();
  }

  /**
   * The engine's settings for a server, with what only the app knows filled
   * in. Settings the engine refuses throw its error (which names the
   * setting, Errors.refusedSetting), and the engine keeps nothing of the
   * server's earlier settings: each caller says so where the person is.
   */
  void push(Server s) throws Exception {
    if (Kinds.removed(s.backend)) return; // not in the engine any more
    JSONObject p = new JSONObject(s.params.toString());
    if (s.backend.equals("sftp")) {
      // Pinned on first connect (ServerActivity's host key check); a server
      // whose key changes is refused. Until then the file is empty, so
      // every key is refused rather than any key accepted.
      File kh = knownHosts(s.id);
      if (!kh.exists()) {
        kh.createNewFile();
      }
      p.put("known_hosts_file", kh.getPath());
      // Plain SFTP only: no remote shell commands.
      p.put("shell_type", "none");
    }
    Engine.setRemote(s.id, s.backend, p.toString());
  }

  /** The pinned SSH host key for a server. */
  File knownHosts(String id) {
    File dir = new File(context.getNoBackupFilesDir(), "known_hosts");
    dir.mkdirs();
    return new File(dir, id);
  }

  /**
   * Pins a key trusted in a screen's Test or Connect, before the server is
   * saved. The key pinned before is kept beside it ("<id>.saved", empty if
   * there was none) until the server is saved (keepPin) or the screen goes
   * without saving (restorePin); if Driveway is stopped in between, the next
   * start puts it back, so a key trusted for an unsaved edit never outlives
   * it.
   */
  void pinForTest(String id, String line) throws IOException {
    File saved = new File(knownHosts(id).getPath() + ".saved");
    if (!saved.exists()) {
      File pin = knownHosts(id);
      byte[] old = pin.exists() ? Files.readAllBytes(pin.toPath()) : new byte[0];
      File tmp = new File(saved.getPath() + ".tmp");
      try (FileOutputStream out = new FileOutputStream(tmp)) {
        out.write(old);
        out.getFD().sync();
      }
      Files.move(tmp.toPath(), saved.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
    }
    pinHostKey(id, line);
  }

  /** The server was saved: the key it was tested with is its key now. */
  void keepPin(String id) {
    new File(knownHosts(id).getPath() + ".saved").delete();
  }

  /** The screen went without saving: the key pinned before is put back. */
  void restorePin(String id) {
    File saved = new File(knownHosts(id).getPath() + ".saved");
    if (!saved.exists()) return;
    try {
      byte[] old = Files.readAllBytes(saved.toPath());
      File pin = knownHosts(id);
      if (old.length == 0 && find(id) == null) {
        // A server never saved: nothing of it stays.
        pin.delete();
      } else {
        // An empty pin (a server saved before its identity was checked)
        // stays an empty file: the engine reads it, and refuses every key.
        File tmp = new File(pin.getPath() + ".tmp");
        try (FileOutputStream out = new FileOutputStream(tmp)) {
          out.write(old);
          out.getFD().sync();
        }
        Files.move(tmp.toPath(), pin.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
      }
      saved.delete();
    } catch (IOException e) {
      Logs.w("can't put back the key pinned before for " + id + ": " + e);
    }
  }

  void pinHostKey(String id, String line) throws IOException {
    File f = knownHosts(id);
    File tmp = new File(f.getPath() + ".tmp");
    try (FileOutputStream out = new FileOutputStream(tmp)) {
      out.write((line + "\n").getBytes(StandardCharsets.UTF_8));
      out.getFD().sync();
    }
    Files.move(tmp.toPath(), f.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
  }

  private void changed() {
    context.getContentResolver().notifyChange(DocumentsContract.buildRootsUri(AUTHORITY), null);
  }

  // Storage: [12-byte IV][AES-GCM ciphertext of the JSON array].

  private void load() {
    if (!file.exists()) return;
    try {
      byte[] all = Files.readAllBytes(file.toPath());
      ByteBuffer buf = ByteBuffer.wrap(all);
      byte[] iv = new byte[12];
      buf.get(iv);
      byte[] body = new byte[buf.remaining()];
      buf.get(body);
      Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
      c.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, iv));
      JSONArray arr = new JSONArray(new String(c.doFinal(body), StandardCharsets.UTF_8));
      for (int i = 0; i < arr.length(); i++) servers.add(Server.fromJson(arr.getJSONObject(i)));
    } catch (Exception e) {
      // Keep the unreadable file for a later version to try; start empty.
      // Only the exception's type: org.json puts the text it couldn't read,
      // the servers' passwords and keys, in its message.
      Logs.e("can't read the server list (" + e.getClass().getName() + ")");
      file.renameTo(new File(file.getPath() + ".unreadable-" + System.currentTimeMillis()));
    }
  }

  private void write() throws Exception {
    JSONArray arr = new JSONArray();
    for (Server s : servers) arr.put(s.toJson());
    Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
    c.init(Cipher.ENCRYPT_MODE, key());
    byte[] body = c.doFinal(arr.toString().getBytes(StandardCharsets.UTF_8));
    File tmp = new File(file.getPath() + ".tmp");
    try (FileOutputStream out = new FileOutputStream(tmp)) {
      out.write(c.getIV());
      out.write(body);
      out.getFD().sync();
    }
    Files.move(tmp.toPath(), file.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
  }

  private static SecretKey key() throws Exception {
    KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
    ks.load(null);
    if (ks.containsAlias(KEY_ALIAS)) return (SecretKey) ks.getKey(KEY_ALIAS, null);
    KeyGenerator g = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
    g.init(new KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
        .setKeySize(256)
        .build());
    return g.generateKey();
  }
}
