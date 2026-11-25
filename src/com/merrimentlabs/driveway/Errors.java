// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;

import java.util.function.BooleanSupplier;

/**
 * Turns the engine's errors into sentences. The engine starts each message
 * with a category in brackets ([network], [auth], [notfound], [exists],
 * [notempty], [other]...). Only Driveway's own sentences reach the screen.
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

  /**
   * A one-sentence message (which has no closing period on its own) as the
   * last sentence of a longer block: with its period.
   */
  static String closed(String sentence) {
    String t = sentence.trim();
    return !t.isEmpty() && Character.isLetterOrDigit(t.charAt(t.length() - 1)) ? t + "." : t;
  }

  /**
   * The engine's file check's own sentence (tagged [credential]): what the
   * chosen file is not and where to get the right one, the one engine text
   * shown as it is. Null for anything else, which goes to the log.
   */
  static String credential(Throwable e) {
    if (!(e instanceof org.json.JSONException) && kind(e).equals("credential") && !detail(e).isEmpty()) return detail(e);
    Logs.w("can't check the chosen file", e);
    return null;
  }

  static boolean isNotFound(Throwable e) {
    return kind(e).equals("notfound");
  }

  private static String service(Servers.Server s, String fallback) {
    Kinds.Kind k = s == null ? null : Kinds.of(s.backend);
    return k != null ? k.label : fallback;
  }

  /**
   * Text Driveway wrote itself, from its own strings, for the person to read:
   * the one kind of error whose message explain() shows as it is.
   */
  static final class Worded extends IllegalStateException {
    private static final long serialVersionUID = 1L;

    Worded(String sentence) {
      super(sentence);
    }
  }

  /** A sentence from strings.xml and what fills it in. */
  static final class Message {
    final int res;
    final Object[] args;

    Message(int res, Object... args) {
      this.res = res;
      this.args = args;
    }
  }

  static String explain(Context ctx, Servers.Server s, Throwable e) {
    return explain(ctx, s, e, false);
  }

  /** onForm: shown on the server's own form, whose checklist offers the fix itself. */
  static String explain(Context ctx, Servers.Server s, Throwable e, boolean onForm) {
    if (e instanceof Worded) return e.getMessage();
    Message m = message(s, e, onForm, () -> MainActivity.hasLocalNetwork(ctx), ctx.getString(R.string.the_server));
    return ctx.getString(m.res, m.args);
  }

  /**
   * What the person reads about an error: one of Driveway's own sentences.
   * The engine's or a library's own words go to the log, never on screen;
   * the one exception is a service's answer to a sign-in, quoted as the
   * service's ("Dropbox said: ...").
   */
  static Message message(Servers.Server s, Throwable e, boolean onForm, BooleanSupplier localNetwork, String theServer) {
    Logs.w((s == null ? "" : s.label + ": ") + (e.getMessage() == null ? e : e.getMessage()));
    String name = s == null ? theServer : s.label;
    switch (kind(e)) {
      case "network":
        if (!localNetwork.getAsBoolean()) return new Message(R.string.err_local_network, name);
        // A name that doesn't look up: misspelled, or a local name off its
        // network. Anything else may also be the wrong port (a server that
        // is on, answering on another).
        if (detail(e).contains("no such host")) return new Message(onForm ? R.string.err_no_host_form : R.string.err_no_host, name);
        return new Message(onForm ? R.string.err_network_form : R.string.err_network, name);
      case "auth":
        if (detail(e).contains("knownhosts: key mismatch") || detail(e).contains("host key mismatch")) {
          return new Message(R.string.err_host_key, name);
        }
        if (detail(e).contains("knownhosts: key is unknown")) return new Message(R.string.err_host_unknown, name);
        if (detail(e).contains("non-privileged ports")) return new Message(R.string.err_nfs_ports, name);
        Kinds.Kind kind = s == null ? null : Kinds.of(s.backend);
        if (s != null && Kinds.browser(kind, s.params)) {
          if (onForm) return new Message(R.string.err_signin_ended, name);
          if (Kinds.signsInAgain(kind, s.params)) return new Message(R.string.err_signin_again, name);
          if (!Kinds.changeRestores(kind)) return new Message(R.string.err_signin_none, name);
          return new Message(R.string.err_signin_change, name);
        }
        return new Message(onForm ? R.string.err_auth_form : R.string.err_auth, name);
      // Driveway's own sign-in (Dropbox, OneDrive): named by the service.
      case "declined":
        return new Message(R.string.err_signin_declined, service(s, name));
      case "expired":
        return new Message(R.string.err_signin_expired, service(s, name));
      // The sign-in page couldn't open (Servers.browserProblem).
      case "background":
        return new Message(R.string.err_signin_background, service(s, name));
      case "nobrowser":
        return new Message(R.string.err_signin_nobrowser, service(s, name));
      // Another app holds the port the sign-in comes back to (the engine's
      // errV6Taken), so the sign-in didn't start.
      case "port":
        return new Message(R.string.err_signin_port, service(s, name));
      // The service refused the sign-in and said why: its words, labeled as its own.
      case "signin":
        return new Message(R.string.err_signin_failed, service(s, name), detail(e));
      // The sign-in failed without the service saying why.
      case "signinfailed":
        return new Message(R.string.err_signin_plain, service(s, name));
      // Signed in, but the service's setup didn't finish (Driveway's own
      // sign-ins never show the service's own setup words or errors).
      case "setup":
        return new Message(R.string.err_setup, service(s, name));
      // rclone's own setup steps (a service's questions) stopped on an error,
      // such as no shared drive to choose.
      case "setupended":
        return new Message(R.string.err_setup_ended, service(s, name));
      // The settings handed to the engine weren't readable.
      case "badsettings":
        return new Message(onForm ? R.string.err_bad_settings_form : R.string.err_bad_settings, name);
      case "notfound":
        return new Message(R.string.err_not_found);
      case "exists":
        return new Message(R.string.err_exists);
      case "notempty":
        return new Message(R.string.err_not_empty);
      case "bucketname":
        return new Message(R.string.err_bucket_name, name);
      case "denied":
        return new Message(R.string.err_denied, name);
      case "full":
        return new Message(R.string.err_full, name);
      // The engine's own cases that each need their own next step.
      case "retry":
        return new Message(R.string.err_retry, name);
      case "nosize":
        return new Message(R.string.err_no_size);
      case "changed":
        return new Message(R.string.err_changed);
      case "norename":
        return new Message(R.string.err_no_rename, name);
      case "bothnames":
        return new Message(R.string.err_both_names);
      case "partway":
        return new Message(R.string.err_part_way);
      case "keptold":
        return new Message(R.string.err_kept_old, name);
      case "short":
        return new Message(R.string.err_short, name);
      default:
        return new Message(onForm ? R.string.err_other_form : R.string.err_other, name);
    }
  }
}
