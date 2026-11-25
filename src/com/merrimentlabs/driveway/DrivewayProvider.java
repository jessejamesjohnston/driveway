// SPDX-License-Identifier: Apache-2.0
package com.merrimentlabs.driveway;

import android.content.Context;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.os.Bundle;
import android.os.CancellationSignal;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.ParcelFileDescriptor;
import android.os.ProxyFileDescriptorCallback;
import android.os.SystemClock;
import android.os.storage.StorageManager;
import android.provider.DocumentsContract;
import android.provider.DocumentsContract.Document;
import android.provider.DocumentsContract.Root;
import android.provider.DocumentsProvider;
import android.system.ErrnoException;
import android.system.OsConstants;
import android.webkit.MimeTypeMap;

import com.merrimentlabs.driveway.engine.Engine;
import com.merrimentlabs.driveway.engine.Reader;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileNotFoundException;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Puts every server in the Files sidebar and in every app's open and save
 * dialogs. A document id is "serverId:path", with the path relative to the
 * folder the server was set up to start in.
 *
 * Reads stream from the server on demand, so a video can seek and a PDF can
 * jump to a page without downloading the whole file. Writes go to a local
 * file first and are uploaded when the app closes it (Uploads), because
 * Android tells us about the close only after the app has moved on.
 */
public final class DrivewayProvider extends DocumentsProvider {

  private static final String[] ROOT_COLUMNS = {
      Root.COLUMN_ROOT_ID, Root.COLUMN_DOCUMENT_ID, Root.COLUMN_TITLE, Root.COLUMN_SUMMARY,
      Root.COLUMN_FLAGS, Root.COLUMN_ICON, Root.COLUMN_MIME_TYPES,
  };
  private static final String[] DOC_COLUMNS = {
      Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE,
      Document.COLUMN_SIZE, Document.COLUMN_LAST_MODIFIED, Document.COLUMN_FLAGS,
  };
  private static final int DIR_FLAGS = Document.FLAG_DIR_SUPPORTS_CREATE | Document.FLAG_SUPPORTS_DELETE
      | Document.FLAG_SUPPORTS_RENAME | Document.FLAG_SUPPORTS_MOVE;
  private static final int FILE_FLAGS = Document.FLAG_SUPPORTS_WRITE | Document.FLAG_SUPPORTS_DELETE
      | Document.FLAG_SUPPORTS_RENAME | Document.FLAG_SUPPORTS_MOVE | Document.FLAG_SUPPORTS_COPY;

  /** Room left on the device below which Driveway won't stage a save. */
  static final long RESERVE_BYTES = 64L << 20;

  /** A folder's contents as last fetched, or why they couldn't be. */
  private static final class Listing {
    final JSONArray entries;
    final String error;
    final long at = System.currentTimeMillis();

    Listing(JSONArray entries, String error) {
      this.entries = entries;
      this.error = error;
    }
  }

  /** How long a listing is shown without asking the server again. */
  private static final long FRESH_MS = 5000;

  private final Map<String, Listing> listings = new ConcurrentHashMap<>();
  private final Set<String> loading = ConcurrentHashMap.newKeySet();
  private final ExecutorService io = Executors.newCachedThreadPool();

  @Override
  public boolean onCreate() {
    return true; // the engine loads on first use, not when Files lists providers
  }

  private Servers servers() {
    // Saves left from a Driveway that was stopped go out on any first use,
    // not only when Files lists the servers.
    Uploads.recover(getContext());
    return Servers.get(getContext());
  }

  // Document ids.

  static String docId(String serverId, String path) {
    return serverId + ":" + path;
  }

  private static String serverOf(String docId) {
    int i = docId.indexOf(':');
    return i < 0 ? docId : docId.substring(0, i);
  }

  private static String pathOf(String docId) {
    int i = docId.indexOf(':');
    return i < 0 ? "" : docId.substring(i + 1);
  }

  static String join(String a, String b) {
    if (a == null || a.isEmpty()) return b;
    if (b == null || b.isEmpty()) return a;
    return a + "/" + b;
  }

  private static String parentOf(String path) {
    int i = path.lastIndexOf('/');
    return i < 0 ? "" : path.substring(0, i);
  }

