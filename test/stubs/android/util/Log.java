// SPDX-License-Identifier: Apache-2.0
package android.util;

/**
 * The JVM checks' stand-in for Android's Log (android.jar's throws "Stub!"):
 * it keeps the last message so a check can see what was logged.
 */
public final class Log {
  public static String lastTag, lastMessage;
  public static Throwable lastError;

  private Log() {}

  public static int e(String tag, String msg, Throwable tr) {
    lastTag = tag;
    lastMessage = msg;
    lastError = tr;
    return 0;
  }

  public static int w(String tag, String msg, Throwable tr) {
    lastTag = tag;
    lastMessage = msg;
    lastError = tr;
    return 0;
  }

  public static int w(String tag, String msg) {
    lastTag = tag;
    lastMessage = msg;
    lastError = null;
    return 0;
  }
}
