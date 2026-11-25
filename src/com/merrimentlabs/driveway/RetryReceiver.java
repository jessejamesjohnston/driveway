// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

import java.io.File;

/** "Try again" and "Discard" on a failed upload's notification. */
public final class RetryReceiver extends BroadcastReceiver {
  static final String DISCARD = "com.merrimentlabs.driveway.DISCARD";

  @Override
  public void onReceive(Context context, Intent intent) {
    String server = intent.getStringExtra(Uploads.EXTRA_SERVER);
    String path = intent.getStringExtra(Uploads.EXTRA_PATH);
    String local = intent.getStringExtra(Uploads.EXTRA_LOCAL);
    if (server == null || path == null || local == null) return;
    File f = new File(local);
    // Only files Driveway itself kept for an upload.
    File dir = new File(context.getNoBackupFilesDir(), "uploads");
    if (!dir.equals(f.getParentFile())) return;
    if (DISCARD.equals(intent.getAction())) {
      Uploads.discard(context, server, path, f);
      return;
    }
    // A notification action may start the foreground service, which keeps
    // Driveway running for as long as the upload takes. The save keeps the
    // generation it was made with, so it can't overtake a newer one.
    Uploads.retry(context, server, path, f);
  }
}
