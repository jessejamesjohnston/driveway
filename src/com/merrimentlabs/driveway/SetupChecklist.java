// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.content.res.ColorStateList;
import android.graphics.Typeface;
import android.net.Uri;
import android.text.InputType;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.inputmethod.EditorInfo;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/**
 * The checklist for a run: one line per step, and the question being asked
 * (or the fix for a failure) under its step. Updated in place as the run
 * moves on, so each step's line is read out when it changes ("Looking for
 * nas.local…", then "Found nas.local") and nothing else is repeated.
 */
final class SetupChecklist {
  /** What the checklist's buttons ask of the screen showing it. */
  interface Actions {
    /** Whether the server is being added (not changed). */
    boolean isNew();

    /** Connects again, with a fresh browser sign-in. */
    void signInAgain();

    /** Connects again with the form as it is. */
    void retry();

    /** Back to the form, with the sign-in field (or else the folder) focused. */
    void backTo(boolean signIn);

    /** Keeps the server although the connection failed. */
    void saveAnyway();
  }

  private final Activity a;
  private final Actions actions;
  final Connection run;
  final LinearLayout card;
  final List<Row> rows = new ArrayList<>();

  final class Row {
    final Connection.Step step;
    final android.widget.FrameLayout icon;
    final TextView title;
    final TextView detail;
    final LinearLayout extra;
    final LinearLayout texts;
    /** The whole line: a keyboard stop only while it holds focus taken from the form (takeFocus). */
    final LinearLayout line;
    Connection.State shown;
    String extraKey;

    Row(Connection.Step step) {
      this.step = step;
      LinearLayout row = Ui.row(a);
      line = row;
      Ui.focusRing(row);
      row.setOnFocusChangeListener((v, has) -> {
        if (!has) {
          v.setFocusable(false);
          v.setFocusableInTouchMode(false);
        }
      });
      row.setGravity(Gravity.TOP);
      row.setMinimumHeight(Ui.dp(a, 52));
      row.setPadding(Ui.dp(a, 16), Ui.dp(a, 12), Ui.dp(a, 16), Ui.dp(a, 12));
      icon = new android.widget.FrameLayout(a);
      LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(Ui.dp(a, 24), Ui.dp(a, 24));
      ilp.setMarginEnd(Ui.dp(a, 14));
      row.addView(icon, ilp);
      texts = Ui.column(a);
      title = Ui.text(a, "", 15.5f, 500);
      Ui.announces(title);
      texts.addView(title);
      detail = Ui.secondary(a, "");
      texts.addView(detail);
      extra = Ui.column(a);
      texts.addView(extra);
      row.addView(texts, Ui.weight());
      if (card.getChildCount() > 0) card.addView(Ui.divider(a));
      card.addView(row);
    }

    void update() {
      Connection.State st = step.state;
      if (st != shown) {
        icon.removeAllViews();
        icon.addView(stateIcon(st, step.id), new android.widget.FrameLayout.LayoutParams(Ui.dp(a, 24), Ui.dp(a, 24)));
        shown = st;
      }
      String t = stepTitle(run, step);
      if (!t.contentEquals(title.getText())) title.setText(t);
      title.setTypeface(Ui.face(a, st == Connection.State.PENDING ? 400 : 500));
      title.setTextColor(Ui.color(a, st == Connection.State.PENDING ? R.color.muted : R.color.ink));
      String d = stepDetail(run, step);
      detail.setText(d == null ? "" : d);
      detail.setVisibility(d == null ? View.GONE : View.VISIBLE);
      detail.setTextColor(Ui.color(a, st == Connection.State.FAILED ? R.color.trust_ink : R.color.muted));
      // A failure's words can be selected to copy, so they are a keyboard stop: 48dp at least.
      boolean selectable = st == Connection.State.FAILED && d != null;
      detail.setTextIsSelectable(selectable);
      detail.setMinHeight(selectable ? Ui.dp(a, 48) : 0);
      // What goes under the step: the question asked, or the fixes for a
      // failure. Rebuilt only when it changes, so typing isn't disturbed.
      String key = st == Connection.State.FAILED && !run.running() ? "fix:" + run.errorKind
          : (st == Connection.State.WAITING || st == Connection.State.RUNNING) ? "ask:" + run.ask + ":"
              + (run.question == null ? "" : run.questionNo) : "";
      if (!key.equals(extraKey)) {
        extraKey = key;
        extra.removeAllViews();
        View v = key.startsWith("fix:") ? fixes(run, step) : key.startsWith("ask:") ? askView(run, step) : null;
        if (v != null) extra.addView(v);
      }
      // Read as its line and its state ("Sign in as sam, not yet").
      texts.setContentDescription(t + ", " + a.getString(stateWord(st)) + (d != null ? ". " + d : ""));
    }
  }

