// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.RectF;
import android.view.View;

/**
 * Driveway's house and its driveway, drawn large: the finish of adding a
 * server, and the empty home screen. On the finish a small car rolls up the
 * driveway and parks (500ms); with Android's animations off it is simply
 * parked. Decorative: screen readers skip it (the title says what happened).
 */
final class DrivewayArt extends View {
  private final Paint disc = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Paint house = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Paint drive = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Paint car = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Paint wheel = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Paint ground = new Paint(Paint.ANTI_ALIAS_FLAG);
  private final Path path = new Path();
  private final boolean withCar;
  /** 0: the car at the bottom of the driveway; 1: parked at the door. */
  private float park = 1f;

  DrivewayArt(Context c, boolean withCar) {
    super(c);
    this.withCar = withCar;
    setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
    disc.setColor(Ui.color(c, R.color.accent_container));
    house.setColor(Ui.color(c, R.color.accent));
    drive.setColor(Ui.withAlpha(Ui.color(c, R.color.accent), 0.55f));
    car.setColor(0xFFF6B23C); // the studio's marigold
    wheel.setColor(Ui.color(c, R.color.ink));
    ground.setColor(Ui.color(c, R.color.line));
    ground.setStrokeWidth(Ui.dp(c, 2));
  }

  /** Rolls the car up the driveway, unless animations are off. */
  void arrive() {
    if (!withCar || !Ui.motion()) {
      park = 1f;
      invalidate();
      return;
    }
    ValueAnimator a = ValueAnimator.ofFloat(0f, 1f);
    a.setDuration(500);
    a.setStartDelay(150);
    a.setInterpolator(Ui.EASE_OUT);
    a.addUpdateListener(v -> {
      park = (float) v.getAnimatedValue();
      invalidate();
    });
    park = 0f;
    a.start();
  }

  @Override
  protected void onMeasure(int w, int h) {
    int size = Ui.dp(getContext(), withCar ? 132 : 88);
    setMeasuredDimension(resolveSize(size, w), resolveSize(size, h));
  }

  @Override
  protected void onDraw(Canvas canvas) {
    float s = Math.min(getWidth(), getHeight());
    float ox = (getWidth() - s) / 2f, oy = (getHeight() - s) / 2f;
    canvas.save();
    canvas.translate(ox, oy);
    canvas.scale(s / 108f, s / 108f);
    canvas.drawCircle(54, 54, 52, disc);
    // The house (the launcher icon's shape) and its driveway.
    path.reset();
    path.moveTo(54, 24);
    path.lineTo(30, 43);
    path.lineTo(35, 43);
    path.lineTo(35, 62);
    path.lineTo(47, 62);
    path.lineTo(47, 52);
    path.lineTo(61, 52);
    path.lineTo(61, 62);
    path.lineTo(73, 62);
    path.lineTo(73, 43);
    path.lineTo(78, 43);
    path.close();
    canvas.drawPath(path, house);
    path.reset();
    path.moveTo(49, 63);
    path.lineTo(59, 63);
    path.lineTo(66, 92);
    path.lineTo(42, 92);
    path.close();
    canvas.drawPath(path, drive);
    if (withCar) {
      // The car drives up from below the picture to the door.
      float y = 66 + (1f - park) * 34;
      float scale = 0.8f + 0.2f * (1f - park);
      canvas.save();
      canvas.translate(54, y);
      canvas.scale(scale, scale);
      canvas.drawRoundRect(new RectF(-8, 0, 8, 11), 3, 3, car);
      canvas.drawRoundRect(new RectF(-6, -3, 6, 3), 2, 2, car);
      canvas.drawRect(-9, 7, -6, 12, wheel);
      canvas.drawRect(6, 7, 9, 12, wheel);
      canvas.restore();
    }
    canvas.restore();
  }
}
