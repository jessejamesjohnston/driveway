// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

/** The JVM checks' stand-in for MainActivity, so Errors compiles (explain() isn't run on the JVM). */
final class MainActivity {
  private MainActivity() {}

  static boolean hasLocalNetwork(android.content.Context c) {
    throw new UnsupportedOperationException("needs Android");
  }
}