  /** Every step worked but the settings couldn't be kept on this device. */
  final TextView saveError;

  SetupChecklist(Activity a, Connection run, Actions actions) {
    this.a = a;
    this.actions = actions;
    this.run = run;
    card = Ui.card(a);
    card.setPadding(0, Ui.dp(a, 4), 0, Ui.dp(a, 4));
    for (Connection.Step st : run.steps) rows.add(new Row(st));
    saveError = Ui.text(a, "", 14, 500);
    saveError.setTextColor(Ui.color(a, R.color.trust_ink));
    saveError.setPadding(Ui.dp(a, 16), Ui.dp(a, 8), Ui.dp(a, 16),
        Ui.dp(a, 12));
    Ui.announces(saveError);
    card.addView(saveError);
    update();
  }

  /**
   * Focus taken from the form as the run locks it: the line of the step
   * the run is at (the keyboard's, or a screen reader's), so it never
   * jumps to the top of the page.
   */
  void takeFocus(boolean keyboard, boolean reader) {
    Row at = rows.get(0);
    for (Row r : rows) {
      if (r.step.state == Connection.State.RUNNING || r.step.state == Connection.State.WAITING) {
        at = r;
        break;
      }
    }
    if (keyboard) {
      boolean touch = at.line.isInTouchMode();
      // The on-screen keyboard has no field to type in any more.
      View f = a.getCurrentFocus();
      if (f instanceof EditText) {
        a.getSystemService(android.view.inputmethod.InputMethodManager.class).hideSoftInputFromWindow(f.getWindowToken(), 0);
      }
      at.line.setFocusable(true);
      at.line.setFocusableInTouchMode(touch);
      at.line.requestFocus();
    }
    if (reader) {
      (keyboard ? at.line : at.texts).performAccessibilityAction(
          android.view.accessibility.AccessibilityNodeInfo.ACTION_ACCESSIBILITY_FOCUS, null);
    }
  }

  void update() {
    for (Row r : rows) r.update();
    // Not kept: the device couldn't write it, or the engine refused the
    // settings (run.error names the setting and the next step).
    boolean refused = "savesettings".equals(run.errorKind);
    boolean failedSave = run.outcome == Connection.Outcome.FAILED && ("save".equals(run.errorKind) || refused);
    saveError.setText(!failedSave ? "" : refused ? run.error : a.getString(R.string.save_failed));
    saveError.setVisibility(failedSave ? View.VISIBLE : View.GONE);
  }

  private int stateWord(Connection.State s) {
    switch (s) {
      case DONE:
        return R.string.state_done;
      case RUNNING:
        return R.string.state_running;
      case WAITING:
        return R.string.state_waiting;
      case FAILED:
        return R.string.state_failed;
      default:
        return R.string.state_pending;
    }
  }

  private View stateIcon(Connection.State s, String step) {
    // The person is asked about an identity: a key, not a spinner (nothing runs meanwhile).
    if (s == Connection.State.WAITING && step.equals(Connection.IDENTITY)) {
      return Ui.icon(a, R.drawable.ic_key, R.color.accent, 24);
    }
    switch (s) {
      case RUNNING:
      case WAITING: {
        ProgressBar p = new ProgressBar(a, null, android.R.attr.progressBarStyleSmall);
        p.setIndeterminateTintList(ColorStateList.valueOf(Ui.accent(a)));
        p.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        return p;
      }
      case DONE:
        return Ui.icon(a, R.drawable.ic_check_circle, R.color.accent, 24);
      case FAILED:
        return Ui.icon(a, R.drawable.ic_error, R.color.trust_ink, 24);
      default:
        return Ui.icon(a, R.drawable.ic_radio_button_unchecked, R.color.muted, 24);
    }
  }

  private String host(Connection r) {
    Servers.Server s = r.server;
    String h = s.params.optString("host", "");
    if (h.isEmpty()) h = Uri.parse(s.params.optString("url", "")).getHost();
    return h == null || h.isEmpty() ? s.label : h;
  }

