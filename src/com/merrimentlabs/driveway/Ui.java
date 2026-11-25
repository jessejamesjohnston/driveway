// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.animation.ValueAnimator;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.graphics.Insets;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.InsetDrawable;
import android.graphics.drawable.RippleDrawable;
import android.graphics.drawable.StateListDrawable;
import android.text.TextUtils;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.animation.PathInterpolator;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.ImageButton;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/**
 * Small helpers for building Driveway's screens in code, with the Merriment
 * Labs look (its own page and card colors, Figtree, cards, pill buttons,
 * outlined fields, icon tiles) and the large-screen and accessibility rules
 * in one place (docs/LARGE-SCREEN-A11Y.md): pages clear the system bars and
 * the keyboard, cap their width and center on wide windows; titles are
 * headings; every control shows a focus ring for keyboard use and is at
 * least 48dp.
 */
final class Ui {
  private Ui() {}

  /** Text column width on a page (forms, settings), unless a page asks for more. */
  static final int PAGE_WIDTH = 720;

  /** The site's ease out: cubic-bezier(0.22, 1, 0.36, 1). */
  static final PathInterpolator EASE_OUT = new PathInterpolator(0.22f, 1f, 0.36f, 1f);

  static int dp(Context c, float v) {
    return Math.round(TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v, c.getResources().getDisplayMetrics()));
  }

  static int color(Context c, int res) {
    return c.getColor(res);
  }

  /** Whether decorative motion may run (Android's "Remove animations" is off). */
  static boolean motion() {
    return ValueAnimator.areAnimatorsEnabled();
  }

  // Pages.

  /** A page's parts: the scrolling column, and a bar pinned under it (empty unless used). */
  static final class Page {
    final LinearLayout col;
    final LinearLayout bar;
    final ScrollView scroll;

    Page(LinearLayout col, LinearLayout bar, ScrollView scroll) {
      this.col = col;
      this.bar = bar;
      this.scroll = scroll;
    }
  }

  static LinearLayout page(Activity a) {
    return page(a, PAGE_WIDTH);
  }

  static LinearLayout page(Activity a, int maxDp) {
    return pageWithBar(a, maxDp).col;
  }

  /**
   * A scrolling page with a centered column at most maxDp wide, and a bar
   * pinned below it for a screen's main buttons (hidden while empty). The
   * page keeps clear of the status and navigation bars, a desktop window's
   * caption, display cutouts and the on-screen keyboard (Android 15+ draws
   * every app edge to edge). Side margins are 16dp in a compact window
   * (under 600dp) and 24dp otherwise.
   */
  static Page pageWithBar(Activity a, int maxDp) {
    // The bars and the keyboard pad an outer frame, so the scrolling area
    // itself ends at them: scrolled content stops at the status bar, and when
    // the on-screen keyboard opens the scroll view gets shorter, which keeps
    // the field being typed in above the keyboard (and the pinned bar with it).
    LinearLayout root = new LinearLayout(a);
    root.setOrientation(LinearLayout.VERTICAL);
    root.setBackgroundColor(color(a, R.color.page));
    ScrollView scroll = new ScrollView(a);
    scroll.setFillViewport(true);
    // The page itself can take keyboard focus (to scroll it); Android would
    // then tint the whole page (a 12% veil in the dark theme). The controls
    // on it show their own focus ring instead.
    scroll.setDefaultFocusHighlightEnabled(false);
    root.setDefaultFocusHighlightEnabled(false);
    root.addView(scroll, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));
    FrameLayout frame = new FrameLayout(a);
    LinearLayout col = column(a);
    int pad = dp(a, 24);
    col.setPadding(pad, dp(a, 16), pad, pad);
    frame.addView(col, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.CENTER_HORIZONTAL));
    scroll.addView(frame);

    // The pinned bar: card color, a hairline above, its buttons in the same
    // column as the page.
    FrameLayout barFrame = new FrameLayout(a);
    barFrame.setBackgroundColor(color(a, R.color.card));
    View hair = new View(a);
    hair.setBackgroundColor(color(a, R.color.line));
    barFrame.addView(hair, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(a, 1), Gravity.TOP));
    LinearLayout bar = row(a);
    bar.setPadding(pad, dp(a, 8), pad, dp(a, 8));
    barFrame.addView(bar, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT, Gravity.CENTER_HORIZONTAL));
    root.addView(barFrame, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT));
    barFrame.setVisibility(View.GONE);
    bar.setOnHierarchyChangeListener(new ViewGroup.OnHierarchyChangeListener() {
      @Override
      public void onChildViewAdded(View parent, View child) {
        barFrame.setVisibility(View.VISIBLE);
      }

      @Override
      public void onChildViewRemoved(View parent, View child) {
        if (bar.getChildCount() == 0) barFrame.setVisibility(View.GONE);
      }
    });

    a.setContentView(root);
    root.setOnApplyWindowInsetsListener((v, insets) -> {
      Insets i = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout()
          | WindowInsets.Type.ime());
      v.setPadding(i.left, i.top, i.right, i.bottom);
      return WindowInsets.CONSUMED;
    });
    lightBars(a);
    root.addOnLayoutChangeListener((v, l, t, r, b, ol, ot, or, ob) -> {
      int inner = r - l - v.getPaddingLeft() - v.getPaddingRight();
      int w = Math.min(inner, dp(a, maxDp));
      int side = inner < dp(a, 600) ? dp(a, 16) : pad;
      if (col.getLayoutParams().width != w || col.getPaddingLeft() != side) {
        col.getLayoutParams().width = w;
        col.setPadding(side, dp(a, 16), side, pad);
        col.requestLayout();
        bar.getLayoutParams().width = w;
        bar.setPadding(side, dp(a, 8), side, dp(a, 8));
        bar.requestLayout();
      }
    });
    return new Page(col, bar, scroll);
  }

  /** Dark status and navigation bar icons on the light theme, light ones on the dark. */
  private static void lightBars(Activity a) {
    WindowInsetsController c = a.getWindow().getInsetsController();
    if (c == null) return;
    int light = WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS | WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS;
    c.setSystemBarsAppearance(night(a) ? 0 : light, light);
  }

  static boolean night(Context c) {
    return (c.getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
  }

  /**
   * A screen's top: Back (an arrow) and the title, as a heading. The title
   * also names the window (desktop caption, recents, screen readers).
   */
  static LinearLayout topBar(Activity a, CharSequence title, View.OnClickListener back) {
    LinearLayout r = row(a);
    r.setPadding(0, 0, 0, dp(a, 4));
    ImageButton b = iconButton(a, R.drawable.ic_arrow_back, a.getString(R.string.back), back);
    LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(dp(a, 48), dp(a, 48));
    lp.setMarginEnd(dp(a, 4));
    r.addView(b, lp);
    TextView t = title(a, title, 22);
    r.addView(t, weight());
    a.setTitle(title);
    return r;
  }

  // Text. Figtree comes from the theme (res/font/figtree.xml); weights are picked here.

  private static Typeface[] faces;

  static Typeface face(Context c, int weight) {
    if (faces == null) faces = new Typeface[10];
    int i = Math.max(1, Math.min(9, weight / 100));
    if (faces[i] == null) {
      Typeface base;
      try {
        base = c.getResources().getFont(R.font.figtree);
      } catch (RuntimeException e) {
        base = Typeface.DEFAULT;
      }
      faces[i] = Typeface.create(base, i * 100, false);
    }
    return faces[i];
  }

  static TextView text(Context c, CharSequence s, float sp, int weight) {
    TextView t = new TextView(c);
    t.setText(s);
    t.setTextColor(color(c, R.color.ink));
    t.setTextSize(TypedValue.COMPLEX_UNIT_SP, sp);
    t.setTypeface(face(c, weight));
    t.setTextIsSelectable(false);
    t.setLineSpacing(0, 1.12f);
    // Lines start at the layout's start edge (the right, in a right-to-left
    // language), whatever language a server's name is in.
    t.setTextAlignment(View.TEXT_ALIGNMENT_VIEW_START);
    return t;
  }

  static TextView text(Context c, CharSequence s, float sp, boolean bold) {
    return text(c, s, sp, bold ? 500 : 400);
  }

  /** Supporting text: 14sp in the muted color (6.2:1 on the page, 8.1:1 on a dark card). */
  static TextView secondary(Context c, CharSequence s) {
    TextView t = text(c, s, 14, 400);
    t.setTextColor(color(c, R.color.muted));
    return t;
  }

  /** A screen's title: SemiBold, and a heading for screen readers. */
  static TextView title(Context c, CharSequence s, float sp) {
    TextView t = text(c, s, sp, 600);
    t.setAccessibilityHeading(true);
    return t;
  }

  /** A section label: 14sp SemiBold in the accent color; screen readers can jump between them. */
  static TextView heading(Context c, CharSequence s) {
    TextView t = text(c, s, 14, 600);
    t.setTextColor(color(c, R.color.accent));
    t.setPadding(dp(c, 4), dp(c, 20), dp(c, 4), dp(c, 8));
    t.setAccessibilityHeading(true);
    return t;
  }

  // Buttons: pills. One filled accent button per screen for the main action;
  // others tonal or text.

  private static final int FILLED = 0, TONAL = 1, TEXT = 2;

  /** The main action on a screen: filled in the accent color. */
  static Button button(Context c, CharSequence s, View.OnClickListener l) {
    return pill(c, s, 0, FILLED, l);
  }

  static Button button(Context c, CharSequence s, int icon, View.OnClickListener l) {
    return pill(c, s, icon, FILLED, l);
  }

  /** A secondary action that should still stand out: accent container, accent text. */
  static Button tonal(Context c, CharSequence s, View.OnClickListener l) {
    return pill(c, s, 0, TONAL, l);
  }

  static Button tonal(Context c, CharSequence s, int icon, View.OnClickListener l) {
    return pill(c, s, icon, TONAL, l);
  }

  /** Other actions: accent text on nothing, so they read as buttons. */
  static Button flat(Context c, CharSequence s, View.OnClickListener l) {
    return pill(c, s, 0, TEXT, l);
  }

  static Button flat(Context c, CharSequence s, int icon, View.OnClickListener l) {
    return pill(c, s, icon, TEXT, l);
  }

  /** A page's main action: taller (56dp) and larger text than every other button on it. */
  static void emphasize(Button b) {
    b.setTextSize(TypedValue.COMPLEX_UNIT_SP, 17);
    b.setMinHeight(dp(b.getContext(), 56));
    b.setMinimumHeight(dp(b.getContext(), 56));
  }

  private static Button pill(Context c, CharSequence s, int icon, int style, View.OnClickListener l) {
    Button b = new Button(c, null, 0, 0);
    b.setText(s);
    b.setAllCaps(false);
    b.setOnClickListener(l);
    b.setTypeface(face(c, 600));
    b.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
    b.setGravity(Gravity.CENTER);
    b.setMinHeight(dp(c, 48));
    b.setMinimumHeight(dp(c, 48));
    b.setMinWidth(dp(c, 48));
    b.setMinimumWidth(dp(c, 48));
    b.setSingleLine(false);
    b.setMaxLines(3);
    b.setEllipsize(TextUtils.TruncateAt.END);
    int fg = style == FILLED ? color(c, R.color.on_accent)
        : style == TONAL ? color(c, R.color.on_accent_container) : color(c, R.color.accent);
    int bg = style == FILLED ? color(c, R.color.accent)
        : style == TONAL ? color(c, R.color.accent_container) : 0;
    b.setTextColor(new ColorStateList(new int[][] {{-android.R.attr.state_enabled}, {}},
        new int[] {withAlpha(color(c, R.color.ink), 0.45f), fg}));
    int side = dp(c, style == TEXT ? 12 : 20);
    b.setPaddingRelative(side, dp(c, 6), side, dp(c, 6));
    // The visible pill is 40dp tall inside the 48dp target.
    GradientDrawable shape = new GradientDrawable();
    shape.setCornerRadius(dp(c, 100));
    shape.setColor(new ColorStateList(new int[][] {{-android.R.attr.state_enabled}, {}},
        new int[] {style == TEXT ? 0 : withAlpha(color(c, R.color.ink), 0.10f), bg}));
    InsetDrawable inset = new InsetDrawable(shape, 0, dp(c, 4), 0, dp(c, 4));
    GradientDrawable mask = new GradientDrawable();
    mask.setCornerRadius(dp(c, 100));
    mask.setColor(0xFFFFFFFF);
    b.setBackground(new RippleDrawable(ColorStateList.valueOf(withAlpha(fg, 0.16f)), inset,
        new InsetDrawable(mask, 0, dp(c, 4), 0, dp(c, 4))));
    b.setStateListAnimator(null);
    b.setElevation(0);
    if (icon != 0) {
      Drawable d = c.getDrawable(icon).mutate();
      int size = dp(c, 18);
      d.setBounds(0, 0, size, size);
      d.setTintList(b.getTextColors());
      b.setCompoundDrawablesRelative(d, null, null, null);
      b.setCompoundDrawablePadding(dp(c, 8));
    }
    focusRing(b, 100);
    return b;
  }

  static int withAlpha(int color, float alpha) {
    return (Math.round(alpha * 255) << 24) | (color & 0x00FFFFFF);
  }

  /** An icon-only button (48dp), labeled for screen readers and tooltips. */
  static ImageButton iconButton(Context c, int icon, CharSequence label, View.OnClickListener l) {
    ImageButton b = new ImageButton(c);
    b.setImageResource(icon);
    b.setImageTintList(ColorStateList.valueOf(color(c, R.color.ink)));
    b.setContentDescription(label);
    b.setTooltipText(label);
    b.setOnClickListener(l);
    b.setScaleType(ImageView.ScaleType.CENTER);
    int size = dp(c, 48);
    b.setMinimumWidth(size);
    b.setMinimumHeight(size);
    b.setLayoutParams(new LinearLayout.LayoutParams(size, size));
    GradientDrawable mask = new GradientDrawable();
    mask.setShape(GradientDrawable.OVAL);
    mask.setColor(0xFFFFFFFF);
    b.setBackground(new RippleDrawable(ColorStateList.valueOf(withAlpha(color(c, R.color.ink), 0.12f)), null, mask));
    focusRing(b, 100);
    return b;
  }

  /** An icon in a color (decorative: screen readers skip it). */
  static ImageView icon(Context c, int icon, int colorRes, int sizeDp) {
    ImageView v = new ImageView(c);
    v.setImageResource(icon);
    v.setImageTintList(ColorStateList.valueOf(color(c, colorRes)));
    v.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    int size = dp(c, sizeDp);
    v.setLayoutParams(new LinearLayout.LayoutParams(size, size));
    return v;
  }

  /** A kind's icon on a soft accent square (server cards, kind tiles). */
  /**
   * A service's tile: the glyph tile (iconTile), or for Box, Dropbox and
   * OneDrive their initial in the same tile and colors (Kinds.letter). The
   * letter is sized in dp, like the glyphs, so the tile reads the same at
   * any text size.
   */
  static FrameLayout serviceTile(Context c, String backend) {
    String letter = Kinds.letter(backend);
    if (letter == null) return iconTile(c, Kinds.icon(backend));
    FrameLayout f = new FrameLayout(c);
    GradientDrawable bg = new GradientDrawable();
    bg.setCornerRadius(dp(c, 12));
    bg.setColor(color(c, R.color.accent_container));
    f.setBackground(bg);
    TextView t = new TextView(c);
    t.setText(letter);
    t.setTypeface(face(c, 600));
    t.setTextSize(TypedValue.COMPLEX_UNIT_DIP, 20);
    t.setIncludeFontPadding(false);
    t.setTextColor(color(c, R.color.on_accent_container));
    t.setGravity(Gravity.CENTER);
    f.addView(t, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.MATCH_PARENT, Gravity.CENTER));
    f.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
    int size = dp(c, 40);
    f.setLayoutParams(new LinearLayout.LayoutParams(size, size));
    return f;
  }

  static FrameLayout iconTile(Context c, int icon) {
    FrameLayout f = new FrameLayout(c);
    GradientDrawable bg = new GradientDrawable();
    bg.setCornerRadius(dp(c, 12));
    bg.setColor(color(c, R.color.accent_container));
    f.setBackground(bg);
    ImageView v = icon(c, icon, R.color.on_accent_container, 22);
    f.addView(v, new FrameLayout.LayoutParams(dp(c, 22), dp(c, 22), Gravity.CENTER));
    f.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
    int size = dp(c, 40);
    f.setLayoutParams(new LinearLayout.LayoutParams(size, size));
    return f;
  }

  // Cards.

  /** Content sits in cards: 16dp corners, the card color on the page, no shadow. */
  static LinearLayout card(Context c) {
    LinearLayout l = column(c);
    l.setBackground(rounded(c, R.color.card, 16));
    l.setClipToOutline(true);
    return l;
  }

  static GradientDrawable rounded(Context c, int colorRes, int radiusDp) {
    GradientDrawable g = new GradientDrawable();
    g.setCornerRadius(dp(c, radiusDp));
    g.setColor(color(c, colorRes));
    return g;
  }

  /** A hairline between rows of a card, inset 16dp. */
  static View divider(Context c) {
    View v = new View(c);
    v.setBackgroundColor(color(c, R.color.line));
    LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, Math.max(1, dp(c, 1)));
    lp.setMarginStart(dp(c, 16));
    lp.setMarginEnd(dp(c, 16));
    v.setLayoutParams(lp);
    v.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    return v;
  }

  /**
   * A row of a card: an optional icon (a tile, or a plain icon), a title and
   * a muted subtitle, and an optional trailing view. At least 56dp tall, 16dp
   * side padding. Clickable rows highlight on hover and press, take Tab and
   * show the focus ring; their text is one stop for screen readers.
   */
  static LinearLayout cardRow(Context c, View lead, CharSequence title, CharSequence subtitle, View trail,
      View.OnClickListener click) {
    LinearLayout r = row(c);
    r.setMinimumHeight(dp(c, 56));
    r.setPaddingRelative(dp(c, 16), dp(c, 10), dp(c, trail != null ? 8 : 16), dp(c, 10));
    if (lead != null) {
      LinearLayout.LayoutParams lp = lead.getLayoutParams() instanceof LinearLayout.LayoutParams
          ? (LinearLayout.LayoutParams) lead.getLayoutParams()
          : new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
      lp.setMarginEnd(dp(c, 16));
      lp.gravity = Gravity.CENTER_VERTICAL;
      r.addView(lead, lp);
    }
    LinearLayout texts = column(c);
    texts.addView(text(c, title, 16, 500));
    if (subtitle != null && subtitle.length() > 0) texts.addView(secondary(c, subtitle));
    r.addView(texts, weight());
    if (trail != null) {
      LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT,
          ViewGroup.LayoutParams.WRAP_CONTENT);
      lp.setMarginStart(dp(c, 8));
      lp.gravity = Gravity.CENTER_VERTICAL;
      r.addView(trail, lp);
    }
    if (click != null) {
      r.setOnClickListener(click);
      pressable(r, 0);
      // The row reads as its title and subtitle, then what it does.
      r.setContentDescription(subtitle == null || subtitle.length() == 0 ? title : title + ", " + subtitle);
    }
    return r;
  }

  /**
   * A small chip ("New") after a row's title: accent text on the accent
   * container, fully rounded. Its row reads it out, so the chip itself is
   * skipped by screen readers.
   */
  static TextView chip(Context c, CharSequence s) {
    TextView t = text(c, s, 12, 600);
    t.setTextColor(color(c, R.color.on_accent_container));
    t.setBackground(rounded(c, R.color.accent_container, 999));
    t.setPadding(dp(c, 8), dp(c, 2), dp(c, 8), dp(c, 2));
    t.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    return t;
  }

  /** Ripple on hover and press, a focus ring, and Tab for a clickable view. */
  static void pressable(View v, int radiusDp) {
    pressable(v, radiusDp, 0);
  }

  /** The same on a surface of its own color (a tile, a tinted line). */
  static void pressable(View v, int radiusDp, int colorRes) {
    Context c = v.getContext();
    GradientDrawable mask = new GradientDrawable();
    mask.setCornerRadius(dp(c, radiusDp));
    mask.setColor(0xFFFFFFFF);
    v.setBackground(new RippleDrawable(ColorStateList.valueOf(withAlpha(color(c, R.color.ink), 0.10f)),
        colorRes == 0 ? null : rounded(c, colorRes, radiusDp), radiusDp > 0 ? mask : null));
    v.setFocusable(true);
    v.setClickable(true);
    focusRing(v, Math.max(radiusDp, 8));
  }

  /** A trailing chevron: this row opens another page. */
  static ImageView chevron(Context c) {
    return icon(c, R.drawable.ic_chevron_right, R.color.muted, 24);
  }

  /** A trailing "opens outside the app" mark. */
  static ImageView external(Context c) {
    return icon(c, R.drawable.ic_open_in_new, R.color.muted, 20);
  }

  /**
   * A tinted strip inside a card for something that needs a hand: a save
   * waiting (attention, amber) or a trust problem (identity changed, sign-in
   * ended: red). The word always carries the state, never the color alone.
   */
  static LinearLayout strip(Context c, boolean trust, CharSequence message, View... actions) {
    LinearLayout box = column(c);
    box.setBackground(rounded(c, trust ? R.color.trust_bg : R.color.attention_bg, 12));
    box.setPaddingRelative(dp(c, 12), dp(c, 10), dp(c, 12), dp(c, 4));
    LinearLayout top = row(c);
    top.setGravity(Gravity.TOP);
    int ink = trust ? R.color.trust_ink : R.color.attention_ink;
    ImageView i = icon(c, trust ? R.drawable.ic_error : R.drawable.ic_warning, ink, 20);
    LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(dp(c, 20), dp(c, 20));
    ilp.setMarginEnd(dp(c, 10));
    top.addView(i, ilp);
    TextView t = text(c, message, 14, 500);
    t.setTextColor(color(c, ink));
    top.addView(t, weight());
    box.addView(top);
    if (actions.length > 0) {
      Layouts.Flow f = actions(c, actions);
      f.setPaddingRelative(dp(c, 22), dp(c, 2), 0, 0);
      box.addView(f);
    } else {
      box.setPaddingRelative(dp(c, 12), dp(c, 10), dp(c, 12), dp(c, 10));
    }
    return box;
  }

  // Fields.

  /**
   * A field's outline: the outline color, accent and thicker while focused.
   * Disabled (while a connection uses the form), the field sinks to the
   * page's color with a faint outline, and its text dims (fieldInk).
   */
  static Drawable outlined(Context c) {
    return outlined(c, false);
  }

  /**
   * A field's outline; with problem, in the red the form uses for trouble,
   * focused or not (a field the status line below says can't be used).
   */
  static Drawable outlined(Context c, boolean problem) {
    GradientDrawable off = new GradientDrawable();
    off.setCornerRadius(dp(c, 12));
    off.setColor(color(c, R.color.page));
    off.setStroke(Math.max(1, dp(c, 1)), withAlpha(color(c, R.color.ink), DISABLED_OUTLINE));
    GradientDrawable idle = new GradientDrawable();
    idle.setCornerRadius(dp(c, 12));
    idle.setColor(color(c, R.color.card));
    if (problem) idle.setStroke(dp(c, 2), color(c, R.color.trust_ink));
    else idle.setStroke(Math.max(1, dp(c, 1)), color(c, R.color.outline));
    GradientDrawable focused = new GradientDrawable();
    focused.setCornerRadius(dp(c, 12));
    focused.setColor(color(c, R.color.card));
    focused.setStroke(dp(c, 2), color(c, problem ? R.color.trust_ink : R.color.accent));
    StateListDrawable s = new StateListDrawable();
    s.addState(new int[] {-android.R.attr.state_enabled}, off);
    s.addState(new int[] {android.R.attr.state_focused}, focused);
    s.addState(new int[] {}, idle);
    return s;
  }

  /** A disabled field's outline and text: the ink at these strengths (docs/LARGE-SCREEN-A11Y.md). */
  private static final float DISABLED_OUTLINE = 0.20f, DISABLED_TEXT = 0.60f;

  /** A field's text: the ink, dimmed while the field is disabled (still readable). */
  static ColorStateList fieldInk(Context c) {
    int ink = color(c, R.color.ink);
    return new ColorStateList(new int[][] {{-android.R.attr.state_enabled}, {}},
        new int[] {withAlpha(ink, DISABLED_TEXT), ink});
  }

  /** A field's hint: muted, dimmed as a disabled button's words are while the field is disabled. */
  static ColorStateList fieldHint(Context c) {
    return new ColorStateList(new int[][] {{-android.R.attr.state_enabled}, {}},
        new int[] {withAlpha(color(c, R.color.ink), 0.45f), color(c, R.color.muted)});
  }

  // Focus.

  /**
   * A ring in the accent color around a view while it has keyboard focus.
   * The theme's own focus state is a faint tint that is easy to lose on a
   * large screen.
   */
  static void focusRing(View v) {
    focusRing(v, 8);
  }

  static void focusRing(View v, int radiusDp) {
    Context c = v.getContext();
    GradientDrawable ring = new GradientDrawable();
    ring.setCornerRadius(dp(c, Math.min(radiusDp, 100)));
    ring.setStroke(dp(c, 3), color(c, R.color.accent));
    StateListDrawable states = new StateListDrawable();
    states.addState(new int[] {android.R.attr.state_focused}, new InsetDrawable(ring, dp(c, 1)));
    v.setForeground(states);
  }

  static int accent(Context c) {
    return color(c, R.color.accent);
  }

  /** Lays a view out at its own width, at the start of a column (instead of stretched across it). */
  static void addStart(LinearLayout col, View v) {
    LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT,
        ViewGroup.LayoutParams.WRAP_CONTENT);
    lp.gravity = Gravity.START;
    col.addView(v, lp);
  }

  /** Buttons in a row that wrap onto more lines when the window is narrow or the text large. */
  static Layouts.Flow actions(Context c, View... buttons) {
    Layouts.Flow f = new Layouts.Flow(c);
    for (View b : buttons) f.addView(b);
    return f;
  }

  /** Text at the start, actions at the end; the actions move below the text when space is short. */
  static Layouts.Split split(Context c, View lead, View trail) {
    Layouts.Split s = new Layouts.Split(c, lead, trail);
    s.setPadding(0, dp(c, 6), 0, dp(c, 6));
    return s;
  }

  static LinearLayout column(Context c) {
    LinearLayout l = new LinearLayout(c);
    l.setOrientation(LinearLayout.VERTICAL);
    return l;
  }

  /** Changes to this text are read out by screen readers (status, progress, errors). */
  static void announces(TextView t) {
    t.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
  }

  /**
   * For keyboard users: the given button of a just-shown dialog takes focus,
   * so Enter answers it. (No effect in touch mode.) A destructive dialog
   * focuses its safe button.
   */
  static AlertDialog focus(AlertDialog d, int which) {
    Button b = d.getButton(which);
    // After the window takes focus, which would otherwise hand it to the
    // dialog's own scroll view.
    if (b != null) b.post(b::requestFocus);
    return d;
  }

  static LinearLayout row(Context c) {
    LinearLayout r = new LinearLayout(c);
    r.setOrientation(LinearLayout.HORIZONTAL);
    r.setGravity(Gravity.CENTER_VERTICAL);
    return r;
  }

  static LinearLayout.LayoutParams weight() {
    return new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
  }

  static LinearLayout.LayoutParams matchWidth() {
    return new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
  }

  static View gap(Context c, int dp) {
    View v = new View(c);
    v.setLayoutParams(new LinearLayout.LayoutParams(1, dp(c, dp)));
    v.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
    return v;
  }
}
