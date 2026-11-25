// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.view.View;
import android.view.ViewGroup;

/**
 * Small layouts that decide at measure time, so they follow a window as
 * it is resized (the activities keep their views across resizes; see
 * configChanges in the manifest). All of them mirror for right-to-left.
 */
final class Layouts {
  private Layouts() {}

  /** Children side by side, wrapping onto the next line when they don't fit (buttons). */
  static final class Flow extends ViewGroup {
    private final int gap;

    Flow(Context c) {
      super(c);
      gap = Ui.dp(c, 4);
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
      int pads = getPaddingLeft() + getPaddingRight();
      boolean bounded = MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED;
      int avail = bounded ? Math.max(0, MeasureSpec.getSize(widthSpec) - pads) : Integer.MAX_VALUE;
      int childSpec = bounded ? MeasureSpec.makeMeasureSpec(avail, MeasureSpec.AT_MOST)
          : MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
      int x = 0, lineHeight = 0, height = 0, widest = 0;
      for (int i = 0; i < getChildCount(); i++) {
        View v = getChildAt(i);
        if (v.getVisibility() == GONE) continue;
        v.measure(childSpec, MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
        int w = v.getMeasuredWidth();
        if (x > 0 && x + gap + w > avail) {
          height += lineHeight;
          x = 0;
          lineHeight = 0;
        }
        x += (x > 0 ? gap : 0) + w;
        lineHeight = Math.max(lineHeight, v.getMeasuredHeight());
        widest = Math.max(widest, x);
      }
      height += lineHeight;
      setMeasuredDimension(resolveSize(widest + pads, widthSpec),
          resolveSize(height + getPaddingTop() + getPaddingBottom(), heightSpec));
    }

    @Override
    protected void onLayout(boolean changed, int l, int t, int r, int b) {
      boolean rtl = getLayoutDirection() == LAYOUT_DIRECTION_RTL;
      int avail = r - l - getPaddingLeft() - getPaddingRight();
      int x = 0, y = getPaddingTop(), lineHeight = 0;
      for (int i = 0; i < getChildCount(); i++) {
        View v = getChildAt(i);
        if (v.getVisibility() == GONE) continue;
        int w = v.getMeasuredWidth();
        if (x > 0 && x + gap + w > avail) {
          y += lineHeight;
          x = 0;
          lineHeight = 0;
        }
        if (x > 0) x += gap;
        int left = rtl ? r - l - getPaddingRight() - x - w : getPaddingLeft() + x;
        v.layout(left, y, left + w, y + v.getMeasuredHeight());
        x += w;
        lineHeight = Math.max(lineHeight, v.getMeasuredHeight());
      }
    }
  }

  /**
   * A row's text (first child) with its actions (second child) at the end;
   * when the text would be left narrower than minLead, the actions go below
   * it instead (a phone, a narrow window, large text).
   */
  static final class Split extends ViewGroup {
    private final int minLead;
    private final int gap;
    private boolean stacked;

    Split(Context c, View lead, View trail) {
      super(c);
      // Large text needs a wider column to stay readable.
      minLead = Math.round(Ui.dp(c, 220) * Math.max(1f, c.getResources().getConfiguration().fontScale));
      gap = Ui.dp(c, 12);
      addView(lead);
      addView(trail);
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
      View lead = getChildAt(0), trail = getChildAt(1);
      int pads = getPaddingLeft() + getPaddingRight();
      int avail = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED
          ? minLead * 3 : Math.max(0, MeasureSpec.getSize(widthSpec) - pads);
      int any = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
      trail.measure(MeasureSpec.makeMeasureSpec(avail, MeasureSpec.AT_MOST), any);
      int leadWidth = avail - trail.getMeasuredWidth() - gap;
      stacked = leadWidth < minLead;
      int height;
      if (stacked) {
        lead.measure(MeasureSpec.makeMeasureSpec(avail, MeasureSpec.EXACTLY), any);
        height = lead.getMeasuredHeight() + trail.getMeasuredHeight();
      } else {
        lead.measure(MeasureSpec.makeMeasureSpec(leadWidth, MeasureSpec.EXACTLY), any);
        height = Math.max(lead.getMeasuredHeight(), trail.getMeasuredHeight());
      }
      setMeasuredDimension(resolveSize(avail + pads, widthSpec),
          resolveSize(height + getPaddingTop() + getPaddingBottom(), heightSpec));
    }

    @Override
    protected void onLayout(boolean changed, int l, int t, int r, int b) {
      View lead = getChildAt(0), trail = getChildAt(1);
      boolean rtl = getLayoutDirection() == LAYOUT_DIRECTION_RTL;
      int start = getPaddingLeft(), end = r - l - getPaddingRight(), top = getPaddingTop();
      int lw = lead.getMeasuredWidth(), lh = lead.getMeasuredHeight();
      int tw = trail.getMeasuredWidth(), th = trail.getMeasuredHeight();
      if (stacked) {
        place(lead, rtl ? end - lw : start, top);
        place(trail, rtl ? end - tw : start, top + lh);
      } else {
        int h = Math.max(lh, th);
        place(lead, rtl ? end - lw : start, top + (h - lh) / 2);
        place(trail, rtl ? start : end - tw, top + (h - th) / 2);
      }
    }
  }

  /**
   * Two columns side by side in an expanded window (840dp wide or more: a
   * Googlebook, a tablet in landscape); one above the other below that. The
   * window's own width decides, measured at layout time, so a window
   * resized across 840dp switches (and not at some smaller width).
   */
  static final class Columns extends ViewGroup {
    static final int EXPANDED_DP = 840;
    private final int gap;
    private boolean sideBySide;