  private String folderName(Connection r) {
    Servers.Server s = r.server;
    if (!s.folder.isEmpty()) return s.folder;
    if (r.browser || r.kind.alt != null) return a.getString(R.string.your_service, r.kind.label);
    switch (s.backend) {
      case "sftp":
        return a.getString(R.string.your_home_folder);
      case "smb":
        return a.getString(R.string.the_shares);
      default:
        return a.getString(R.string.the_top_folder);
    }
  }

  private String stepTitle(Connection r, Connection.Step s) {
    Connection.State st = s.state;
    switch (s.id) {
      case Connection.FOUND: {
        String h = host(r);
        return a.getString(st == Connection.State.DONE ? R.string.found_host : st == Connection.State.FAILED
            ? R.string.not_found_host : st == Connection.State.RUNNING ? R.string.finding_host : R.string.find_host, h);
      }
      case Connection.IDENTITY:
        if (st == Connection.State.DONE) return a.getString(R.string.identity_checked);
        if (st == Connection.State.FAILED) return a.getString(R.string.identity_not_trusted);
        if (st == Connection.State.WAITING) {
          return a.getString(r.identityChanged ? R.string.identity_changed : R.string.identity_first, host(r));
        }
        return a.getString(R.string.check_identity_step);
      case Connection.SIGN_IN: {
        if (r.browser) {
          if (st == Connection.State.FAILED && "cancelled".equals(r.errorKind)) return a.getString(R.string.sign_in_cancelled);
          int id = st == Connection.State.DONE ? R.string.signed_in_to : st == Connection.State.FAILED ? R.string.not_signed_in_to
              : st == Connection.State.WAITING ? R.string.signing_in_to : R.string.sign_in_to;
          return a.getString(id, r.kind.label);
        }
        if (st == Connection.State.FAILED && "cancelled".equals(r.errorKind)) return a.getString(R.string.setup_cancelled);
        String user = r.server.params.optString("user", "");
        if (r.server.backend.equals("nfs")) {
          String export = r.server.params.optString("export", "");
          int id = st == Connection.State.DONE ? R.string.opened_export : st == Connection.State.FAILED ? R.string.not_opened_export
              : st == Connection.State.RUNNING ? R.string.opening_export : R.string.open_export;
          return a.getString(id, export);
        }
        if (!user.isEmpty()) {
          int id = st == Connection.State.DONE ? R.string.signed_in_as : st == Connection.State.FAILED ? R.string.not_signed_in_as
              : st == Connection.State.RUNNING ? R.string.signing_in_as : R.string.sign_in_as;
          return a.getString(id, user);
        }
        return a.getString(st == Connection.State.DONE ? R.string.connected_step : st == Connection.State.FAILED
            ? R.string.not_connected : st == Connection.State.RUNNING ? R.string.connecting : R.string.connect_step);
      }
      default: {
        String f = folderName(r);
        int id = st == Connection.State.DONE ? R.string.opened_folder : st == Connection.State.FAILED ? R.string.not_opened_folder
            : st == Connection.State.RUNNING ? R.string.opening_folder : R.string.open_folder;
        return a.getString(id, f);
      }
    }
  }

  private String stepDetail(Connection r, Connection.Step s) {
    if (s.state == Connection.State.FAILED) return s.detail;
    if (s.id.equals(Connection.IDENTITY) && s.state == Connection.State.DONE && s.detail != null) {
      return a.getString(R.string.identity_remembered, shortPrint(s.detail));
    }
    if (s.id.equals(Connection.SIGN_IN) && s.state == Connection.State.WAITING && r.ask == Connection.Ask.BROWSER) {
      return a.getString(R.string.signing_in_body, r.kind.label);
    }
    return null;
  }

  private static String shortPrint(String print) {
    return print.length() > 20 ? print.substring(0, 14) + "…" + print.substring(print.length() - 4) : print;
  }