  private static String nameOf(String path) {
    return path.substring(path.lastIndexOf('/') + 1);
  }

  /**
   * A path made only of real names: no empty, "." or ".." parts, so no
   * document id can reach outside the folder a server starts in, and a
   * grant to one folder can't be stretched to its neighbors.
   */
  static boolean canonical(String path) {
    if (path.isEmpty()) return true;
    for (String part : path.split("/", -1)) {
      if (part.isEmpty() || part.equals(".") || part.equals("..") || part.indexOf('\0') >= 0) return false;
    }
    return true;
  }

  /** A name an app may give a new or renamed file. */
  private static void checkName(String name) throws FileNotFoundException {
    if (name == null || name.isEmpty() || name.equals(".") || name.equals("..") || name.contains("/")
        || name.indexOf('\0') >= 0 || name.startsWith(".driveway-")) {
      throw new FileNotFoundException("Can't use that name: " + name);
    }
  }

  private Servers.Server server(String docId) throws FileNotFoundException {
    if (!canonical(pathOf(docId))) throw new FileNotFoundException("Not a document: " + docId);
    Servers.Server s = servers().find(serverOf(docId));
    if (s == null) throw new FileNotFoundException("That server was removed from Driveway");
    return s;
  }

  /** The path on the server. */
  private static String remotePath(Servers.Server s, String docId) {
    return join(s.folder, pathOf(docId));
  }

  // Roots and listings.

  @Override
  public Cursor queryRoots(String[] projection) {
    Uploads.recover(getContext());
    MatrixCursor c = new MatrixCursor(projection != null ? projection : ROOT_COLUMNS);
    for (Servers.Server s : servers().all()) {
      if (Kinds.removed(s.backend)) continue; // the engine can't open it
      c.newRow()
          .add(Root.COLUMN_ROOT_ID, s.id)
          .add(Root.COLUMN_DOCUMENT_ID, docId(s.id, ""))
          .add(Root.COLUMN_TITLE, s.label)
          .add(Root.COLUMN_SUMMARY, s.summary())
          .add(Root.COLUMN_FLAGS, Kinds.readOnly(s.backend) ? Root.FLAG_SUPPORTS_IS_CHILD
              : Root.FLAG_SUPPORTS_CREATE | Root.FLAG_SUPPORTS_IS_CHILD)
          .add(Root.COLUMN_ICON, R.drawable.ic_server)
          .add(Root.COLUMN_MIME_TYPES, "*/*");
    }
    c.setNotificationUri(getContext().getContentResolver(), DocumentsContract.buildRootsUri(Servers.AUTHORITY));
    return c;
  }

  @Override
  public Cursor queryDocument(String documentId, String[] projection) throws FileNotFoundException {
    Servers.Server s = server(documentId);
    MatrixCursor c = new MatrixCursor(projection != null ? projection : DOC_COLUMNS);
    if (pathOf(documentId).isEmpty()) {
      c.newRow()
          .add(Document.COLUMN_DOCUMENT_ID, documentId)
          .add(Document.COLUMN_DISPLAY_NAME, s.label)
          .add(Document.COLUMN_MIME_TYPE, Document.MIME_TYPE_DIR)
          // An SMB server's top level is its list of shares, and a read-only
          // service holds nothing new: nothing can be saved there.
          .add(Document.COLUMN_FLAGS, s.backend.equals("smb") && s.folder.isEmpty() || Kinds.readOnly(s.backend)
              ? 0 : Document.FLAG_DIR_SUPPORTS_CREATE);
      return c;
    }
    String path = pathOf(documentId);
    Listing l = listings.get(docId(s.id, parentOf(path)));
    if (l != null && l.entries != null && System.currentTimeMillis() - l.at < FRESH_MS) {
      for (int i = 0; i < l.entries.length(); i++) {
        JSONObject e = l.entries.optJSONObject(i);
        if (e.optString("name").equals(nameOf(path))) {
          addRow(c, documentId, overlay(s, path, e), s);
          return c;
        }
      }
    }
    try {
      addRow(c, documentId, overlay(s, path, new JSONObject(Engine.stat(s.id, remotePath(s, documentId)))), s);
    } catch (Exception e) {
      // A save still on the device answers for its file while the server
      // can't (it's offline, or the upload failed): the copy reads fine.
      File p = Uploads.pending(s.id, join(s.folder, path));
      if (p == null) throw notFound(s, e);
      try {
        addRow(c, documentId, new JSONObject().put("name", nameOf(path)).put("size", p.length())
            .put("mtime", p.lastModified()), s);
      } catch (org.json.JSONException je) {
        throw notFound(s, e);
      }
    }
    return c;
  }

