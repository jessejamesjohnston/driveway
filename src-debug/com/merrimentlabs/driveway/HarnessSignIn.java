// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import static com.merrimentlabs.driveway.Harness.*;

import android.content.Context;
import android.content.Intent;
import android.util.Log;

import org.json.JSONObject;

import java.nio.charset.StandardCharsets;

/**
 * The harness's sign-in actions: a browser sign-in run as the setup form
 * runs it (against the stand-in provider, test/servers/fake-oauth.py),
 * cancelling one, a refresh of a saved sign-in, and what a saved server
 * keeps (setting names and a token fingerprint, never values). Debug builds
 * only (see Harness).
 */
final class HarnessSignIn {
  private HarnessSignIn() {}

  /**
   * A web sign-in as the setup form runs it: the browser opens for
   * real, the stand-in provider (test/servers/fake-oauth.py) sends
   * it straight back, defaults answer any questions, then the
   * server is saved with its token. expect=declined, expired,
   * signin, cancelled or background (Driveway wasn't on screen to
   * open the page): the sign-in must end that way (the message
   * the app would show is logged). stopat=NAME: stop
   * at that setup question (OneDrive's drive, which needs
   * Microsoft). client=ID: the saved client_id must be this.
   * drive=ID: the saved drive_id must be this. noquestion=1: no
   * setup question may reach the person (Driveway's own sign-ins).
   */
  static void signin(Context context, String id, Intent intent) throws Exception {
    String backend = intent.getStringExtra("backend");
    JSONObject p = new JSONObject(intent.getStringExtra("params"));
    String expect = intent.getStringExtra("expect") == null ? "ok" : intent.getStringExtra("expect");
    String stopAt = intent.getStringExtra("stopat");
    String client = intent.getStringExtra("client");
    String drive = intent.getStringExtra("drive");
    boolean noQuestion = intent.getStringExtra("noquestion") != null;
    SessionService.hold(context);
    String failed = null;
    Servers.browserProblem = null;
    try {
      String out;
      try {
        out = com.merrimentlabs.driveway.engine.Engine.configStart(id, backend, p.toString());
      } catch (Exception e) {
        if (expect.equals("ok")) throw e;
        Servers.Server shown = new Servers.Server();
        shown.id = id;
        shown.label = intent.getStringExtra("label");
        shown.backend = backend;
        shown.params = p;
        // As Connection does: a page that couldn't open says why.
        if (Servers.browserProblem != null) e = new Exception("[" + Servers.browserProblem + "] " + e.getMessage());
        String kind = Errors.kind(e);
        Log.i(TAG, "HARNESS ENDED " + id + " " + kind + ": " + Errors.explain(context, shown, e));
        boolean ok = expect.equals(kind) || (expect.equals("cancelled") && String.valueOf(e.getMessage()).contains("cancelled"));
        Log.i(TAG, (ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": ended " + kind + ", expected " + expect));
        return;
      }
      if (!expect.equals("ok")) failed = "signed in, expected " + expect;
      while (failed == null) {
        JSONObject q = new JSONObject(out);
        if (q.optBoolean("done")) break;
        Log.i(TAG, "HARNESS QUESTION " + id + " " + q.optString("name") + " -> " + q.optString("default"));
        if (noQuestion) {
          failed = "asked " + q.optString("name");
          break;
        }
        if (q.optString("name").equals(stopAt)) break;
        out = com.merrimentlabs.driveway.engine.Engine.configAnswer(id, q.optString("state"), q.optString("default"));
      }
      JSONObject values = new JSONObject(com.merrimentlabs.driveway.engine.Engine.configValues(id));
      Servers.Server srv = new Servers.Server();
      srv.id = id;
      srv.label = intent.getStringExtra("label");
      srv.backend = backend;
      srv.params = p;
      for (java.util.Iterator<String> it = values.keys(); it.hasNext(); ) {
        String k = it.next();
        srv.params.put(k, values.get(k));
      }
      Kinds.dropSetupAnswers(Kinds.of(backend), srv.params);
      Servers.get(context).save(srv);
      JSONObject tok = new JSONObject(srv.params.optString("token", "{}"));
      Log.i(TAG, "HARNESS SIGNEDIN " + id + " token " + (srv.params.has("token") ? "stored" : "MISSING")
          + " fields " + tok.names() + " client_id " + srv.params.optString("client_id", "(rclone's)")
          + " secret " + (srv.params.has("client_secret") ? "PRESENT" : "none"));
      if (failed == null && (tok.optString("access_token").isEmpty() || tok.optString("refresh_token").isEmpty()
          || tok.optString("expiry").isEmpty())) {
        failed = "token incomplete: " + tok.names();
      }
      if (failed == null && client != null && !client.equals(srv.params.optString("client_id"))) {
        failed = "client_id " + srv.params.optString("client_id");
      }
      if (failed == null && srv.params.has("client_secret")) failed = "a client secret was kept";
      if (failed == null && drive != null && !drive.equals(srv.params.optString("drive_id"))) {
        failed = "drive_id " + srv.params.optString("drive_id");
      }
    } finally {
      SessionService.release(context);
    }
    Log.i(TAG, failed == null ? "HARNESS PASS " + id : "HARNESS FAIL " + id + ": " + failed);
  }