  /** What the run is asking the person, under its step. */
  private View askView(Connection r, Connection.Step s) {
    LinearLayout box = Ui.column(a);
    box.setPadding(0, Ui.dp(a, 8), 0, 0);
    switch (r.ask) {
      case IDENTITY: {
        if (!s.id.equals(Connection.IDENTITY)) return null;
        String h = host(r);
        // The server's own copy to compare with is the file of the key type it presented.
        String[] key = Kinds.hostKeyFile(r.keyType);
        TextView body = Ui.secondary(a, r.identityChanged ? a.getString(R.string.identity_changed_body)
            : key != null ? a.getString(R.string.identity_first_body, h, key[0], key[1])
            : a.getString(R.string.identity_first_body_other, h));
        if (r.identityChanged) body.setTextColor(Ui.color(a, R.color.trust_ink));
        box.addView(body);
        TextView fp = Ui.text(a, r.fingerprint, 14, 400);
        fp.setTypeface(Typeface.MONOSPACE);
        fp.setTextIsSelectable(true);
        fp.setMinHeight(Ui.dp(a, 48));
        fp.setGravity(Gravity.CENTER_VERTICAL);
        fp.setBackground(Ui.rounded(a, R.color.page, 10));
        fp.setPadding(Ui.dp(a, 12), Ui.dp(a, 8), Ui.dp(a, 12), Ui.dp(a, 8));
        LinearLayout.LayoutParams flp = Ui.matchWidth();
        flp.topMargin = Ui.dp(a, 8);
        box.addView(fp, flp);
        // Trusting a server is a decision: Cancel comes first and takes the keyboard.
        Button cancel = Ui.tonal(a, a.getString(R.string.cancel), v -> r.trust(false));
        cancel.setContentDescription(a.getString(R.string.dont_trust, h));
        cancel.setTag("safe");
        Button trust = Ui.button(a, a.getString(R.string.trust), v -> r.trust(true));
        box.addView(Ui.actions(a, cancel, trust));
        return box;
      }
      case BROWSER: {
        if (!s.id.equals(Connection.SIGN_IN)) return null;
        Button again = Ui.tonal(a, a.getString(R.string.open_page_again), R.drawable.ic_open_in_browser, v -> r.reopenSignIn());
        Button cancel = Ui.flat(a, a.getString(R.string.cancel_sign_in), v -> r.cancelSignIn());
        cancel.setTag("safe");
        box.addView(Ui.actions(a, again, cancel));
        return box;
      }
      case QUESTION: {
        if (!s.id.equals(Connection.SIGN_IN) || r.question == null) return null;
        JSONObject q = r.question;
        // Driveway's own question (which OneDrive): the question in plain
        // words, and each choice by its name, never its ID.
        boolean friendly = q.optBoolean("friendly");
        String name = questionName(q.optString("name"));
        TextView qt = Ui.text(a, friendly ? q.optString("help") : a.getString(R.string.setup_question, r.kind.label, name),
            15, 500);
        box.addView(qt);
        String help = friendly ? "" : q.optString("help");
        // Why the service asks again: in Driveway's words, with its own to the log.
        if (!q.optString("error").isEmpty()) {
          Logs.w(r.kind.label + " setup asks again: " + q.optString("error"));
          help = a.getString(R.string.setup_answer_refused, r.kind.label) + (help.isEmpty() ? "" : "\n\n" + help);
        }
        if (!help.isEmpty()) box.addView(Ui.secondary(a, help));
        EditText input = new EditText(a);
        input.setSingleLine(true);
        input.setTypeface(Ui.face(a, 400));
        input.setTextColor(Ui.color(a, R.color.ink));
        input.setBackground(Ui.outlined(a));
        input.setMinHeight(Ui.dp(a, 52));
        input.setPaddingRelative(Ui.dp(a, 14), 0, Ui.dp(a, 14), 0);
        String what = a.getString(R.string.answer_for, name);
        input.setAccessibilityDelegate(new View.AccessibilityDelegate() {
          @Override
          public void onInitializeAccessibilityNodeInfo(View host, android.view.accessibility.AccessibilityNodeInfo info) {
            super.onInitializeAccessibilityNodeInfo(host, info);
            info.setHintText(what);
          }
        });
        input.setText(r.draftAnswer);
        if (q.optBoolean("password")) {
          input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        }
        input.addTextChangedListener(new ServerActivity.Watcher(() -> r.draftAnswer = input.getText().toString()));
        JSONArray examples = q.optJSONArray("examples");
        boolean exclusive = q.optBoolean("exclusive") && examples != null && examples.length() > 0;
        for (int i = 0; examples != null && i < examples.length(); i++) {
          JSONObject e = examples.optJSONObject(i);
          String value = e.optString("value");
          String label = e.optString("help").isEmpty() ? value
              : friendly ? e.optString("help") : value + "  ·  " + e.optString("help").split("\n", 2)[0];
          // Where only these answers are allowed, picking one answers.
          Button b = Ui.flat(a, label, v -> {
            if (exclusive) {
              r.answer(value);
            } else {
              input.setText(value);
            }
          });
          b.setGravity(Gravity.START | Gravity.CENTER_VERTICAL);
          // Only these answers allowed: the first takes the keyboard.
          if (exclusive && i == 0) b.setTag("safe");
          box.addView(b);
        }
        Button cont = Ui.button(a, a.getString(R.string.answer), v -> r.answer(input.getText().toString()));
        Button cancel = Ui.flat(a, a.getString(R.string.cancel), v -> r.answer(null));
        cancel.setContentDescription(a.getString(R.string.cancel_setup));
        if (!exclusive) {
          LinearLayout.LayoutParams ilp = Ui.matchWidth();
          ilp.topMargin = Ui.dp(a, 8);
          box.addView(input, ilp);
          input.setTag("safe");
          // Enter in the answer is Continue.
          input.setImeOptions(EditorInfo.IME_ACTION_DONE);
          input.setOnEditorActionListener((v, action, event) -> {
            boolean enter = action == EditorInfo.IME_ACTION_DONE
                || (event != null && event.getKeyCode() == KeyEvent.KEYCODE_ENTER);
            if (enter && (event == null || event.getAction() == KeyEvent.ACTION_UP)) cont.performClick();
            return enter;
          });
          box.addView(Ui.actions(a, cont, cancel));
        } else {
          box.addView(Ui.actions(a, cancel));
        }
        return box;
      }
      default:
        return null;
    }
  }

