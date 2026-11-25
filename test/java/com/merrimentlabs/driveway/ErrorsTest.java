// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

/**
 * Reading the engine's errors, on a plain JVM (./build.sh test): the category
 * in brackets and the detail after it. Turning them into sentences
 * (explain()) needs Android's resources and is checked on the emulator.
 */
public final class ErrorsTest {
  private static int failures;

  private static void check(boolean ok, String what) {
    System.out.println((ok ? "PASS " : "FAIL ") + what);
    if (!ok) failures++;
  }

  private static Exception err(String message) {
    return new Exception(message);
  }

  public static void main(String[] args) {
    // kind()
    check(Errors.kind(err("[network] dial tcp 10.0.0.1:22: i/o timeout")).equals("network"), "kind: the category");
    check(Errors.kind(err("[notfound]")).equals("notfound"), "kind: a category alone");
    check(Errors.kind(err("dial tcp: refused")).equals("other"), "kind: none is other");
    check(Errors.kind(err("bad [auth] here")).equals("other"), "kind: only at the start");
    check(Errors.kind(err("[auth no end")).equals("other"), "kind: an unclosed bracket is other");
    check(Errors.kind(err(null)).equals("other") && Errors.kind(new RuntimeException()).equals("other"),
        "kind: no message is other");
    check(Errors.kind(err("[auth] one ] two")).equals("auth"), "kind: up to the first closing bracket");

    // detail()
    check(Errors.detail(err("[auth]   wrong password  ")).equals("wrong password"), "detail: after the category, trimmed");
    check(Errors.detail(err("[exists]")).isEmpty(), "detail: a category alone leaves nothing");
    check(Errors.detail(err(" plain message ")).equals(" plain message "), "detail: no category, the message as it is");
    check(Errors.detail(err("[unclosed message")).equals("[unclosed message"), "detail: an unclosed bracket stays");
    check(Errors.detail(err("bad [auth] here")).equals("bad [auth] here"), "detail: brackets later on stay");
    check(Errors.detail(new IllegalStateException()).equals("java.lang.IllegalStateException"),
        "detail: no message, the error's name");

    // isNotFound()
    check(Errors.isNotFound(err("[notfound] photos/a.jpg")), "isNotFound: notfound");
    check(!Errors.isNotFound(err("[network] notfound")) && !Errors.isNotFound(err("notfound")) && !Errors.isNotFound(err(null)),
        "isNotFound: anything else");

    System.out.println(failures == 0 ? "ErrorsTest: all passed" : "ErrorsTest: " + failures + " failed");
    if (failures > 0) System.exit(1);
  }
}