  /** Cancel sign-in, as the screen's button does. */
  static void cancelSignIn(Context context, String id, Intent intent) throws Exception {
    com.merrimentlabs.driveway.engine.Engine.configCancel();
    Log.i(TAG, "HARNESS PASS " + id);
  }

  /**
   * The saved sign-in, used after its token has run out: rclone
   * refreshes it (the stand-in, --expires 1, sees how) and the
   * app keeps the new one. The connection itself then fails
   * against the real service, which never issued these tokens.
   */
  static void refresh(Context context, String id, Intent intent) throws Exception {
    Servers.Server srv = Servers.get(context).find(id);
    String before = new JSONObject(srv.params.optString("token", "{}")).optString("access_token");
    try {
      com.merrimentlabs.driveway.engine.Engine.check(id);
    } catch (Exception e) {
      Log.i(TAG, "HARNESS CHECK " + id + " " + Errors.kind(e));
    }
    srv = Servers.get(context).find(id);
    JSONObject after = new JSONObject(srv.params.optString("token", "{}"));
    boolean ok = !after.optString("access_token").isEmpty() && !after.optString("access_token").equals(before)
        && !after.optString("refresh_token").isEmpty();
    Log.i(TAG, "HARNESS REFRESHED " + id + " " + before + " -> " + after.optString("access_token"));
    Log.i(TAG, (ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": the token wasn't refreshed and kept"));
  }

  /** The saved server's setting names (values left out), after a restart. */
  static void show(Context context, String id, Intent intent) throws Exception {
    Servers.Server srv = Servers.get(context).find(id);
    String token = srv == null ? "" : srv.params.optString("token");
    // The access token's fingerprint, never the token: enough to tell two apart.
    Log.i(TAG, "HARNESS SHOW " + id + " " + (srv == null ? "none" : srv.params.names()) + " token "
        + (token.contains("access_token") ? fingerprint(token.replaceAll(".*\"access_token\":\"([^\"]*)\".*", "$1")) : "none"));
    Log.i(TAG, "HARNESS PASS " + id);
  }

  static String fingerprint(String secret) {
    try {
      byte[] d = java.security.MessageDigest.getInstance("SHA-256").digest(secret.getBytes(StandardCharsets.UTF_8));
      StringBuilder b = new StringBuilder("sha256:");
      for (int i = 0; i < 4; i++) b.append(String.format("%02x", d[i]));
      return b.toString();
    } catch (java.security.NoSuchAlgorithmException e) {
      return "?";
    }
  }
}
