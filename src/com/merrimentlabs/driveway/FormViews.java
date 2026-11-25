// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.view.View;
import android.view.inputmethod.EditorInfo;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * The building blocks of a server's form, shared by every kind of server: a
 * labeled text field, a field label, a settings card and a section heading
 * over a card. They hold no state; ServerActivity decides what goes where.
 */
final class FormViews {
  private FormViews() {}

  /**
   * A labeled, outlined text field. The label is tied to the field for
   * screen readers. A hint too long to stay readable inside the field at
   * large text sizes is shown below it instead (and still read out with the
   * field).
   */
  static EditText field(Context c, LinearLayout parent, String label, String hint, String value, int inputType) {
    TextView l = label(c, label);
    parent.addView(l);
    EditText e = new EditText(c);
    e.setId(View.generateViewId());
    // The form is carried over by Kept, never into saved state (passwords).
    e.setSaveEnabled(false);
    l.setLabelFor(e.getId());
    e.setSingleLine(true);
    e.setInputType(inputType);
    e.setImeOptions(EditorInfo.IME_ACTION_NEXT);
    e.setMinHeight(Ui.dp(c, 52));
    e.setTypeface(Ui.face(c, 400));
    // Dimmed, with the outline, while a connection uses the form (lockForm).
    e.setTextColor(Ui.fieldInk(c));
    e.setHintTextColor(Ui.fieldHint(c));
    e.setTextSize(16);
    e.setBackground(Ui.outlined(c));
    e.setPaddingRelative(Ui.dp(c, 14), 0, Ui.dp(c, 14), 0);
    if (value != null) e.setText(value);
    parent.addView(e, Ui.matchWidth());
    if (hint != null && hint.length() > LONG_HINT) {
      TextView help = Ui.secondary(c, hint);
      help.setPadding(Ui.dp(c, 4), Ui.dp(c, 4), Ui.dp(c, 4), 0);
      help.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
      parent.addView(help);
      e.setAccessibilityDelegate(new View.AccessibilityDelegate() {
        @Override
        public void onInitializeAccessibilityNodeInfo(View host, android.view.accessibility.AccessibilityNodeInfo info) {
          super.onInitializeAccessibilityNodeInfo(host, info);
          info.setHintText(hint);
        }
      });
    } else {
      e.setHint(hint);
    }
    return e;
  }

  /** Hints longer than this (characters) go below their field. */
  static final int LONG_HINT = 24;

  static TextView label(Context c, String s) {
    TextView t = Ui.text(c, s, 14, 500);
    t.setTextColor(Ui.color(c, R.color.muted));
    t.setPadding(Ui.dp(c, 4), Ui.dp(c, 12), 0, Ui.dp(c, 4));
    return t;
  }

  /** A group's card: fields inside, 16dp in. */
  static LinearLayout group(Context c) {
    LinearLayout card = Ui.card(c);
    card.setPadding(Ui.dp(c, 16), Ui.dp(c, 4), Ui.dp(c, 16), Ui.dp(c, 16));
    return card;
  }

  /** A section label over its card. */
  static LinearLayout section(Context c, int label, LinearLayout card) {
    LinearLayout s = Ui.column(c);
    s.addView(Ui.heading(c, c.getString(label)));
    s.addView(card);
    return s;
  }
}
