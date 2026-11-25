// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.job.JobInfo;
import android.app.job.JobParameters;
import android.app.job.JobScheduler;
import android.app.job.JobService;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;

/**
 * The opt-in weekly update check (Settings, About, "Check weekly": off until
 * the person turns it on). While it is on, Android runs this job about once
 * a week when there is a network; with it off no job exists, so Driveway
 * never contacts GitHub on its own. A newer version posts one quiet
 * notification that opens the release page, once per version. A failed
 * check waits for the next week.
 *
 * <p>The job isn't kept across a restart of the device (that would need the
 * boot permission); {@link #sync} arms it again each time Driveway starts.
 */
public final class UpdateJob extends JobService {
  private static final int JOB_ID = 0x5550; // Driveway's only job
  private static final long WEEK_MS = 7L * 24 * 60 * 60 * 1000;
  static final String CHANNEL = "updates";
  private static final int NOTIFICATION = 0x5551;

  /** The weekly check's settings, in the app's own preferences (never backed up: allowBackup is off). */
  static UpdateCheck.Store store(Context c) {
    SharedPreferences p = c.getApplicationContext().getSharedPreferences("updates", Context.MODE_PRIVATE);
    return new UpdateCheck.Store() {
      @Override
      public String get(String key) {
        return p.getString(key, null);
      }

      @Override
      public void put(String key, String value) {
        p.edit().putString(key, value).apply();
      }
    };
  }

  /** Arms the weekly job when the check is on, cancels it otherwise. */
  static void sync(Context c) {
    JobScheduler js = c.getSystemService(JobScheduler.class);
    if (js == null) return;
    if (!UpdateCheck.weekly(store(c))) {
      js.cancel(JOB_ID);
      return;
    }
    if (js.getPendingJob(JOB_ID) != null) return;
    js.schedule(new JobInfo.Builder(JOB_ID, new ComponentName(c, UpdateJob.class))
        .setPeriodic(WEEK_MS)
        .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
        .build());
  }

  /** The installed version's name ("0.2.13"), or null. */
  static String installed(Context c) {
    try {
      return c.getPackageManager().getPackageInfo(c.getPackageName(), 0).versionName;
    } catch (PackageManager.NameNotFoundException e) {
      return null;
    }
  }

  @Override
  public boolean onStartJob(JobParameters params) {
    new Thread(() -> {
      UpdateCheck.Store s = store(this);
      String version = installed(this);
      if (UpdateCheck.weekly(s) && version != null) {
        UpdateCheck.Result r = UpdateCheck.check(version, UpdateCheck.API);
        // Without the notification permission nothing can be shown: the
        // version stays unannounced, so a later week tells the person once
        // notifications are allowed.
        if (UpdateCheck.shouldAnnounce(s, r) && canNotify()) {
          notify(r.version);
          UpdateCheck.announced(s, r);
        }
      }
      jobFinished(params, false);
    }).start();
    return true;
  }

  @Override
  public boolean onStopJob(JobParameters params) {
    return false;
  }

  private boolean canNotify() {
    return checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
  }

  private void notify(String version) {
    NotificationManager nm = getSystemService(NotificationManager.class);
    nm.createNotificationChannel(new NotificationChannel(CHANNEL, getString(R.string.update_channel),
        NotificationManager.IMPORTANCE_LOW));
    PendingIntent open = PendingIntent.getActivity(this, NOTIFICATION,
        new Intent(Intent.ACTION_VIEW, Uri.parse(UpdateCheck.RELEASES_PAGE)),
        PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    nm.notify(NOTIFICATION, new Notification.Builder(this, CHANNEL)
        .setSmallIcon(R.drawable.ic_refresh)
        .setContentTitle(getString(R.string.update_available, version))
        .setContentText(getString(R.string.update_notification_text))
        .setContentIntent(open)
        .setAutoCancel(true)
        .build());
  }
}
