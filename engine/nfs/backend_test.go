// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
)

// testDir makes a folder with a random name on the server, removed when the
// test ends.
func testDir(t *testing.T, f *Fs) string {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	dir := "b-nfs-" + hex.EncodeToString(b)
	if err := f.Mkdir(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Purge(context.Background(), dir) })
	return dir
}

func put(t *testing.T, f *Fs, remote, data string) fs.Object {
	t.Helper()
	o, err := f.Put(context.Background(), strings.NewReader(data), object.NewStaticObjectInfo(remote, time.Now(), -1, true, nil, f))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func names(t *testing.T, f *Fs, dir string) []string {
	t.Helper()
	entries, err := f.List(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimPrefix(e.Remote(), dir+"/"))
	}
	sort.Strings(out)
	return out
}

// rclone convention: a root that names a file means its folder, and NewFs
// says so (fs.ErrorIsFile).
func TestRootIsFile(t *testing.T) {
	needServers(t)
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	put(t, f, dir+"/note.txt", "hi")
	g, err := NewFs(context.Background(), "b-nfs", dir+"/note.txt", configmap.Simple{"host": "127.0.0.1", "port": "2050", "export": "/"})
	if !errors.Is(err, fs.ErrorIsFile) {
		t.Fatalf("err %v", err)
	}
	defer g.Features().Shutdown(context.Background())
	if g.Root() != dir {
		t.Fatalf("root %q, want %q", g.Root(), dir)
	}
	if _, err := g.NewObject(context.Background(), "note.txt"); err != nil {
		t.Fatal(err)
	}
}

// What isn't there (or isn't what was asked for) comes back as rclone's
// own errors, which the engine turns into "not found" for Files.
func TestNotThere(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	o := put(t, f, dir+"/note.txt", "hi")

	if _, err := f.List(ctx, dir+"/missing"); !errors.Is(err, fs.ErrorDirNotFound) {
		t.Errorf("list missing folder: %v", err)
	}
	if _, err := f.List(ctx, dir+"/note.txt"); !errors.Is(err, fs.ErrorDirNotFound) {
		t.Errorf("list a file: %v", err)
	}
	if _, err := f.NewObject(ctx, ""); !errors.Is(err, fs.ErrorIsDir) {
		t.Errorf("the export's root as a file: %v", err)
	}
	if err := f.Rmdir(ctx, dir+"/missing"); !errors.Is(err, fs.ErrorDirNotFound) {
		t.Errorf("rmdir missing: %v", err)
	}
	// Purging what is already gone succeeds, unless the folder it was in is
	// gone too.
	if err := f.Purge(ctx, dir+"/missing"); err != nil {
		t.Errorf("purge missing: %v", err)
	}
	if err := f.Purge(ctx, dir+"/missing/sub"); !errors.Is(err, fs.ErrorDirNotFound) {
		t.Errorf("purge in a missing folder: %v", err)
	}
	if err := o.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove(ctx); !errors.Is(err, fs.ErrorObjectNotFound) {
		t.Errorf("remove again: %v", err)
	}
	if _, err := o.Open(ctx); !errors.Is(err, fs.ErrorObjectNotFound) {
		t.Errorf("open removed: %v", err)
	}
	if _, err := f.Move(ctx, o, dir+"/moved.txt"); err == nil {
		t.Error("moved a removed file")
	}
	if got := names(t, f, dir); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

// Purge removes a folder with everything in it, subfolders included, and
// never a file: a file's path fails with NOTDIR and the file stays.
func TestPurge(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	put(t, f, dir+"/tree/a.txt", "a")
	put(t, f, dir+"/tree/sub/b.txt", "b")
	put(t, f, dir+"/tree/sub/deeper/c.txt", "c")
	if err := f.Mkdir(ctx, dir+"/tree/empty"); err != nil {
		t.Fatal(err)
	}
	put(t, f, dir+"/note.txt", "hi")
	if err := f.Purge(ctx, dir+"/note.txt"); err == nil || err.Error() != "NFS3ERR_NOTDIR" {
		t.Errorf("purge a file: %v", err)
	}
	if err := f.Purge(ctx, dir+"/tree"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, f, dir); len(got) != 1 || got[0] != "note.txt" {
		t.Fatalf("left %v", got)
	}
}

// Nothing is made below a file: Mkdir, Put, Move and DirMove all stop at
// "exists and is not a directory" (or the server's NOTDIR) and leave the
// file alone.
func TestUnderAFile(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	o := put(t, f, dir+"/note.txt", "hi")
	if err := f.Mkdir(ctx, dir+"/sub"); err != nil {
		t.Fatal(err)
	}
	notDir := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "exists and is not a directory") {
			t.Errorf("%s: %v", what, err)
		}
	}
	notDir("mkdir on the file", f.Mkdir(ctx, dir+"/note.txt"))
	_, err := f.Put(ctx, strings.NewReader("x"), object.NewStaticObjectInfo(dir+"/note.txt/x", time.Now(), -1, true, nil, f))
	notDir("put below the file", err)
	_, err = f.Move(ctx, o, dir+"/note.txt/x")
	notDir("move below the file", err)
	notDir("dir move below the file", f.DirMove(ctx, f, dir+"/sub", dir+"/note.txt/sub"))
	if err := f.Mkdir(ctx, dir+"/note.txt/sub"); err == nil {
		t.Error("made a folder inside a file")
	}
	if o2, err := f.NewObject(ctx, dir+"/note.txt"); err != nil || o2.Size() != 2 {
		t.Fatalf("file after: %v", err)
	}
}

