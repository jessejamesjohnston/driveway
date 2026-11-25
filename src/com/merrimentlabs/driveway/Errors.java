// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;

/**
 * Turns the engine's errors into sentences. The engine starts each message
 * with a category in brackets ([network], [auth], [notfound], [exists],
 * [notempty], [other]).
 */
final class Errors {
  private Errors() {}

  static String kind(Throwable e) {
    String m = e.getMessage();
    if (m != null && m.startsWith("[")) {
      int end = m.indexOf(']');
      if (end > 0) return m.substring(1, end);
    }
    return "other";
  }

  static String detail(Throwable e) {
    String m = e.getMessage() == null ? e.toString() : e.getMessage();
    if (m.startsWith("[")) {
      int end = m.indexOf(']');
      if (end > 0) m = m.substring(end + 1).trim();
    }
    return m;
  }

  static boolean isNotFound(Throwable e) {
    return kind(e).equals("notfound");
  }

  private static String service(Servers.Server s, String fallback) {
    Kinds.Kind k = s == null ? null : Kinds.of(s.backend);
    return k != null ? k.label : fallback;
  }

  static String explain(Context ctx, Servers.Server s, Throwable e) {
    return explain(ctx, s, e, false);
  }

  /** onForm: shown on the server's own form, whose checklist offers the fix itself. */
  static String explain(Context ctx, Servers.Server s, Throwable e, boolean onForm) {
    android.util.Log.w("Driveway", (s == null ? "" : s.label + ": ") + e.getMessage());
    if (e instanceof IllegalStateException && !String.valueOf(e.getMessage()).startsWith("[")) return e.getMessage();
    String name = s == null ? ctx.getString(R.string.the_server) : s.label;
    switch (kind(e)) {
      case "network":
        if (!MainActivity.hasLocalNetwork(ctx)) return ctx.getString(R.string.err_local_network, name);
        return ctx.getString(R.string.err_network, name);
      case "auth":
        if (detail(e).contains("knownhosts: key mismatch") || detail(e).contains("host key mismatch")) {
          return ctx.getString(R.string.err_host_key, name);
        }
        if (detail(e).contains("knownhosts: key is unknown")) return ctx.getString(R.string.err_host_unknown, name);
        if (detail(e).contains("non-privileged ports")) return ctx.getString(R.string.err_nfs_ports, name);
        Kinds.Kind kind = s == null ? null : Kinds.of(s.backend);
        if (s != null && Kinds.browser(kind, s.params)) {
          if (onForm) return ctx.getString(R.string.err_signin_ended, name);
          if (Kinds.signsInAgain(kind, s.params)) return ctx.getString(R.string.err_signin_again, name);
          if (!Kinds.changeRestores(kind)) return ctx.getString(R.string.err_signin_none, name);
          return ctx.getString(R.string.err_signin_change, name);
        }
        return ctx.getString(R.string.err_auth, name, detail(e));
      // Driveway's own sign-in (Dropbox, OneDrive): named by the service.
      case "declined":
        return ctx.getString(R.string.err_signin_declined, service(s, name));
      case "expired":
        return ctx.getString(R.string.err_signin_expired, service(s, name));
      // The sign-in page couldn't open (Servers.browserProblem).
      case "background":
        return ctx.getString(R.string.err_signin_background, service(s, name));
      case "nobrowser":
        return ctx.getString(R.string.err_signin_nobrowser, service(s, name));
      // Another app holds the port the sign-in comes back to (the engine's
      // errV6Taken), so the sign-in didn't start.
      case "port":
        return ctx.getString(R.string.err_signin_port, service(s, name));
      case "signin":
        return ctx.getString(R.string.err_signin_failed, service(s, name), detail(e));
      // Signed in, but the service's setup didn't finish (Driveway's own
      // sign-ins never show the service's own setup words or errors).
      case "setup":
        return ctx.getString(R.string.err_setup, service(s, name));
      case "notfound":
        return ctx.getString(R.string.err_not_found);
      case "exists":
        return ctx.getString(R.string.err_exists);
      case "notempty":
        return ctx.getString(R.string.err_not_empty);
      case "bucketname":
        return ctx.getString(R.string.err_bucket_name, name);
      case "denied":
        return ctx.getString(R.string.err_denied, name);
      case "full":
        return ctx.getString(R.string.err_full, name);
      default:
        return ctx.getString(R.string.err_other, name, detail(e));
    }
  }
}