  /** A file with a save still uploading shows the saved size and time. */
  private static JSONObject overlay(Servers.Server s, String path, JSONObject e) {
    File p = Uploads.pending(s.id, join(s.folder, path));
    if (p == null || e.optBoolean("dir")) return e;
    try {
      return new JSONObject(e.toString()).put("size", p.length()).put("mtime", p.lastModified());
    } catch (org.json.JSONException ex) {
      return e;
    }
  }

  private static void addRow(MatrixCursor c, String docId, JSONObject e, Servers.Server s) {
    boolean dir = e.optBoolean("dir");
    String name = e.optString("name");
    long mtime = e.optLong("mtime");
    c.newRow()
        .add(Document.COLUMN_DOCUMENT_ID, docId)
        .add(Document.COLUMN_DISPLAY_NAME, name)
        .add(Document.COLUMN_MIME_TYPE, dir ? Document.MIME_TYPE_DIR : mimeOf(name, e.optString("mime")))
        // (-1: the service doesn't know until the file is made, e.g. a Google Docs document.)
        .add(Document.COLUMN_SIZE, dir || e.optLong("size") < 0 ? null : e.optLong("size"))
        .add(Document.COLUMN_LAST_MODIFIED, mtime > 0 ? mtime : null)
        // A read-only service: reading and copying elsewhere is all there is.
        .add(Document.COLUMN_FLAGS, Kinds.readOnly(s.backend) ? 0 : dir ? DIR_FLAGS : FILE_FLAGS);
  }

