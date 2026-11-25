// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.SystemClock;

/**
 * Keeps Driveway in the foreground while a server is in use.
 *
 * Android 15 and later cut the network of an app in the background. A
 * provider only counts as foreground while the app using it is on screen;
 * a copy that Files runs in its own background service, or an upload after
 * the app that saved has moved on, would lose the connection halfway. So the
 * first time a server is used while Driveway is allowed to (Files or the
 * saving app on screen), this service starts, shows a quiet "connected"
 * notification, and stops itself a few minutes after the last use, once no
 * upload is running.
 */
public final class SessionService extends Service {
  static final String CHANNEL = "connected";
  private static final long IDLE_MS = 3 * 60 * 1000;

  private static volatile long lastUse;
  private static volatile boolean active;
  private static int holds;

  /**
   * Keeps Driveway in the foreground until release(): while someone signs
   * in to a service in the browser, Driveway is in the background, and the
   * sign-in's last step (fetching the token) needs the network.
   */
  static synchronized void hold(Context ctx) {
    holds++;
    touch(ctx);
  }

  static synchronized void release(Context ctx) {
    if (holds > 0) holds--;
    touch(ctx);
  }

  private static synchronized boolean held() {
    return holds > 0;
  }

  private static volatile long lastStart;

  /** A server is being used: keep Driveway in the foreground for a while. */
  static void touch(Context ctx) {
    long now = SystemClock.elapsedRealtime();
    lastUse = now;
    if (active) return;
    // Reads call this for every block: try to start at most every 5 seconds.
    if (now - lastStart < 5000) return;
    lastStart = now;
    try {
      ctx.startForegroundService(new Intent(ctx, SessionService.class));
      active = true;
    } catch (RuntimeException e) {
      // Android refuses when nothing using Driveway is on screen; the work goes ahead without it.
      Logs.i("no foreground session: " + e.getMessage());
    }
  }

  private final Handler handler = new Handler(Looper.getMainLooper());
  private int lastStartId;

  private final Runnable check = new Runnable() {
    @Override
    public void run() {
      boolean uploading = Uploads.running() > 0;
      if (!uploading && !held() && SystemClock.elapsedRealtime() - lastUse > IDLE_MS) {
        stop();
        return;
      }
      getSystemService(NotificationManager.class).notify(1, notification(uploading));
      handler.postDelayed(this, uploading ? 1000 : 5000);
    }
  };

  static void channels(Context ctx) {
    NotificationManager nm = ctx.getSystemService(NotificationManager.class);
    nm.createNotificationChannel(new NotificationChannel(CHANNEL, ctx.getString(R.string.channel_connected),
        NotificationManager.IMPORTANCE_LOW));
    nm.createNotificationChannel(new NotificationChannel(Uploads.CHANNEL, ctx.getString(R.string.channel_uploads),
        NotificationManager.IMPORTANCE_DEFAULT));
  }

  private Notification notification(boolean uploading) {
    PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class),
        PendingIntent.FLAG_IMMUTABLE);
    Notification.Builder b = new Notification.Builder(this, CHANNEL)
        .setSmallIcon(R.drawable.ic_server)
        .setContentTitle(getString(uploading ? R.string.uploading : R.string.connected_notice))
        .setContentIntent(open)
        .setOnlyAlertOnce(true)
        .setOngoing(true);
    if (uploading) {
      // "Saving report.pdf to Home server · 45%", or "Saving 3 files · 45%" for several.
      java.util.List<Uploads.Sending> now = Uploads.sending();
      long done = 0, total = 0;
      for (Uploads.Sending p : now) {
        done += p.done;
        total += p.total;
      }
      int pct = total > 0 ? (int) (done * 100 / total) : 0;
      if (now.size() == 1) {
        b.setContentTitle(getString(R.string.uploading_one, now.get(0).name, now.get(0).server));
      } else if (now.size() > 1) {
        b.setContentTitle(getString(R.string.uploading_many, now.size()));
      }
      if (total > 0) {
        b.setContentText(getString(R.string.uploading_progress, pct,
            android.text.format.Formatter.formatShortFileSize(this, done),
            android.text.format.Formatter.formatShortFileSize(this, total)));
        b.setProgress(100, pct, false);
      } else {
        b.setProgress(0, 0, true);
      }
    }
    return b.build();
  }

  @Override
  public int onStartCommand(Intent intent, int flags, int startId) {
    lastStartId = startId;
    active = true;
    channels(this);
    startForeground(1, notification(Uploads.running() > 0), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
    handler.removeCallbacks(check);
    handler.postDelayed(check, 5000);
    return START_NOT_STICKY;
  }

  @Override
  public void onTimeout(int startId, int fgsType) {
    // Android's daily limit for data-sync services: stop now, start again on the next use.
    stop();
  }

  private void stop() {
    handler.removeCallbacks(check);
    active = false;
    stopForeground(STOP_FOREGROUND_REMOVE);
    // Only if no start arrived since the last one seen here: stopping a
    // service that a new start still expects to go foreground crashes the app.
    // That start's onStartCommand brings the notification back.
    stopSelfResult(lastStartId);
  }

  @Override
  public void onDestroy() {
    active = false;
    handler.removeCallbacks(check);
    super.onDestroy();
  }

  @Override
  public IBinder onBind(Intent intent) {
    return null;
  }
}
