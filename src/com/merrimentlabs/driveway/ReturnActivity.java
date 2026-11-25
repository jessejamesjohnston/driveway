// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;

/**
 * "Return to Driveway" on the browser page a sign-in comes back to
 * (engine/pkce.go). Opened from the browser in Driveway's task, it closes at
 * once, so the screen under it, the sign-in waiting to finish, is what shows.
 * With no Driveway screen open (Driveway was closed meanwhile), it opens the
 * server list instead. It reads nothing from the link.
 */
public final class ReturnActivity extends Activity {
  @Override
  protected void onCreate(Bundle state) {
    super.onCreate(state);
    if (isTaskRoot()) {
      startActivity(new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
    }
    finish();
    overrideActivityTransition(OVERRIDE_TRANSITION_CLOSE, 0, 0);
  }
}
