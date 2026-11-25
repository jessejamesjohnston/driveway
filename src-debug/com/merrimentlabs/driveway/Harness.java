// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.BroadcastReceiver;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract;
import android.provider.DocumentsContract.Document;
import android.util.Log;

import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Map;
import java.util.Random;

/**
 * Debug builds only (./build.sh debug), and only for adb: the receiver
 * requires android.permission.DUMP, which the shell has and apps can't get.
 *
 *   adb shell am broadcast -n com.merrimentlabs.driveway/.Harness -a add \
 *     --es id t-smb --es label SMB --es backend smb --es params '{...}' [--es folder share] [--es hostkey LINE]
 *   adb shell am broadcast -n com.merrimentlabs.driveway/.Harness -a run --es id t-smb
 *   adb shell am broadcast -n com.merrimentlabs.driveway/.Harness -a remove --es id t-smb
 *
 * "run" goes through Android's documents API, the way Files and other apps
 * do, and logs "HARNESS PASS id" or "HARNESS FAIL id: why" (tag Driveway).
 *
 * For the screens (tools/screens.sh): "open" shows a screen (screen=home,
 * add, form with backend/label/host/port, edit or fix with server=ID, settings,
 * licenses, license with item); "found" stands in for servers found on the
 * network (list=JSON, or "none" to look for real); "problem" marks a server's
 * card (code=signin, identity or none); "failsave" leaves a failed save of
 * NAME on a server that can't be reached; "connect" runs the connection
 * checklist without a screen and checks where it stops (expect=ok or a step);
 * "clear" removes every server.
 */
public final class Harness extends BroadcastReceiver {
  static final String TAG = "Driveway";

  /** Each action by the name adb sends (-a NAME). */
  private static final Map<String, Action> ACTIONS = new HashMap<>();

  static {
    ACTIONS.put("add", Harness::add);
    ACTIONS.put("remove", Harness::remove);
    ACTIONS.put("run", Harness::runAction);
    ACTIONS.put("create", HarnessDocs::create);
    ACTIONS.put("doc", HarnessDocs::doc);
    ACTIONS.put("save", HarnessDocs::saveAction);
    ACTIONS.put("check", HarnessDocs::checkAction);
    ACTIONS.put("bigio", HarnessDocs::bigio);
    ACTIONS.put("list", HarnessDocs::listAction);
    ACTIONS.put("copy", HarnessDocs::copyAction);
    ACTIONS.put("signin", HarnessSignIn::signin);
    ACTIONS.put("cancelsignin", HarnessSignIn::cancelSignIn);
    ACTIONS.put("refresh", HarnessSignIn::refresh);
    ACTIONS.put("show", HarnessSignIn::show);
    ACTIONS.put("servers", Harness::servers);
    ACTIONS.put("stream", HarnessDocs::stream);
    ACTIONS.put("halfwrite", HarnessDocs::halfWrite);
    ACTIONS.put("editinplace", HarnessDocs::editInPlace);
    ACTIONS.put("names", Harness::namesAction);
    ACTIONS.put("failedsaves", Harness::failedSavesAction);
    ACTIONS.put("unreachable", Harness::unreachableAction);
    ACTIONS.put("clear", Harness::clear);
    ACTIONS.put("open", Harness::openAction);
    ACTIONS.put("newlabel", Harness::newLabel);
    ACTIONS.put("found", Harness::found);
    ACTIONS.put("problem", Harness::problem);
    ACTIONS.put("failsave", Harness::failSave);
    ACTIONS.put("staleflag", Harness::staleFlagAction);
    ACTIONS.put("oldmove", Harness::oldMoveAction);
    ACTIONS.put("movekill", Harness::moveKillAction);
    ACTIONS.put("readonly", Harness::readOnlyAction);
    ACTIONS.put("connect", Harness::connectAction);
  }

  /** One harness action: it logs its own result lines. */
  interface Action {
    void run(Context context, String id, Intent intent) throws Exception;
  }

  @Override
  public void onReceive(Context context, Intent intent) {
    // No goAsync: a broadcast held for over a minute counts as not
    // responding. The harness keeps Driveway's window in front instead.
    // Except movekill's phase 2, which runs with no window by design and
    // takes seconds: held, so Android doesn't freeze the process meanwhile.
    PendingResult held = "movekill".equals(intent.getAction()) && "2".equals(intent.getStringExtra("phase"))
        ? goAsync() : null;
    new Thread(() -> {
      String id = intent.getStringExtra("id");
      try {
        Action action = ACTIONS.get(String.valueOf(intent.getAction()));
        if (action == null) {
          Log.w(TAG, Logs.clean("HARNESS unknown action " + intent.getAction()));
        } else {
          action.run(context, id, intent);
        }
      } catch (Throwable e) {
        Log.e(TAG, Logs.clean("HARNESS FAIL " + id + ": " + e + "\n" + Logs.trace(e)));
      } finally {
        if (held != null) held.finish();
      }
    }).start();
  }

