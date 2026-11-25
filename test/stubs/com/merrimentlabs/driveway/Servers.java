// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import org.json.JSONObject;

/** The JVM checks' stand-in for Servers: only the fields Errors reads. */
final class Servers {
  private Servers() {}

  static final class Server {
    String label;
    String backend;
    JSONObject params = new JSONObject();
  }
}
