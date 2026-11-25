// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import static com.merrimentlabs.driveway.Harness.*;

import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract;
import android.util.Log;

import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;

/**
 * The harness's actions that go through Android's documents API one call
 * at a time, as Files and other apps do: create, open, read, write, copy,
 * list and the provider's other calls, each timed and logged. Debug builds
 * only (see Harness).
 */
final class HarnessDocs {
  private HarnessDocs() {}

  /** Times a create on the server's top folder (a save dialog's "Save"). */
  static void create(Context context, String id, Intent intent) throws Exception {
    long t0 = System.currentTimeMillis();
    String result;
    boolean ok = false;
    try {
      Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
      Uri made = DocumentsContract.createDocument(context.getContentResolver(), top, "text/plain", "timed.txt");
      result = String.valueOf(made);
      ok = made != null;
    } catch (Exception e) {
      result = e.toString();
    }
    Log.i(TAG, Logs.clean("HARNESS CREATE " + id + " took " + (System.currentTimeMillis() - t0) + " ms: " + Logs.clean(result)));
    Log.i(TAG, Logs.clean((ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": create failed")));
  }

  /**
   * One provider call as Files makes it: OP is delete, rename, move
   * or read; PATH is from the server's top folder; TO is the new
   * name (rename) or the target folder (move).
   */
  static void doc(Context context, String id, Intent intent) throws Exception {
    String op = intent.getStringExtra("op");
    String path = intent.getStringExtra("path");
    String to = intent.getStringExtra("to");
    ContentResolver cr = context.getContentResolver();
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, path));
    String parent = path.contains("/") ? path.substring(0, path.lastIndexOf('/')) : "";
    long t0 = System.currentTimeMillis();
    String result;
    boolean ok;
    try {
      switch (String.valueOf(op)) {
        case "delete":
          ok = DocumentsContract.deleteDocument(cr, doc);
          result = "deleted";
          break;
        case "rename":
          Uri renamed = DocumentsContract.renameDocument(cr, doc, to);
          ok = renamed != null;
          result = String.valueOf(renamed);
          break;
        case "move":
          Uri moved = DocumentsContract.moveDocument(cr, doc,
              DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, parent)),
              DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, to)));
          ok = moved != null;
          result = String.valueOf(moved);
          break;
        case "read":
          result = readAll(cr, doc).length() + " characters";
          ok = true;
          break;
        case "rw": {
          // Opened to edit in place, read, closed unchanged: the
          // server answered, so its card asks for nothing after.
          int n;
          try (ParcelFileDescriptor pfd = cr.openFileDescriptor(doc, "rw");
               FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
            n = in.readAllBytes().length;
          }
          String problem = Problems.of(context, id);
          result = n + " bytes" + (problem == null ? "" : ", the card still says " + problem);
          ok = problem == null;
          break;
        }
        default:
          throw new IllegalArgumentException("op " + op);
      }
    } catch (Exception e) {
      ok = false;
      result = e.toString();
    }
    Log.i(TAG, Logs.clean("HARNESS DOC " + id + " " + op + " " + path + " took " + (System.currentTimeMillis() - t0) + " ms: " + Logs.clean(result)));
    Log.i(TAG, Logs.clean((ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": " + Logs.clean(result))));
  }

  /** Saves NAME (MB megabytes, contents fixed by the name) into the server's top folder; doesn't wait for the upload. */
  static void saveAction(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    int mb = Integer.parseInt(intent.getStringExtra("mb"));
    long t0 = System.currentTimeMillis();
    // overwrite=1: write over the file's document as an app with it open would, without looking it up.
    Uri doc = "1".equals(intent.getStringExtra("overwrite"))
        ? overwrite(context, id, name, mb) : saveFile(context, id, name, mb);
    Log.i(TAG, Logs.clean("HARNESS SAVED " + id + " " + name + " " + mb + " MB locally in " + (System.currentTimeMillis() - t0) + " ms: " + doc));
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /** Reads NAME back and compares it with what "save" wrote; logs throughput. */
  static void checkAction(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    int mb = Integer.parseInt(intent.getStringExtra("mb"));
    if ("1".equals(intent.getStringExtra("wait"))) waitUploaded(context, id, name);
    long t0 = System.currentTimeMillis();
    checkFile(context, id, name, mb);
    long ms = System.currentTimeMillis() - t0;
    Log.i(TAG, Logs.clean("HARNESS READ " + id + " " + name + " " + mb + " MB in " + ms + " ms (" + (mb * 1000L / Math.max(ms, 1)) + " MB/s)"));
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /** Save MB megabytes, wait for the upload, read it all back, compare. */
  static void bigio(Context context, String id, Intent intent) throws Exception {
    int mb = Integer.parseInt(intent.getStringExtra("mb"));
    String name = "big-" + mb + ".bin";
    try {
      // Start from no file, so the size check waits for this upload.
      DocumentsContract.deleteDocument(context.getContentResolver(),
          DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name)));
    } catch (Exception ignored) {
    }
    long t0 = System.currentTimeMillis();
    Uri doc = saveFile(context, id, name, mb);
    long saved = System.currentTimeMillis();
    waitUploaded(context, id, name);
    long uploaded = System.currentTimeMillis();
    checkFile(context, id, name, mb);
    long read = System.currentTimeMillis();
    Log.i(TAG, Logs.clean("HARNESS BIGIO " + id + " " + mb + " MB: local save " + (saved - t0) + " ms, upload "
        + (uploaded - saved) + " ms (" + (mb * 1000L / Math.max(uploaded - saved, 1)) + " MB/s), read back "
        + (read - uploaded) + " ms (" + (mb * 1000L / Math.max(read - uploaded, 1)) + " MB/s)"));
    DocumentsContract.deleteDocument(context.getContentResolver(), doc);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /** Times a listing of PATH until it has loaded. */
  static void listAction(Context context, String id, Intent intent) throws Exception {
    String path = intent.getStringExtra("path");
    Uri uri = DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, DrivewayProvider.docId(id, path == null ? "" : path));
    long t0 = System.currentTimeMillis();
    String error;
    int n;
    try (Cursor c = children(context.getContentResolver(), uri)) {
      n = c.getCount();
      Bundle x = c.getExtras();
      error = x == null ? null : x.getString(DocumentsContract.EXTRA_ERROR);
    }
    Log.i(TAG, Logs.clean("HARNESS LIST " + id + " " + path + ": " + n + " entries in " + (System.currentTimeMillis() - t0) + " ms" + (error == null ? "" : ", error: " + Logs.clean(error))));
    Log.i(TAG, Logs.clean((error == null ? "HARNESS PASS " : "HARNESS FAIL ") + id + (error == null ? "" : ": the listing has an error")));
  }

  /** Copies NAME to "NAME copy" in the same folder through copyDocument, timing it. */
  static void copyAction(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    ContentResolver cr = context.getContentResolver();
    Uri src = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    long t0 = System.currentTimeMillis();
    String result;
    boolean ok = true;
    try {
      Uri copy = DocumentsContract.copyDocument(cr, src, top);
      if (copy == null) throw new IllegalStateException("copyDocument returned nothing");
      result = "server-side copy " + name(cr, copy) + " in " + (System.currentTimeMillis() - t0) + " ms";
      DocumentsContract.deleteDocument(cr, copy);
    } catch (UnsupportedOperationException e) {
      // The provider's "copy it yourself": Files then copies the ordinary way.
      result = "no server-side copy (Files would copy the ordinary way): " + e.getMessage();
    } catch (Exception e) {
      result = "copy failed: " + e;
      ok = false;
    }
    Log.i(TAG, Logs.clean("HARNESS COPY " + id + " " + Logs.clean(result)));
    Log.i(TAG, Logs.clean((ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": " + Logs.clean(result))));
  }

  /**
   * A read open must stream: return at once, stage nothing on the
   * device, read the file's end without the rest, refuse writes.
   */
  static void stream(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    int mb = Integer.parseInt(intent.getStringExtra("mb"));
    java.io.File staging = new java.io.File(context.getNoBackupFilesDir(), "uploads");
    String[] before = staging.list();
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    long t0 = System.currentTimeMillis();
    try (ParcelFileDescriptor pfd = context.getContentResolver().openFileDescriptor(doc, "r")) {
      long opened = System.currentTimeMillis() - t0;
      String[] after = staging.list();
      check((after == null ? 0 : after.length) == (before == null ? 0 : before.length), "a read staged a copy");
      FileChannel ch = new FileInputStream(pfd.getFileDescriptor()).getChannel();
      ByteBuffer last = ByteBuffer.allocate(1 << 20);
      ch.position((long) (mb - 1) << 20);
      while (last.hasRemaining() && ch.read(last) > 0) { }
      check(Arrays.equals(last.array(), chunk(name, mb - 1)), "the last megabyte differs");
      long readEnd = System.currentTimeMillis() - t0;
      boolean refused;
      try (FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
        out.write(1);
        refused = false;
      } catch (java.io.IOException e) {
        refused = true;
      }
      check(refused, "a read-only open accepted a write");
      Log.i(TAG, Logs.clean("HARNESS STREAM " + id + " " + name + ": opened in " + opened + " ms, last MB of " + mb
          + " read by " + readEnd + " ms, write refused"));
    }
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /**
   * Opens NAME to replace it, writes 1 MB of a planned many, and
   * holds the file open, so Driveway can be killed mid-save.
   */
  static void halfWrite(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    ParcelFileDescriptor pfd = context.getContentResolver().openFileDescriptor(doc, "wt");
    FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor());
    out.write(new byte[1 << 20]);
    out.flush();
    Log.i(TAG, Logs.clean("HARNESS HALFWRITE " + id + " holding " + name + " open"));
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
    Thread.sleep(600_000);
  }

  /**
   * An edit in place ("rw"), then a reopen at once: the reopen must
   * see the edit, before and after the upload.
   */
  static void editInPlace(Context context, String id, Intent intent) throws Exception {
    ContentResolver cr = context.getContentResolver();
    Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    Uri doc = DocumentsContract.createDocument(cr, top, "text/plain", "edit-in-place.txt");
    check(doc != null, "create");
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(doc, "wt");
         FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
      out.write("first version".getBytes(StandardCharsets.UTF_8));
    }
    waitForSize(cr, doc, 13);
    Thread.sleep(3000); // let the first upload land
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(doc, "rw");
         FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
      out.write("FIRST".getBytes(StandardCharsets.UTF_8));
    }
    String want = "FIRST version";
    String now = readAll(cr, doc);
    check(now.equals(want), "reopen right after the edit read \"" + now + "\"");
    Thread.sleep(5000); // the upload
    now = readAll(cr, doc);
    check(now.equals(want), "after the upload read \"" + now + "\"");
    DocumentsContract.deleteDocument(cr, doc);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }
}