  static void add(Context context, String id, Intent intent) throws Exception {
    Servers.Server s = new Servers.Server();
    s.id = id;
    s.label = intent.getStringExtra("label");
    s.backend = intent.getStringExtra("backend");
    s.params = new JSONObject(intent.getStringExtra("params"));
    String folder = intent.getStringExtra("folder");
    s.folder = folder == null ? "" : folder;
    String hostkey = intent.getStringExtra("hostkey");
    if (hostkey != null) Servers.get(context).pinHostKey(id, hostkey);
    Servers.get(context).save(s);
    Log.i(TAG, Logs.clean("HARNESS ADDED " + id));
  }

  static void remove(Context context, String id, Intent intent) throws Exception {
    Servers.get(context).remove(id);
    Log.i(TAG, Logs.clean("HARNESS REMOVED " + id));
  }

  static void runAction(Context context, String id, Intent intent) throws Exception {
    run(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /** Every saved server: id, kind, name, setting names (no values). */
  static void servers(Context context, String id, Intent intent) throws Exception {
    for (Servers.Server srv : Servers.get(context).all()) {
      Log.i(TAG, Logs.clean("HARNESS SERVER " + srv.id + " " + srv.backend + " \"" + srv.label + "\" " + srv.params.names()));
    }
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void namesAction(Context context, String id, Intent intent) throws Exception {
    names(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void failedSavesAction(Context context, String id, Intent intent) throws Exception {
    failedSaves(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void unreachableAction(Context context, String id, Intent intent) throws Exception {
    unreachable(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /** Every server goes (an emulator's state before a screen walk). */
  static void clear(Context context, String id, Intent intent) throws Exception {
    for (Servers.Server srv : Servers.get(context).all()) Servers.get(context).remove(srv.id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void openAction(Context context, String id, Intent intent) throws Exception {
    open(context, intent);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  /**
   * backend=NAME: label a service New, as an update that made it
   * available would (for screenshots and the label's checks);
   * backend=none drops every label. Logs whether the service is
   * labeled now (isnew=true/false) and checks it against expect.
   */
  static void newLabel(Context context, String id, Intent intent) throws Exception {
    String backend = intent.getStringExtra("backend");
    NewLabels.Store store = NewLabelRows.store(context);
    if ("none".equals(backend)) store.put(NewLabels.KEY_PENDING, "");
    else if (backend != null) NewLabels.markNew(store, backend);
    String ask = intent.getStringExtra("is");
    boolean ok = true;
    if (ask != null) {
      boolean now = NewLabels.isNew(store, ask);
      Log.i(TAG, Logs.clean("HARNESS NEWLABEL " + id + " " + ask + " isnew=" + now));
      ok = String.valueOf(now).equals(intent.getStringExtra("expect"));
    }
    Log.i(TAG, Logs.clean((ok ? "HARNESS PASS " : "HARNESS FAIL ") + id + (ok ? "" : ": label state differs")));
  }

  static void found(Context context, String id, Intent intent) throws Exception {
    String list = intent.getStringExtra("list");
    if (list == null || list.equals("none")) {
      Discovery.standIn = null;
    } else {
      org.json.JSONArray a = new org.json.JSONArray(list);
      java.util.List<Discovery.Found> f = new java.util.ArrayList<>();
      for (int i = 0; i < a.length(); i++) {
        JSONObject o = a.getJSONObject(i);
        f.add(new Discovery.Found(o.getString("name"), o.getString("backend"), o.getString("host"),
            o.optInt("port", 0), o.optBoolean("https")));
      }
      Discovery.standIn = f;
    }
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void problem(Context context, String id, Intent intent) throws Exception {
    String code = intent.getStringExtra("code");
    if (code == null || code.equals("none")) Problems.clear(context, id);
    else Problems.note(context, id, code);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void failSave(Context context, String id, Intent intent) throws Exception {
    String name = intent.getStringExtra("name");
    overwrite(context, id, name, 1);
    lastGen = 0;
    waitFailed(context, id, name, Uploads.unsent(context, id) + 0);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void staleFlagAction(Context context, String id, Intent intent) throws Exception {
    staleFlag(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void oldMoveAction(Context context, String id, Intent intent) throws Exception {
    oldMove(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void moveKillAction(Context context, String id, Intent intent) throws Exception {
    moveKill(context, id, intent);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void readOnlyAction(Context context, String id, Intent intent) throws Exception {
    readOnly(context, id);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void connectAction(Context context, String id, Intent intent) throws Exception {
    connect(context, id, intent);
    Log.i(TAG, Logs.clean("HARNESS PASS " + id));
  }

  static void check(boolean ok, String what) {
    if (!ok) throw new AssertionError(what);
  }

  static void run(Context ctx, String id) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    String a = Servers.AUTHORITY;

    // The server is a root.
    boolean listed = false;
    try (Cursor c = cr.query(DocumentsContract.buildRootsUri(a), null, null, null, null)) {
      while (c.moveToNext()) listed |= id.equals(c.getString(c.getColumnIndexOrThrow(DocumentsContract.Root.COLUMN_ROOT_ID)));
    }
    check(listed, "server is not a root");

    Uri top = DocumentsContract.buildDocumentUri(a, DrivewayProvider.docId(id, ""));
    String dirName = "harness-" + Long.toHexString(new Random().nextLong());
    Uri dir = DocumentsContract.createDocument(cr, top, Document.MIME_TYPE_DIR, dirName);
    check(dir != null, "create folder");
    try {
      // A new file, written the way an app saves: open "w", write, close.
      Uri file = DocumentsContract.createDocument(cr, dir, "application/octet-stream", "data.bin");
      check(file != null, "create file");
      byte[] data = new byte[5 * 1024 * 1024 + 321];
      new Random(7).nextBytes(data);
      try (ParcelFileDescriptor pfd = cr.openFileDescriptor(file, "wt");
           FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
        out.write(data);
      }
      waitForSize(cr, file, data.length);
      // Reads come from the local copy until the upload lands: wait, so these go to the server.
      waitUploaded(ctx, id, dirName + "/data.bin");

      // Read it back in pieces, jumping around like a video player.
      try (ParcelFileDescriptor pfd = cr.openFileDescriptor(file, "r");
           FileChannel ch = new FileInputStream(pfd.getFileDescriptor()).getChannel()) {
        check(ch.size() == data.length, "size " + ch.size());
        int[][] reads = {{0, 4096}, {3 * 1024 * 1024, 200000}, {100, 10}, {data.length - 77, 77}};
        for (int[] r : reads) {
          ByteBuffer buf = ByteBuffer.allocate(r[1]);
          ch.position(r[0]);
          while (buf.hasRemaining() && ch.read(buf) > 0) { }
          check(Arrays.equals(Arrays.copyOf(buf.array(), buf.position()), Arrays.copyOfRange(data, r[0], r[0] + r[1])),
              "read at " + r[0]);
        }
      }

      // Overwrite with something shorter: the new contents only.
      byte[] small = "second version".getBytes();
      try (ParcelFileDescriptor pfd = cr.openFileDescriptor(file, "wt");
           FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
        out.write(small);
      }
      waitForSize(cr, file, small.length);
      waitUploaded(ctx, id, dirName + "/data.bin");
      try (ParcelFileDescriptor pfd = cr.openFileDescriptor(file, "r");
           FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
        check(Arrays.equals(in.readAllBytes(), small), "contents after overwrite");
      }

      // A name that's taken gets " (1)".
      Uri again = DocumentsContract.createDocument(cr, dir, "text/plain", "data.bin");
      check(again != null && name(cr, again).equals("data (1).bin"), "unique name: " + (again == null ? null : name(cr, again)));

      // Rename, then move into a subfolder.
      Uri renamed = DocumentsContract.renameDocument(cr, file, "renamed.bin");
      check(renamed != null && name(cr, renamed).equals("renamed.bin"), "rename");
      Uri sub = DocumentsContract.createDocument(cr, dir, Document.MIME_TYPE_DIR, "sub");
      Uri moved = DocumentsContract.moveDocument(cr, renamed, dir, sub);
      check(moved != null && name(cr, moved).equals("renamed.bin"), "move");
      check(count(cr, sub) == 1, "sub has the moved file");
      check(DocumentsContract.isChildDocument(cr, dir, moved), "isChildDocument");

      // No temporary upload files show.
      check(count(cr, dir) == 2, "listing: " + count(cr, dir) + " entries");
    } finally {
      DocumentsContract.deleteDocument(cr, dir);
    }
    Uri topChildren = DocumentsContract.buildChildDocumentsUri(a, DrivewayProvider.docId(id, ""));
    try (Cursor c = children(cr, topChildren)) {
      while (c.moveToNext()) {
        check(!dirName.equals(c.getString(c.getColumnIndexOrThrow(Document.COLUMN_DISPLAY_NAME))), "folder deleted");
      }
    }
  }

  /** Megabyte i of the test file NAME: fixed by the name, so a later read can check it. */
  static byte[] chunk(String name, int i) {
    byte[] b = new byte[1 << 20];
    new Random(name.hashCode() * 31L + i).nextBytes(b);
    return b;
  }

  static Uri saveFile(Context ctx, String id, String name, int mb) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    boolean exists;
    try (Cursor c = cr.query(doc, null, null, null, null)) {
      exists = c != null && c.moveToFirst();
    } catch (Exception e) {
      exists = false;
    }
    if (!exists) {
      Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
      doc = DocumentsContract.createDocument(cr, top, "application/octet-stream", name);
    }
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(doc, "wt");
         FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
      for (int i = 0; i < mb; i++) out.write(chunk(name, i));
    }
    return doc;
  }

  static Uri overwrite(Context ctx, String id, String name, int mb) throws Exception {
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    try (ParcelFileDescriptor pfd = ctx.getContentResolver().openFileDescriptor(doc, "wt");
         FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
      for (int i = 0; i < mb; i++) out.write(chunk(name, i));
    }
    return doc;
  }

  static void checkFile(Context ctx, String id, String name, int mb) throws Exception {
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    try (ParcelFileDescriptor pfd = ctx.getContentResolver().openFileDescriptor(doc, "r");
         FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
      byte[] got = new byte[1 << 20];
      for (int i = 0; i < mb; i++) {
        int n = 0;
        while (n < got.length) {
          int r = in.read(got, n, got.length - n);
          check(r > 0, name + ": ends at " + ((long) i * got.length + n) + " bytes, want " + ((long) mb << 20));
          n += r;
        }
        byte[] want = chunk(name, i);
        if (!Arrays.equals(got, want)) {
          int at = 0;
          while (got[at] == want[at]) at++;
          int same = 0;
          for (int k = 0; k < got.length; k++) if (got[k] == want[k]) same++;
          throw new AssertionError(name + ": megabyte " + i + " differs from byte " + at + " (" + same
              + " of " + got.length + " bytes match); got " + hex(got, at) + " want " + hex(want, at));
        }
      }
      check(in.read() == -1, name + ": longer than " + mb + " MB");
    }
  }

  static String hex(byte[] b, int at) {
    StringBuilder sb = new StringBuilder();
    for (int k = at; k < Math.min(b.length, at + 8); k++) sb.append(String.format("%02x", b[k]));
    return sb.toString();
  }

  /** Names that trip up protocols: spaces, accents, emoji, punctuation, a long name. */
  static void names(Context ctx, String id) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    Uri dir = DocumentsContract.createDocument(cr, top, Document.MIME_TYPE_DIR, "names test " + System.currentTimeMillis());
    try {
      String[] tricky = {"Café menu #1 (draft).txt", "naïve résumé.pdf", "🚗 driveway.txt", "a+b=c & d's [copy].txt",
          "%20 not a space.txt", "semi;colon,comma.txt", "x".repeat(200) + ".txt", "日本語のファイル.txt"};
      for (String n : tricky) {
        Uri f = DocumentsContract.createDocument(cr, dir, "text/plain", n);
        check(f != null, "create " + n);
        try (ParcelFileDescriptor pfd = cr.openFileDescriptor(f, "wt");
             FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
          out.write(n.getBytes("UTF-8"));
        }
        waitForSize(cr, f, n.getBytes("UTF-8").length);
        try (ParcelFileDescriptor pfd = cr.openFileDescriptor(f, "r");
             FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
          check(new String(in.readAllBytes(), "UTF-8").equals(n), "read back " + n);
        }
        check(name(cr, f).equals(n), "name " + n + " came back as " + name(cr, f));
      }
      check(count(cr, dir) == tricky.length, "listing has " + count(cr, dir));
    } finally {
      DocumentsContract.deleteDocument(cr, dir);
    }
  }

  /**
   * Two saves of one file to a server that can't be reached both fail. The
   * file still answers from the device; Discard on the newer leaves no older
   * save behind for the next start to send over the server's version.
   */
  static void failedSaves(Context ctx, String id) throws Exception {
    String name = "failed-saves.bin";
    overwrite(ctx, id, name, 1);
    waitFailed(ctx, id, name, 1);
    overwrite(ctx, id, name, 2);
    waitFailed(ctx, id, name, 2);
    Uri doc = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    try (Cursor c = ctx.getContentResolver().query(doc, null, null, null, null)) {
      check(c != null && c.moveToFirst(), "no row for a file saved on the device");
      long size = c.getLong(c.getColumnIndexOrThrow(DocumentsContract.Document.COLUMN_SIZE));
      check(size == 2L << 20, "size " + size);
    }
    File newest = Uploads.pending(id, name);
    check(newest != null, "no pending save");
    // The server's card shows the newest failed save, with why.
    java.util.List<Uploads.Failed> shown = Uploads.failedFor(id);
    check(shown.size() == 1 && shown.get(0).file.equals(newest) && shown.get(0).why != null,
        "card shows " + shown.size() + " failed saves");
    Uploads.discard(ctx, id, name, newest);
    check(Uploads.unsent(ctx, id) == 0, "saves left after Discard: " + Uploads.unsent(ctx, id));
    check(Uploads.failedFor(id).isEmpty(), "card still shows a failed save after Discard");
    Log.i(TAG, "HARNESS failedsaves: both failed, the file answered from the device, Discard left nothing");
  }

  /**
   * A failed save's Try again and Discard belong to that save only. On a
   * server that can't be reached: save A fails; a newer save B is staged and
   * finished (as when B is handed to its upload just as A's failure is
   * recorded). The card must not offer B as failed, and its Discard must
   * not delete B.
   */
  static void staleFlag(Context ctx, String id) throws Exception {
    String name = "stale-flag.bin";
    overwrite(ctx, id, name, 1);
    lastGen = 0;
    waitFailed(ctx, id, name, Uploads.unsent(ctx, id));
    File a = Uploads.pending(id, name);
    File b = Uploads.newLocalFile(ctx);
    long gen = Uploads.nextGen();
    try {
      try (FileOutputStream out = new FileOutputStream(b)) {
        out.write(chunk(name + " B", 0));
      }
      Uploads.begin(id, name, b, gen);
      try (FileOutputStream out = new FileOutputStream(b.getPath() + ".json")) {
        out.write(new JSONObject().put("server", id).put("path", name).put("gen", gen).put("ready", true)
            .toString().getBytes(StandardCharsets.UTF_8));
      }
      for (Uploads.Failed f : Uploads.failedFor(id)) check(!f.file.equals(b), "the card offers the newer save as failed");
      Uploads.discardIfFailed(ctx, new Uploads.Failed(id, name, b, null));
      check(b.exists(), "the card's Discard deleted a newer save");
      Log.i(TAG, "HARNESS staleflag: the newer save isn't offered as failed and Discard left it");
    } finally {
      Uploads.discard(ctx, id, name, b);
      if (a != null) Uploads.discard(ctx, id, name, a);
    }
  }

  /**
   * A move Files makes by copying, refused over ten minutes ago, whose copy
   * is still being written: the original stays until the copy has reached
   * its server.
   */
  static void oldMove(Context ctx, String id) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    String name = "old-move.bin";
    Uri src = saveFile(ctx, id, name, 1);
    waitUploaded(ctx, id, name);
    java.lang.reflect.Field f = Uploads.class.getDeclaredField("refusedMoves");
    f.setAccessible(true);
    String path = DrivewayProvider.join(Servers.get(ctx).find(id).folder, name);
    // refusedMoves is private to Uploads, so the harness reaches it by
    // reflection, which returns Object; it is declared Map<String, Long>.
    @SuppressWarnings("unchecked")
    java.util.Map<String, Long> refused = (java.util.Map<String, Long>) f.get(null);
    refused.put(Uploads.key(id, path), System.currentTimeMillis() - 11 * 60_000L);
    Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    Uri copy = DocumentsContract.createDocument(cr, top, "application/octet-stream", "old-move copy.bin");
    check(copy != null, "create the copy");
    boolean deleted;
    String result;
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(copy, "wt");
         FileOutputStream out = new FileOutputStream(pfd.getFileDescriptor())) {
      out.write(chunk(name, 0));
      try {
        deleted = DocumentsContract.deleteDocument(cr, src);
        result = "deleted";
      } catch (Exception e) {
        deleted = false;
        result = e.getMessage();
      }
    }
    waitUploaded(ctx, id, DocumentsContract.getDocumentId(copy).substring(id.length() + 1));
    DocumentsContract.deleteDocument(cr, copy);
    if (!deleted) DocumentsContract.deleteDocument(cr, src);
    check(!deleted, "the original went while its copy was still being written");
    Log.i(TAG, Logs.clean("HARNESS oldmove: the original stayed: " + result));
  }

  /**
   * A service rclone can only read (a web folder): Files is offered no
   * saving, renaming or deleting there, and a write is refused before
   * anything is staged.
   */
  static void readOnly(Context ctx, String id) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    String a = Servers.AUTHORITY;
    try (Cursor c = cr.query(DocumentsContract.buildRootsUri(a), null, null, null, null)) {
      while (c.moveToNext()) {
        if (!id.equals(c.getString(c.getColumnIndexOrThrow(DocumentsContract.Root.COLUMN_ROOT_ID)))) continue;
        int flags = c.getInt(c.getColumnIndexOrThrow(DocumentsContract.Root.COLUMN_FLAGS));
        check((flags & DocumentsContract.Root.FLAG_SUPPORTS_CREATE) == 0, "root flags " + flags);
      }
    }
    Uri top = DocumentsContract.buildDocumentUri(a, DrivewayProvider.docId(id, ""));
    try (Cursor c = cr.query(top, null, null, null, null)) {
      check(c.moveToFirst(), "no top folder");
      int flags = c.getInt(c.getColumnIndexOrThrow(Document.COLUMN_FLAGS));
      check(flags == 0, "top folder flags " + flags);
    }
    String file = null;
    try (Cursor c = children(cr, DocumentsContract.buildChildDocumentsUri(a, DrivewayProvider.docId(id, "")))) {
      check(c.getCount() > 0, "nothing listed");
      while (c.moveToNext()) {
        int flags = c.getInt(c.getColumnIndexOrThrow(Document.COLUMN_FLAGS));
        String doc = c.getString(c.getColumnIndexOrThrow(Document.COLUMN_DOCUMENT_ID));
        check(flags == 0, doc + " flags " + flags);
        if (!Document.MIME_TYPE_DIR.equals(c.getString(c.getColumnIndexOrThrow(Document.COLUMN_MIME_TYPE)))) file = doc;
      }
    }
    check(file != null, "no file listed");
    int before = Uploads.unsent(ctx, id);
    boolean opened;
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(DocumentsContract.buildDocumentUri(a, file), "wt")) {
      opened = pfd != null;
    } catch (Exception e) {
      opened = false;
      Log.i(TAG, Logs.clean("HARNESS readonly: write refused: " + e.getMessage()));
    }
    Thread.sleep(500);
    check(!opened && Uploads.unsent(ctx, id) == before, "a write was " + (opened ? "opened" : "staged"));
  }

  /**
   * A move Files makes by copying, with Driveway stopped between the copy's
   * close and the delete of the original (three broadcasts, phase=1, 2, 3,
   * with `am force-stop` before 2 and Driveway's window brought back
   * before 3; host=H is where the emulator reaches the test servers).
   *
   * 1: the original is saved to the server and uploaded; its move to a
   * second server, a WebDAV server that can't be reached yet, is refused
   * (eleven minutes ago, so only the copy holds it); the copy is written
   * there and its upload fails.
   * 2: in the restarted process, before any window, the provider's delete
   * of the original is refused because the copy is still on the device.
   * 3: the second server is reachable; Try again sends the copy, the
   * refused move's record goes, and the delete goes through.
   */
  static void moveKill(Context ctx, String id, Intent intent) throws Exception {
    ContentResolver cr = ctx.getContentResolver();
    String phase = String.valueOf(intent.getStringExtra("phase"));
    String host = intent.getStringExtra("host") == null ? "10.0.2.2" : intent.getStringExtra("host");
    String name = "move-kill.bin";
    String copyName = "move-kill copy.bin";
    String dest = id + "-dest";
    // An unrelated save failing on a third server, handed over before the
    // move was refused: it must never hold the move's original, before a
    // restart or after.
    String other = id + "-other";
    String otherName = "unrelated.bin";
    String path = DrivewayProvider.join(Servers.get(ctx).find(id).folder, name);
    Uri top = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    Uri src = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(id, name));
    Uri copy = DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(dest, copyName));
    switch (phase) {
      case "1": {
        // Saves left failing on other servers by earlier checks (the screen
        // walk's) would hold this move's original too, rightly: start clean.
        for (Servers.Server any : Servers.get(ctx).all()) {
          for (Uploads.Failed f : Uploads.failedFor(any.id)) {
            Log.i(TAG, Logs.clean("HARNESS movekill: discarding a leftover failed save of " + f.path + " on " + any.id));
            Uploads.discard(ctx, f.serverId, f.path, f.file);
          }
        }
        saveDest(ctx, dest, host, 8099);
        saveDest(ctx, other, host, 8099);
        overwrite(ctx, other, otherName, 1);
        long upTo = System.currentTimeMillis() + 90_000;
        while (!Uploads.failed(other, otherName) || Uploads.running() > 0) {
          check(System.currentTimeMillis() < upTo, "the unrelated save didn't fail");
          Thread.sleep(200);
        }
        // Begun and handed over twelve minutes ago, before the refusal below.
        File otherLocal = Uploads.pending(other, otherName);
        check(otherLocal != null, "the unrelated save isn't on the device");
        File otherNote = new File(otherLocal.getPath() + ".json");
        JSONObject n = new JSONObject(new String(java.nio.file.Files.readAllBytes(otherNote.toPath()), "UTF-8"));
        check(n.optLong("queued") > 0, "a save's note doesn't keep when it was handed over");
        long before = System.currentTimeMillis() - 12 * 60_000L;
        n.put("queued", before).put("gen", before);
        java.nio.file.Files.write(otherNote.toPath(), n.toString().getBytes("UTF-8"));
        saveFile(ctx, id, name, 1);
        waitUploaded(ctx, id, name);
        Uri moved = null;
        try {
          moved = DocumentsContract.moveDocument(cr, src, top,
              DocumentsContract.buildDocumentUri(Servers.AUTHORITY, DrivewayProvider.docId(dest, "")));
        } catch (Exception e) {
          // refused, as it should be: Files copies and deletes instead
        }
        check(moved == null, "moved between servers");
        check(Uploads.moveNote(ctx, Uploads.key(id, path)).exists(), "no record of the refused move");
        // Eleven minutes ago: only the copy, not the minutes since, holds it.
        Uploads.moveRefused(ctx, id, path, System.currentTimeMillis() - 11 * 60_000L);
        overwrite(ctx, dest, copyName, 1);
        long until = System.currentTimeMillis() + 90_000;
        while (!Uploads.failed(dest, copyName) || Uploads.running() > 0) {
          check(System.currentTimeMillis() < until, "the copy's upload didn't fail");
          Thread.sleep(200);
        }
        check(deleteRefused(ctx, src), "the original went while its copy was still on the device");
        Log.i(TAG, "HARNESS movekill: the copy waits on the device; the original stays");
        break;
      }
      case "2":
        check(Uploads.movingSince(id, path) == Long.MAX_VALUE, "the refused move was known before the provider was used");
        check(deleteRefused(ctx, src), "after a restart, the original went while its copy was still on the device");
        Log.i(TAG, "HARNESS movekill: after a restart, the provider's first delete kept the original");
        break;
      case "3":
        try {
          saveDest(ctx, dest, host, 8080);
          File local = Uploads.pending(dest, copyName);
          check(local != null, "the copy is no longer on the device");
          Uploads.retry(ctx, dest, copyName, local);
          waitUploaded(ctx, dest, copyName);
          File note = Uploads.moveNote(ctx, Uploads.key(id, path));
          long until = System.currentTimeMillis() + 5000;
          while (note.exists()) {
            check(System.currentTimeMillis() < until, "the refused move's record stayed after its copy was sent");
            Thread.sleep(100);
          }
          check(Uploads.movingSince(id, path) == Long.MAX_VALUE, "the refused move still holds after its copy was sent");
          check(Uploads.failed(other, otherName), "the unrelated save should still be waiting (the check needs it)");
          check(DocumentsContract.deleteDocument(cr, src), "delete the original");
          Log.i(TAG, "HARNESS movekill: the copy reached its server and the original went, the unrelated save still waiting");
        } finally {
          try {
            DocumentsContract.deleteDocument(cr, copy);
          } catch (Exception ignored) {
            // not on the server
          }
          try {
            DocumentsContract.deleteDocument(cr, src);
          } catch (Exception ignored) {
            // gone already
          }
          Servers.get(ctx).remove(dest);
          File o = Uploads.pending(other, otherName);
          if (o != null) Uploads.discard(ctx, other, otherName, o);
          Servers.get(ctx).remove(other);
        }
        break;
      default:
        throw new AssertionError("phase=1, 2 or 3");
    }
  }

  /** The WebDAV test server, on a port: 8080 answers, 8099 doesn't. */
  static void saveDest(Context ctx, String dest, String host, int port) throws Exception {
    Servers.Server s = new Servers.Server();
    s.id = dest;
    s.label = "Move destination";
    s.backend = "webdav";
    s.params = new JSONObject().put("url", "http://" + host + ":" + port).put("vendor", "other")
        .put("user", "driveway").put("pass", "driveway-test");
    Servers.get(ctx).save(s);
  }

  /** Whether the provider refused to delete doc because a copy hasn't reached its server. */
  static boolean deleteRefused(Context ctx, Uri doc) {
    String why;
    try {
      if (DocumentsContract.deleteDocument(ctx.getContentResolver(), doc)) return false;
      why = "not deleted";
    } catch (Exception e) {
      why = String.valueOf(e.getMessage());
    }
    Log.i(TAG, Logs.clean("HARNESS movekill: delete answered: " + why));
    return why.equals(ctx.getString(R.string.err_copy_pending));
  }

  static long lastGen;

  /** Shows one of the app's screens, for screenshots and accessibility checks. */
  static void open(Context ctx, Intent in) {
    String screen = String.valueOf(in.getStringExtra("screen"));
    Intent i;
    switch (screen) {
      case "add":
        i = new Intent(ctx, ServerActivity.class);
        break;
      case "form":
        i = new Intent(ctx, ServerActivity.class)
            .putExtra(ServerActivity.EXTRA_BACKEND, in.getStringExtra("backend"))
            .putExtra(ServerActivity.EXTRA_LABEL, in.getStringExtra("label"))
            .putExtra(ServerActivity.EXTRA_HOST, in.getStringExtra("host"))
            .putExtra(ServerActivity.EXTRA_PORT, Integer.parseInt(String.valueOf(in.getStringExtra("port") == null ? "0" : in.getStringExtra("port"))));
        break;
      case "edit":
        i = new Intent(ctx, ServerActivity.class).putExtra(ServerActivity.EXTRA_ID, in.getStringExtra("server"));
        break;
      case "fix":
        i = new Intent(ctx, ServerActivity.class).putExtra(ServerActivity.EXTRA_ID, in.getStringExtra("server"))
            .putExtra(ServerActivity.EXTRA_FIX, in.getStringExtra("fix"));
        break;
      case "settings":
        i = new Intent(ctx, SettingsActivity.class);
        break;
      case "licenses":
        i = new Intent(ctx, LicensesActivity.class);
        break;
      case "license":
        i = new Intent(ctx, LicensesActivity.class).putExtra(LicensesActivity.EXTRA_ITEM,
            Integer.parseInt(String.valueOf(in.getStringExtra("item"))));
        break;
      default:
        i = new Intent(ctx, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP);
    }
    ctx.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
  }

  /**
   * The connection checklist without a screen: trusts a first identity,
   * answers setup questions with their defaults, and checks the step it
   * stops at (expect=ok, or found, identity, signin, folder).
   */
  static void connect(Context ctx, String id, Intent in) throws Exception {
    Servers.Server s = new Servers.Server();
    s.id = id;
    s.label = in.getStringExtra("label") == null ? id : in.getStringExtra("label");
    s.backend = in.getStringExtra("backend");
    s.params = new JSONObject(in.getStringExtra("params"));
    String folder = in.getStringExtra("folder");
    s.folder = folder == null ? "" : folder;
    Kinds.Kind k = Kinds.of(s.backend);
    check(k != null, "no kind " + s.backend);
    Connection[] holder = new Connection[1];
    Connection run = new Connection(ctx, s, k, true, false, false);
    holder[0] = run;
    run.attach(() -> {
      Connection r = holder[0];
      if (r.ask == Connection.Ask.IDENTITY) r.trust(true);
      if (r.ask == Connection.Ask.QUESTION && r.question != null) r.answer(r.question.optString("default"));
    });
    long t0 = System.currentTimeMillis();
    run.start();
    while (run.running()) {
      check(System.currentTimeMillis() - t0 < 90_000, "still running after 90 s");
      Thread.sleep(100);
    }
    StringBuilder steps = new StringBuilder();
    String stopped = "ok";
    for (Connection.Step st : run.steps) {
      steps.append(' ').append(st.id).append('=').append(st.state);
      if (st.state == Connection.State.FAILED) stopped = st.id;
    }
    Log.i(TAG, Logs.clean("HARNESS CONNECT " + id + " " + run.outcome + steps + " in " + (System.currentTimeMillis() - t0) + " ms"
        + (run.error != null ? ": " + run.error : "")));
    String expect = in.getStringExtra("expect") == null ? "ok" : in.getStringExtra("expect");
    check(expect.equals(stopped), "stopped at " + stopped + ", expected " + expect);
    if (expect.equals("ok")) check(run.outcome == Connection.Outcome.CONNECTED, "outcome " + run.outcome);
    // A screen-less run leaves nothing behind for the next one.
    Servers.get(ctx).knownHosts(id).delete();
    com.merrimentlabs.driveway.engine.Engine.removeRemote(id);
  }

  /** Until the file's save has failed with n saves of it kept on the device (Android reports a close late). */
  static void waitFailed(Context ctx, String id, String name, int n) throws InterruptedException {
    long until = System.currentTimeMillis() + 90_000;
    // First this save's own upload is queued (its note says ready; the
    // earlier save's failure is still marked until then), then it fails.
    while (true) {
      File f = Uploads.pending(id, name);
      JSONObject note = f == null ? null : Uploads.note(f);
      if (note != null && note.optBoolean("ready") && Uploads.genOf(f) > lastGen) {
        lastGen = Uploads.genOf(f);
        break;
      }
      check(System.currentTimeMillis() < until, "save " + n + " was never queued");
      Thread.sleep(50);
    }
    Thread.sleep(300);
    while (!(Uploads.unsent(ctx, id) == n && Uploads.running() == 0 && Uploads.failed(id, name))) {
      check(System.currentTimeMillis() < until, "save " + n + " didn't fail as expected: " + Uploads.unsent(ctx, id)
          + " kept, " + Uploads.running() + " running, failed " + Uploads.failed(id, name));
      Thread.sleep(200);
    }
  }

  /** A server that can't be reached says so in the listing, instead of looking empty. */
  static void unreachable(Context ctx, String id) throws InterruptedException {
    Uri children = DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, DrivewayProvider.docId(id, ""));
    try (Cursor c = children(ctx.getContentResolver(), children)) {
      Bundle extras = c.getExtras();
      String error = extras == null ? null : extras.getString(DocumentsContract.EXTRA_ERROR);
      check(c.getCount() == 0 && error != null && error.startsWith("Can't reach"), "error: " + error);
      Log.i(TAG, Logs.clean("HARNESS unreachable says: " + error));
    }
  }

  /** A folder listing once it has loaded (listings answer "loading" first). */
  static Cursor children(ContentResolver cr, Uri childrenUri) throws InterruptedException {
    long until = System.currentTimeMillis() + 60_000;
    while (true) {
      Cursor c = cr.query(childrenUri, null, null, null, null);
      Bundle x = c == null ? null : c.getExtras();
      if (c != null && (x == null || !x.getBoolean(DocumentsContract.EXTRA_LOADING))) return c;
      if (c != null) c.close();
      if (System.currentTimeMillis() > until) throw new AssertionError("listing never loaded: " + childrenUri);
      Thread.sleep(200);
    }
  }

  /** Until the server has the save (reads come from the local copy before that). */
  static void waitUploaded(Context ctx, String id, String name) throws InterruptedException {
    Servers.Server srv = Servers.get(ctx).find(id);
    String path = DrivewayProvider.join(srv.folder, name);
    long until = System.currentTimeMillis() + 600_000;
    Thread.sleep(200); // Android reports the close to Driveway a moment after the app's close returns
    while (Uploads.pending(id, path) != null || Uploads.running() > 0) {
      if (System.currentTimeMillis() > until) throw new AssertionError("upload of " + name + " never finished");
      Thread.sleep(50);
    }
  }

  static void waitForSize(ContentResolver cr, Uri file, long size) throws Exception {
    long until = System.currentTimeMillis() + 60_000;
    while (System.currentTimeMillis() < until) {
      try (Cursor c = cr.query(file, new String[] {Document.COLUMN_SIZE}, null, null, null)) {
        if (c != null && c.moveToFirst() && c.getLong(0) == size) return;
      } catch (Exception ignored) {
        // not there yet
      }
      Thread.sleep(250);
    }
    throw new AssertionError("upload didn't finish: " + file);
  }

  static String readAll(ContentResolver cr, Uri doc) throws Exception {
    try (ParcelFileDescriptor pfd = cr.openFileDescriptor(doc, "r");
         FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
      return new String(in.readAllBytes(), StandardCharsets.UTF_8);
    }
  }

  static String name(ContentResolver cr, Uri doc) {
    try (Cursor c = cr.query(doc, new String[] {Document.COLUMN_DISPLAY_NAME}, null, null, null)) {
      return c != null && c.moveToFirst() ? c.getString(0) : null;
    }
  }

  static int count(ContentResolver cr, Uri dir) throws InterruptedException {
    Uri uri = DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, DocumentsContract.getDocumentId(dir));
    try (Cursor c = children(cr, uri)) {
      return c == null ? -1 : c.getCount();
    }
  }
}