  /**
   * A setup question's name in words. rclone names its questions after
   * settings ("config_driveid"); the common ones read as plain words, the
   * rest without the prefix.
   */
  static String questionName(String key) {
    switch (key) {
      case "config_team_drive":
        return "shared drive";
      case "config_driveid":
        return "drive";
      case "config_drive_ok":
        return "is this the right drive";
      case "config_type":
        return "type of connection";
      case "config_2fa":
      case "2fa":
        return "two-factor code";
      default:
        return key.replaceFirst("^config_", "").replace('_', ' ');
    }
  }

  /** The fix for a failed step, in place: the right button for what went wrong. */
  private View fixes(Connection r, Connection.Step s) {
    List<View> b = new ArrayList<>();
    String k = r.errorKind == null ? "" : r.errorKind;
    if (k.equals("network") && !MainActivity.hasLocalNetwork(a)) {
      b.add(Ui.tonal(a, a.getString(R.string.allow_nearby), v -> Permissions.allow(a, ServerActivity.ASK_NEARBY,
          MainActivity.LOCAL_NETWORK)));
    } else if (s.id.equals(Connection.SIGN_IN) && r.browser && Kinds.signsInAgain(r.kind, r.server.params)
        && !k.equals("cancelled")) {
      b.add(Ui.tonal(a, a.getString(R.string.sign_in_again), v -> actions.signInAgain()));
    } else if (s.id.equals(Connection.SIGN_IN) && k.equals("auth")) {
      b.add(Ui.tonal(a, a.getString(R.string.change_sign_in), v -> actions.backTo(true)));
    } else if (s.id.equals(Connection.FOLDER) && k.equals("notfound")) {
      b.add(Ui.tonal(a, a.getString(R.string.change_folder), v -> actions.backTo(false)));
    }
    if (!k.equals("untrusted")) b.add(Ui.flat(a, a.getString(R.string.try_connect_again), v -> actions.retry()));
    // Settings the engine refused can't be saved anyway: the address is fixed on the form.
    if (!actions.isNew() && r.save && !k.equals("untrusted") && !k.equals("cancelled") && !k.equals("badsettings")
        && !tokenless(r)) {
      b.add(Ui.flat(a, a.getString(R.string.save_anyway), v -> actions.saveAnyway()));
    }
    Layouts.Flow f = Ui.actions(a, b.toArray(new View[0]));
    f.setPadding(0, Ui.dp(a, 4), 0, 0);
    return f;
  }

  /**
   * A server that opens only with a sign-in token and has none (a sign-in
   * that failed or never finished): saved, it couldn't open, so there is no
   * Save anyway.
   */
  static boolean tokenless(Connection r) {
    JSONObject p = r.server.params;
    boolean needs = Kinds.browser(r.kind, p) || (r.kind != null && r.kind.alt != null && r.kind.alt.makesToken);
    return needs && (p == null || !p.has("token"));
  }
}