// Rmdir removes an empty folder. A folder with something in it stays, and
// says it isn't empty: the test server (rclone serve nfs) answers NFS3ERR_IO
// where a kernel server says NOTEMPTY (TestRmdirNotEmpty).
func TestRmdir(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	if err := f.Mkdir(ctx, dir+"/empty"); err != nil {
		t.Fatal(err)
	}
	put(t, f, dir+"/full/note.txt", "hi")
	if err := f.Rmdir(ctx, dir+"/empty"); err != nil {
		t.Fatal(err)
	}
	if err := f.Rmdir(ctx, dir+"/full"); !errors.Is(err, fs.ErrorDirectoryNotEmpty) {
		t.Fatalf("a folder with a file in it: %v", err)
	}
	if got := names(t, f, dir); len(got) != 1 || got[0] != "full" {
		t.Fatalf("left %v", got)
	}
}

// A save onto a name that is a folder with files fails and leaves the folder
// and its files where they were.
func TestPutOntoFolder(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	put(t, f, dir+"/photos/a.jpg", "jpeg")
	if _, err := f.Put(ctx, strings.NewReader("x"), object.NewStaticObjectInfo(dir+"/photos", time.Now(), -1, true, nil, f)); err == nil {
		t.Fatal("saved over a folder")
	}
	if got := names(t, f, dir+"/photos"); len(got) != 1 || got[0] != "a.jpg" {
		t.Fatalf("folder after: %v", got)
	}
}

// A file is never moved onto a folder: the test server (rclone serve nfs)
// would let it replace the folder and everything in it, where a kernel
// server refuses. The save that tried says the name is a folder.
func TestMoveOntoFolder(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	put(t, f, dir+"/photos/a.jpg", "jpeg")
	o := put(t, f, dir+"/"+TempPrefix+"photos", "x")
	if _, err := f.Move(ctx, o, dir+"/photos"); !errors.Is(err, fs.ErrorIsDir) {
		t.Fatalf("err %v", err)
	}
	if got := names(t, f, dir+"/photos"); len(got) != 1 || got[0] != "a.jpg" {
		t.Fatalf("folder after: %v", got)
	}
}

// A folder renamed on the server takes its files with it; one onto a name
// that's taken is refused with fs.ErrorDirExists (rclone then merges).
func TestDirMove(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	put(t, f, dir+"/a/note.txt", "hi")
	put(t, f, dir+"/taken/other.txt", "x")
	if err := f.DirMove(ctx, f, dir+"/a", dir+"/taken"); !errors.Is(err, fs.ErrorDirExists) {
		t.Fatalf("onto a taken name: %v", err)
	}
	if err := f.DirMove(ctx, f, dir+"/a", dir+"/new/b"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, f, dir+"/new/b"); len(got) != 1 || got[0] != "note.txt" {
		t.Fatalf("moved folder: %v", got)
	}
	if got := names(t, f, dir); strings.Join(got, ",") != "new,taken" {
		t.Fatalf("left %v", got)
	}
}

// mandatoryOption is an open option the backend doesn't know and mustn't
// ignore silently: it logs it and reads the whole file.
type mandatoryOption struct{}

func (mandatoryOption) Header() (string, string) { return "X-Test", "1" }
func (mandatoryOption) String() string           { return "mandatory test option" }
func (mandatoryOption) Mandatory() bool          { return true }

// Reads honor range and seek options: a range returns exactly its bytes,
// then EOF; a negative seek fails before reading.
func TestReadRanges(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	o := put(t, f, dir+"/digits.txt", "0123456789")
	read := func(options ...fs.OpenOption) (string, error) {
		t.Helper()
		r, err := o.Open(ctx, options...)
		if err != nil {
			return "", err
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		return string(b), err
	}
	for _, tc := range []struct {
		opt  fs.OpenOption
		want string
	}{
		{&fs.RangeOption{Start: 2, End: 4}, "234"},
		{&fs.RangeOption{Start: -1, End: 3}, "789"},
		{&fs.SeekOption{Offset: 6}, "6789"},
		{mandatoryOption{}, "0123456789"},
	} {
		if got, err := read(tc.opt); err != nil || got != tc.want {
			t.Errorf("%v: %q, %v; want %q", tc.opt, got, err, tc.want)
		}
	}
	// A range read stops at its end even when asked for more.
	r, err := o.Open(ctx, &fs.RangeOption{Start: 0, End: 1})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if n, err := r.Read(buf); n != 2 || err != nil || string(buf[:n]) != "01" {
		t.Fatalf("first read: %d %v %q", n, err, buf[:n])
	}
	if n, err := r.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("read past the range: %d %v", n, err)
	}
	if _, err := read(&fs.SeekOption{Offset: -1}); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative seek: %v", err)
	}
}

// A save whose source fails partway returns the source's error.
func TestPutSourceFails(t *testing.T) {
	needServers(t)
	f := connectTo(t, "127.0.0.1", 2050)
	dir := testDir(t, f)
	broke := errors.New("source went away")
	in := io.MultiReader(strings.NewReader("part"), iotest.ErrReader(broke))
	_, err := f.Put(context.Background(), in, object.NewStaticObjectInfo(dir+"/a.txt", time.Now(), -1, true, nil, f))
	if !errors.Is(err, broke) {
		t.Fatalf("err %v", err)
	}
}
