// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway.engine;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;

/**
 * The JVM checks' stand-in for gomobile's Engine class: backends() is the
 * real engine's catalog, written by engine/backends_json_test.go to the file
 * named by -Ddriveway.backends (./build.sh test does both).
 */
public abstract class Engine {
  /** What backends() returns: the engine's catalog, unless a check sets its own. */
  public static String json;

  public static String backends() {
    if (json == null) {
      String path = System.getProperty("driveway.backends");
      if (path == null) throw new IllegalStateException("no catalog: run with -Ddriveway.backends=FILE");
      try {
        json = new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8);
      } catch (IOException e) {
        // Loudly: Kinds would otherwise list no services and look like a different failure.
        throw new UncheckedIOException("no catalog at " + path, e);
      }
    }
    return json;
  }
}