    Columns(Context c, int gapDp, View first, View second) {
      super(c);
      gap = Ui.dp(c, gapDp);
      addView(first);
      addView(second);
    }

    /** Whether the window is expanded (at least 840dp wide). */
    private boolean expanded() {
      Context c = getContext();
      float density = getResources().getDisplayMetrics().density;
      if (c instanceof android.app.Activity) {
        android.graphics.Rect bounds = ((android.app.Activity) c).getWindowManager().getCurrentWindowMetrics()
            .getBounds();
        return bounds.width() / density >= EXPANDED_DP;
      }
      return getResources().getConfiguration().screenWidthDp >= EXPANDED_DP;
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
      View a = getChildAt(0), b = getChildAt(1);
      int pads = getPaddingLeft() + getPaddingRight();
      int avail = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED
          ? Ui.dp(getContext(), 360) : Math.max(0, MeasureSpec.getSize(widthSpec) - pads);
      int any = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
      sideBySide = MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED && expanded();
      int height;
      if (sideBySide) {
        int each = MeasureSpec.makeMeasureSpec((avail - gap) / 2, MeasureSpec.EXACTLY);
        a.measure(each, any);
        b.measure(each, any);
        height = Math.max(a.getMeasuredHeight(), b.getMeasuredHeight());
      } else {
        int all = MeasureSpec.makeMeasureSpec(avail, MeasureSpec.EXACTLY);
        a.measure(all, any);
        b.measure(all, any);
        height = a.getMeasuredHeight() + b.getMeasuredHeight();
      }
      setMeasuredDimension(resolveSize(avail + pads, widthSpec),
          resolveSize(height + getPaddingTop() + getPaddingBottom(), heightSpec));
    }

    @Override
    protected void onLayout(boolean changed, int l, int t, int r, int b) {
      View first = getChildAt(0), second = getChildAt(1);
      boolean rtl = getLayoutDirection() == LAYOUT_DIRECTION_RTL;
      int start = getPaddingLeft(), end = r - l - getPaddingRight(), top = getPaddingTop();
      if (sideBySide) {
        int w = first.getMeasuredWidth();
        place(first, rtl ? end - w : start, top);
        place(second, rtl ? start : end - second.getMeasuredWidth(), top);
      } else {
        place(first, start, top);
        place(second, start, top + first.getMeasuredHeight());
      }
    }
  }

  /**
   * Cards in columns: as many as fit at least minColumnDp each (at most
   * maxColumns), one per line below that. Rows are as tall as their tallest
   * card; cards keep their own height (top aligned). Used for the server
   * cards (two columns in an expanded window only) and the kind tiles.
   */
  static final class Grid extends ViewGroup {
    private final int minColumn;
    private final int maxColumns;
    private final int gap;
    private int columns = 1;

    Grid(Context c, int minColumnDp, int maxColumns, int gapDp) {
      super(c);
      minColumn = Ui.dp(c, minColumnDp);
      this.maxColumns = maxColumns;
      gap = Ui.dp(c, gapDp);
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
      int pads = getPaddingLeft() + getPaddingRight();
      int avail = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED
          ? minColumn : Math.max(0, MeasureSpec.getSize(widthSpec) - pads);
      // Large text needs wider columns to stay readable.
      float scale = Math.max(1f, getResources().getConfiguration().fontScale);
      int min = Math.round(minColumn * Math.min(scale, 1.5f));
      columns = Math.max(1, Math.min(maxColumns, (avail + gap) / (min + gap)));
      int each = (avail - gap * (columns - 1)) / columns;
      int spec = MeasureSpec.makeMeasureSpec(each, MeasureSpec.EXACTLY);
      int any = MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED);
      int height = 0, rowHeight = 0, col = 0;
      for (int i = 0; i < getChildCount(); i++) {
        View v = getChildAt(i);
        if (v.getVisibility() == GONE) continue;
        v.measure(spec, any);
        rowHeight = Math.max(rowHeight, v.getMeasuredHeight());
        if (++col == columns) {
          height += rowHeight + gap;
          rowHeight = 0;
          col = 0;
        }
      }
      if (col > 0) height += rowHeight + gap;
      if (height > 0) height -= gap;
      setMeasuredDimension(resolveSize(avail + pads, widthSpec),
          resolveSize(height + getPaddingTop() + getPaddingBottom(), heightSpec));
    }

    @Override
    protected void onLayout(boolean changed, int l, int t, int r, int b) {
      boolean rtl = getLayoutDirection() == LAYOUT_DIRECTION_RTL;
      int avail = r - l - getPaddingLeft() - getPaddingRight();
      int each = (avail - gap * (columns - 1)) / columns;
      int y = getPaddingTop(), rowHeight = 0, col = 0;
      for (int i = 0; i < getChildCount(); i++) {
        View v = getChildAt(i);
        if (v.getVisibility() == GONE) continue;
        int x = getPaddingLeft() + col * (each + gap);
        if (rtl) x = r - l - getPaddingRight() - (col + 1) * each - col * gap;
        place(v, x, y);
        rowHeight = Math.max(rowHeight, v.getMeasuredHeight());
        if (++col == columns) {
          y += rowHeight + gap;
          rowHeight = 0;
          col = 0;
        }
      }
    }
  }

  private static void place(View v, int left, int top) {
    v.layout(left, top, left + v.getMeasuredWidth(), top + v.getMeasuredHeight());
  }
}