  static String mimeOf(String name, String fallback) {
    int dot = name.lastIndexOf('.');
    if (dot >= 0) {
      String m = MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substring(dot + 1).toLowerCase());
      if (m != null) return m;
    }
    return fallback != null && !fallback.isEmpty() ? fallback : "application/octet-stream";
  }

  /**
   * Files gives a provider about 20 seconds to answer before it declares it
   * not responding and Android kills it, and a server can take longer than
   * that. So a listing answers at once with what it has (nothing, the first
   * time) marked as loading, fetches in the background, and tells Files to
   * ask again when the contents arrive.
   */
  @Override
  public Cursor queryChildDocuments(String parentDocumentId, String[] projection, String sortOrder)
      throws FileNotFoundException {
    SessionService.touch(getContext());
    Servers.Server s = server(parentDocumentId);
    MatrixCursor c = new MatrixCursor(projection != null ? projection : DOC_COLUMNS);
    c.setNotificationUri(getContext().getContentResolver(),
        DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, parentDocumentId));
    Listing l = listings.get(parentDocumentId);
    boolean fresh = l != null && System.currentTimeMillis() - l.at < FRESH_MS;
    if (!fresh) load(s, parentDocumentId);
    Bundle extras = new Bundle();
    if (!fresh) extras.putBoolean(DocumentsContract.EXTRA_LOADING, true);
    if (l != null && l.error != null) {
      // Shown above the list, so a server that can't be reached never looks like an empty folder.
      extras.putString(DocumentsContract.EXTRA_ERROR, l.error);
    }
    if (l != null && l.entries != null) {
      String parent = pathOf(parentDocumentId);
      for (int i = 0; i < l.entries.length(); i++) {
        JSONObject e = l.entries.optJSONObject(i);
        String name = e.optString("name");
        // An upload in progress (but a ".driveway-old-" original stranded by
        // an interrupted replace stays visible, so it can be found).
        if (name.startsWith(".driveway-") && !name.startsWith(".driveway-old-")) continue;
        addRow(c, docId(s.id, join(parent, e.optString("name"))), overlay(s, join(parent, e.optString("name")), e), s);
      }
    }
    c.setExtras(extras);
    return c;
  }

  /** Bumped when Driveway changes a folder: a listing fetched before that is stale. */
  private final Map<String, Integer> gens = new ConcurrentHashMap<>();

  private void load(Servers.Server s, String parentDocumentId) {
    if (!loading.add(parentDocumentId)) return;
    int gen = gens.getOrDefault(parentDocumentId, 0);
    io.execute(() -> {
      try {
        publish(parentDocumentId, gen, new Listing(new JSONArray(Engine.list(s.id, remotePath(s, parentDocumentId))), null));
        // It works again: its card stops asking for a hand.
        Problems.clear(getContext(), s.id);
      } catch (Exception e) {
        Problems.observe(getContext(), s, e);
        publish(parentDocumentId, gen, new Listing(null, Errors.explain(getContext(), s, e)));
      } finally {
        loading.remove(parentDocumentId);
        getContext().getContentResolver().notifyChange(
            DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, parentDocumentId), null);
      }
    });
  }

  /** Keeps a fetched listing, unless Driveway changed the folder since the fetch began. */
  private void publish(String parentDocumentId, int gen, Listing l) {
    synchronized (gens) {
      if (gens.getOrDefault(parentDocumentId, 0) == gen) listings.put(parentDocumentId, l);
    }
  }

  @Override
  public String getDocumentType(String documentId) throws FileNotFoundException {
    if (pathOf(documentId).isEmpty()) return Document.MIME_TYPE_DIR;
    try (Cursor c = queryDocument(documentId, null)) {
      if (c.moveToFirst()) return c.getString(c.getColumnIndexOrThrow(Document.COLUMN_MIME_TYPE));
    }
    throw new FileNotFoundException(documentId);
  }

  @Override
  public boolean isChildDocument(String parentDocumentId, String documentId) {
    if (!serverOf(parentDocumentId).equals(serverOf(documentId))) return false;
    String parent = pathOf(parentDocumentId);
    String child = pathOf(documentId);
    if (!canonical(parent) || !canonical(child)) return false;
    return parent.isEmpty() ? !child.isEmpty() : child.startsWith(parent + "/");
  }

  @Override
  public DocumentsContract.Path findDocumentPath(String parentDocumentId, String childDocumentId)
      throws FileNotFoundException {
    String id = serverOf(childDocumentId);
    server(childDocumentId);
    String start = parentDocumentId == null ? "" : pathOf(parentDocumentId);
    List<String> ids = new ArrayList<>();
    String p = pathOf(childDocumentId);
    while (true) {
      ids.add(0, docId(id, p));
      if (p.equals(start) || p.isEmpty()) break;
      p = parentOf(p);
    }
    return new DocumentsContract.Path(parentDocumentId == null ? id : null, ids);
  }

  // Changes.

  @Override
  public String createDocument(String parentDocumentId, String mimeType, String displayName)
      throws FileNotFoundException {
    SessionService.touch(getContext());
    Servers.Server s = server(parentDocumentId);
    checkName(displayName);
    String parent = pathOf(parentDocumentId);
    boolean dir = Document.MIME_TYPE_DIR.equals(mimeType);
    String path;
    try {
      // The engine picks a free name ("name (1).ext"), checks it on the
      // server, and makes the file or folder, connecting included, inside one
      // deadline: Files gives up on a provider after about 20 seconds. A
      // listing fetched a moment ago saves it a trip; it still asks the
      // server about the name it picks, so a stale listing can't make it
      // write over anything.
      Listing cached = listings.get(parentDocumentId);
      String taken = "";
      if (cached != null && cached.entries != null && System.currentTimeMillis() - cached.at < FRESH_MS) {
        JSONArray names = new JSONArray();
        for (int i = 0; i < cached.entries.length(); i++) names.put(cached.entries.getJSONObject(i).optString("name"));
        taken = names.toString();
      }
      path = join(parent, Engine.createNew(s.id, join(s.folder, parent), displayName, dir, taken));
    } catch (Exception e) {
      throw failed(s, e);
    }
    Problems.clear(getContext(), s.id); // it worked
    notifyChildren(parentDocumentId);
    return docId(s.id, path);
  }


  @Override
  public void deleteDocument(String documentId) throws FileNotFoundException {
    long start = SystemClock.uptimeMillis();
    SessionService.touch(getContext());
    Servers.Server s = server(documentId);
    if (pathOf(documentId).isEmpty()) throw new UnsupportedOperationException("To remove this server, use Driveway");
    // A folder whose listing (or a listing inside it) just failed looked
    // empty to Files: a move Files makes by copying would have copied
    // nothing, so its original is kept.
    long now = System.currentTimeMillis();
    for (Map.Entry<String, Listing> e : listings.entrySet()) {
      String k = e.getKey();
      Listing l = e.getValue();
      if (l.error != null && now - l.at < UNLISTED_MS && (k.equals(documentId) || k.startsWith(documentId + "/"))) {
        throw new Errors.Worded(getContext().getString(R.string.err_delete_unlisted));
      }
    }
    try {
      // Files moves what Driveway refused to move on the server by copying
      // it and deleting the original at once, while the copy may still be on
      // its way: the original of such a move stays until the saves begun
      // since the refusal have reached their servers.
      String path = remotePath(s, documentId);
      settle(s, path, Uploads.movingSince(s.id, path));
      Engine.deleteWithin(s.id, path, left(start));
    } catch (Exception e) {
      throw failed(s, e);
    }
    Problems.clear(getContext(), s.id); // it worked
    notifyChildren(docId(s.id, parentOf(pathOf(documentId))));
  }

  @Override
  public void removeDocument(String documentId, String parentDocumentId) throws FileNotFoundException {
    deleteDocument(documentId);
  }

  @Override
  public String renameDocument(String documentId, String displayName) throws FileNotFoundException {
    long start = SystemClock.uptimeMillis();
    SessionService.touch(getContext());
    Servers.Server s = server(documentId);
    String path = pathOf(documentId);
    if (path.isEmpty()) throw new UnsupportedOperationException("To rename this server, use Driveway");
    checkName(displayName);
    String to = join(parentOf(path), displayName);
    try {
      settle(s, join(s.folder, path));
      Engine.renameWithin(s.id, join(s.folder, path), join(s.folder, to), left(start));
    } catch (Exception e) {
      if (Errors.kind(e).equals("toobig")) {
        // A service with no folder rename moves a folder's files one by one:
        // only folders small enough to finish in time, so the rename never
        // stops half way.
        throw new Errors.Worded(getContext().getString(R.string.err_rename_folder_big, s.label));
      }
      throw failed(s, e);
    }
    Problems.clear(getContext(), s.id); // it worked
    notifyChildren(docId(s.id, parentOf(path)));
    return docId(s.id, to);
  }

  @Override
  public String moveDocument(String sourceDocumentId, String sourceParentDocumentId, String targetParentDocumentId)
      throws FileNotFoundException {
    long start = SystemClock.uptimeMillis();
    SessionService.touch(getContext());
    Servers.Server s = server(sourceDocumentId);
    if (!serverOf(targetParentDocumentId).equals(s.id)) {
      // Between servers: Files copies and deletes instead.
      Uploads.moveRefused(getContext(), s.id, remotePath(s, sourceDocumentId));
      throw new UnsupportedOperationException("Can't move between servers");
    }
    server(targetParentDocumentId);
    String from = pathOf(sourceDocumentId);
    String to = join(pathOf(targetParentDocumentId), nameOf(from));
    try {
      settle(s, join(s.folder, from));
      Engine.moveWithin(s.id, join(s.folder, from), join(s.folder, to), left(start));
    } catch (Exception e) {
      if (Errors.kind(e).equals("unsupported")) {
        // A folder on a service with no folder rename: Files makes the folder
        // anew, moves each file into it (on the server, one by one), and
        // deletes the emptied original.
        Logs.i("folder move not possible on the server, Files moves it file by file: " + e.getMessage());
        Uploads.moveRefused(getContext(), s.id, join(s.folder, from));
        throw new UnsupportedOperationException(Errors.detail(e));
      }
      throw failed(s, e);
    }
    Problems.clear(getContext(), s.id); // it worked
    notifyChildren(sourceParentDocumentId);
    notifyChildren(targetParentDocumentId);
    return docId(s.id, to);
  }

  /**
   * A save to path still uploading would land after a rename, move or
   * delete; wait for it, but answer Files well inside its 20 seconds. A save
   * that failed and waits in its notification has to be settled there first.
   */
  private void settle(Servers.Server s, String path) throws Exception {
    settle(s, path, Long.MAX_VALUE);
  }

  /** As settle, also waiting for saves handed to an upload since recentSince (Uploads.settle). */
  private void settle(Servers.Server s, String path, long recentSince) throws Exception {
    switch (Uploads.settle(s.id, path, 10_000, recentSince)) {
      case Uploads.BUSY:
        throw new Errors.Worded(getContext().getString(R.string.err_still_saving));
      case Uploads.FAILED_SAVE:
        throw new Errors.Worded(getContext().getString(R.string.err_failed_save_pending));
      case Uploads.RECENT:
        throw new Errors.Worded(getContext().getString(R.string.err_copy_pending));
      default:
    }
  }

  /**
   * Files waits about 20 seconds for each call. A call that first waited for
   * a save of the same file (settle, up to 10 s) hands the engine only what is
   * left of REQUEST_MS (the engine's own call limit), and never less than a
   * second, so the whole call still answers in time.
   */
  private static final long REQUEST_MS = 18_000;

  private static long left(long start) {
    return Math.max(1_000, REQUEST_MS - (SystemClock.uptimeMillis() - start));
  }

  /** How long a failed listing keeps its folder from being deleted (deleteDocument). */
  private static final long UNLISTED_MS = 60_000;


  /**
   * Copies a file within one server on the server itself (WebDAV, S3-style
   * services, SFTP servers with a shell), so the bytes don't travel through
   * the device twice. Where the server can't, this fails and Files copies
   * the ordinary way.
   */
  @Override
  public String copyDocument(String sourceDocumentId, String targetParentDocumentId) throws FileNotFoundException {
    long start = SystemClock.uptimeMillis();
    SessionService.touch(getContext());
    Servers.Server s = server(sourceDocumentId);
    if (!serverOf(targetParentDocumentId).equals(s.id)) {
      throw new UnsupportedOperationException("Can't copy between servers");
    }
    server(targetParentDocumentId);
    String from = pathOf(sourceDocumentId);
    String to;
    try {
      settle(s, join(s.folder, from));
      // The engine picks the free name itself, in the same call as the copy.
      to = join(pathOf(targetParentDocumentId),
          Engine.copyNew(s.id, join(s.folder, from), remotePath(s, targetParentDocumentId), nameOf(from), left(start)));
    } catch (Exception e) {
      Logs.i("copy on the server not possible, Files copies instead: " + e.getMessage());
      throw new UnsupportedOperationException(Errors.detail(e));
    }
    Problems.clear(getContext(), s.id); // it worked
    notifyChildren(targetParentDocumentId);
    return docId(s.id, to);
  }

  /** The folder changed: forget its listing and have Files fetch it again. */
  void notifyChildren(String parentDocumentId) {
    synchronized (gens) {
      gens.merge(parentDocumentId, 1, Integer::sum);
      listings.remove(parentDocumentId);
    }
    getContext().getContentResolver().notifyChange(
        DocumentsContract.buildChildDocumentsUri(Servers.AUTHORITY, parentDocumentId), null);
  }

  // Opening files.

  @Override
  public ParcelFileDescriptor openDocument(String documentId, String mode, CancellationSignal signal)
      throws FileNotFoundException {
    SessionService.touch(getContext());
    Servers.Server s = server(documentId);
    String path = remotePath(s, documentId);
    int m = ParcelFileDescriptor.parseMode(mode);
    StorageManager sm = getContext().getSystemService(StorageManager.class);
    try {
      // MODE_READ_ONLY and MODE_WRITE_ONLY are bits of MODE_READ_WRITE: compare the masked value.
      if ((m & ParcelFileDescriptor.MODE_READ_WRITE) == ParcelFileDescriptor.MODE_READ_ONLY) {
        return openRead(sm, s, path);
      }
      return openWrite(sm, s, documentId, path, mode);
    } catch (FileNotFoundException e) {
      throw e;
    } catch (Exception e) {
      throw notFound(s, e);
    }
  }

  private ParcelFileDescriptor openRead(StorageManager sm, Servers.Server s, String path) throws Exception {
    File pending = Uploads.pending(s.id, path);
    if (pending != null) return ParcelFileDescriptor.open(pending, ParcelFileDescriptor.MODE_READ_ONLY);
    Reader r = Engine.open(s.id, path);
    // Opened on the server: it works (a sign-in or identity problem is over).
    Problems.clear(getContext(), s.id);
    HandlerThread t = new HandlerThread("read");
    t.start();
    Context ctx = getContext();
    try {
      return openReadFd(sm, ctx, r, t, s, path);
    } catch (Exception e) {
      r.close();
      t.quitSafely();
      throw e;
    }
  }

  private static ParcelFileDescriptor openReadFd(StorageManager sm, Context ctx, Reader r, HandlerThread t,
      Servers.Server s, String path) throws IOException {
    return sm.openProxyFileDescriptor(ParcelFileDescriptor.MODE_READ_ONLY, new ProxyFileDescriptorCallback() {
      @Override
      public long onGetSize() {
        return r.size();
      }

      @Override
      public int onRead(long offset, int size, byte[] data) throws ErrnoException {
        // A long read (a copy off the server, a film) keeps Driveway in the foreground.
        SessionService.touch(ctx);
        try {
          byte[] got = r.readAt(offset, size);
          System.arraycopy(got, 0, data, 0, got.length);
          return got.length;
        } catch (Exception e) {
          Logs.w("read " + path + " at " + offset + ": " + e.getMessage());
          // A sign-in that ended mid-read shows on the server's card too.
          Problems.observe(ctx, s, e);
          throw new ErrnoException("read", OsConstants.EIO);
        }
      }

      @Override
      public void onRelease() {
        r.close();
        t.quitSafely();
      }
    }, new Handler(t.getLooper()));
  }

  /**
   * Writes go into a local copy; when the app closes the file, the copy is
   * uploaded under a temporary name and renamed over the original, so the
   * file on the server is always whole: the old version or the new one.
   */
  private ParcelFileDescriptor openWrite(StorageManager sm, Servers.Server s, String docId, String path, String mode)
      throws Exception {
    Context ctx = getContext();
    // Refused before anything is staged: the upload could only fail.
    if (Kinds.readOnly(s.backend)) throw new FileNotFoundException(ctx.getString(R.string.err_read_only, s.label));
    File local = Uploads.newLocalFile(ctx);
    // The save's generation: newer opens win over older ones, whatever order they close in.
    long gen = Uploads.nextGen();
    Uploads.opened(s.id, path);
    RandomAccessFile raf = null;
    HandlerThread t = null;
    try {
      // Saves are staged on the device before they upload: say so plainly
      // when there's no room, rather than failing halfway.
      long free = local.getParentFile().getUsableSpace();
      if (free < RESERVE_BYTES) throw new FileNotFoundException(ctx.getString(R.string.err_device_full));
      boolean truncate = mode.contains("t") || mode.equals("w");
      boolean keep = !truncate; // "rw", "wa": start from the current contents
      boolean append = mode.contains("a");
      File pending = Uploads.pending(s.id, path);
      if (keep && pending != null) {
        java.nio.file.Files.copy(pending.toPath(), local.toPath());
      } else if (keep) {
        try {
          long size = new JSONObject(Engine.stat(s.id, path)).optLong("size");
          if (size > free - RESERVE_BYTES) throw new FileNotFoundException(ctx.getString(R.string.err_device_full));
          download(ctx, s, path, local);
          // Read from the server: it works (a sign-in or identity problem is over).
          Problems.clear(ctx, s.id);
        } catch (Exception e) {
          if (!Errors.isNotFound(e)) throw e;
        }
      }
      raf = new RandomAccessFile(local, "rw");
      if (truncate) {
        raf.setLength(0);
        Uploads.begin(s.id, path, local, gen); // replaced, whatever the app writes
      }
      t = new HandlerThread("write");
      t.start();
      int pm = mode.contains("r") ? ParcelFileDescriptor.MODE_READ_WRITE : ParcelFileDescriptor.MODE_WRITE_ONLY;
      String parentDoc = docId(s.id, parentOf(pathOf(docId)));
      RandomAccessFile file = raf;
      HandlerThread thread = t;
      return sm.openProxyFileDescriptor(pm, new ProxyFileDescriptorCallback() {
        boolean changed = truncate;
        boolean failed; // a write failed: the local copy is incomplete

        @Override
        public long onGetSize() throws ErrnoException {
          try {
            return file.length();
          } catch (IOException e) {
            throw new ErrnoException("size", OsConstants.EIO);
          }
        }

        @Override
        public int onRead(long offset, int size, byte[] data) throws ErrnoException {
          try {
            file.seek(offset);
            int n = file.read(data, 0, size);
            return Math.max(n, 0);
          } catch (IOException e) {
            throw new ErrnoException("read", OsConstants.EIO);
          }
        }

        @Override
        public int onWrite(long offset, int size, byte[] data) throws ErrnoException {
          try {
            file.seek(append ? file.length() : offset);
            file.write(data, 0, size);
            if (!changed) {
              // An edit in place: from its first write, reads come from this
              // copy, as for a replace ("w"), since Android tells Driveway the
              // app closed the file only some time after it did.
              Uploads.begin(s.id, path, local, gen);
            }
            changed = true;
            return size;
          } catch (IOException e) {
            failed = true;
            Uploads.deviceFull(ctx, s, path);
            throw new ErrnoException("write", OsConstants.ENOSPC);
          }
        }

        @Override
        public void onFsync() throws ErrnoException {
          try {
            file.getFD().sync();
          } catch (IOException e) {
            failed = true;
            throw new ErrnoException("fsync", OsConstants.EIO);
          }
        }

        @Override
        public void onRelease() {
          try {
            file.close();
          } catch (IOException e) {
            failed = true;
          }
          if (failed) {
            // Never send an incomplete save: the server keeps what it had.
            Uploads.abandon(s.id, path, local);
            notifyChildren(parentDoc);
          } else if (changed) {
            Uploads.upload(ctx, s.id, path, local, gen, () -> notifyChildren(parentDoc));
          } else {
            Uploads.forget(local);
          }
          // Only now: until the upload is queued, a rename or delete must wait.
          Uploads.closed(s.id, path);
          thread.quitSafely();
        }
      }, new Handler(t.getLooper()));
    } catch (Exception e) {
      // Nothing was handed to the app: undo what was set up.
      if (raf != null) {
        try {
          raf.close();
        } catch (IOException ignored) {
        }
      }
      if (t != null) t.quitSafely();
      Uploads.closed(s.id, path);
      Uploads.abandon(s.id, path, local);
      throw e;
    }
  }

  /** The whole file, for an app that edits it in place ("rw"). */
  private static void download(Context ctx, Servers.Server s, String path, File to) throws Exception {
    Reader r = Engine.open(s.id, path);
    try (FileOutputStream out = new FileOutputStream(to)) {
      long off = 0;
      while (off < r.size()) {
        SessionService.touch(ctx);
        byte[] b = r.readAt(off, 1 << 20);
        if (b.length == 0) break;
        out.write(b);
        off += b.length;
      }
      if (off != r.size()) throw new IOException("[network] the server sent " + off + " of " + r.size() + " bytes");
    } finally {
      r.close();
    }
  }

  // Errors: Android's document calls only report FileNotFoundException, so
  // the message has to carry the explanation.

  private FileNotFoundException notFound(Servers.Server s, Exception e) {
    Problems.observe(getContext(), s, e);
    return new FileNotFoundException(Errors.explain(getContext(), s, e));
  }

  private IllegalStateException failed(Servers.Server s, Exception e) {
    Problems.observe(getContext(), s, e);
    return new IllegalStateException(Errors.explain(getContext(), s, e));
  }
}
