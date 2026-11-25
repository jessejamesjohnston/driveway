// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	rlocal "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
	"golang.org/x/crypto/ssh"
)

// A server that fails on cue: rclone's local backend on a test folder,
// behind hooks that fail any operation before it runs, or after it ran (the
// operation happened, only its answer failed). Every connection to it reads
// its hooks by name at call time, so the fresh connections a folder rename
// makes (id:from, id:to) see them too.

// afFaults is what one test's server does wrong.
type afFaults struct {
	dir   string // the backing folder on this machine
	mu    sync.Mutex
	calls map[string]int
	// pre runs before an operation and fails it with its error; post runs
	// after the operation succeeded and replaces its answer with its error.
	pre, post func(c afCall) error
	disable   []string               // features the server lacks: Move, Copy, DirMove, Purge, About
	hide      func(name string) bool // entries a listing leaves out
	putDrop   bool                   // a Put failed by post returns no object
	putExtra  int64                  // added to the size a Put reports storing
	stream    func(n int, rc io.ReadCloser) io.ReadCloser
	about     *fs.Usage
	shut      chan fs.Fs // connections closed by Shutdown
	bucket    bool       // an object store's buckets: the top folders
}

// afCall is one operation on the server. Paths are relative to the backing
// folder, whichever folder the connection was made on.
type afCall struct {
	ctx      context.Context
	op       string // newfs, list, newobject, put, open, remove, move, copy, dirmove, purge, mkdir, rmdir, about
	path, to string // to: the destination of move, copy and dirmove
	n        int    // this op's call number, from 1
}

func (k *afFaults) before(ctx context.Context, op, p, to string) (afCall, error) {
	k.mu.Lock()
	k.calls[op]++
	c := afCall{ctx: ctx, op: op, path: p, to: to, n: k.calls[op]}
	k.mu.Unlock()
	if k.pre != nil {
		return c, k.pre(c)
	}
	return c, nil
}

func (k *afFaults) after(c afCall) error {
	if k.post != nil {
		return k.post(c)
	}
	return nil
}

func (k *afFaults) count(op string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls[op]
}

var afKnobs sync.Map // remote id -> *afFaults

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "afaulty",
		Description: "A test server that fails on cue",
		Hide:        true,
		NewFs:       afNewFs,
		Options:     []fs.Option{{Name: "knob", Help: "Which test's faults."}},
	})
}

func afNewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	knob, _ := m.Get("knob")
	v, ok := afKnobs.Load(knob)
	if !ok {
		return nil, fmt.Errorf("no test server %q", knob)
	}
	k := v.(*afFaults)
	root = strings.Trim(root, "/")
	if _, err := k.before(ctx, "newfs", root, ""); err != nil {
		return nil, err
	}
	inner, err := rlocal.NewFs(ctx, name, filepath.Join(k.dir, root), configmap.Simple{})
	if inner == nil {
		return nil, err
	}
	f := &afFs{Fs: inner, k: k, root: root}
	if errors.Is(err, fs.ErrorIsFile) {
		f.root = parent(root)
	}
	return f, err
}

type afFs struct {
	fs.Fs // local, on the backing folder's root
	k     *afFaults
	root  string
}

func (f *afFs) full(remote string) string { return path.Join(f.root, remote) }

func (f *afFs) wrap(o fs.Object) fs.Object { return &afObj{Object: o, f: f} }

func (f *afFs) Features() *fs.Features {
	ft := (&fs.Features{CanHaveEmptyDirectories: true, BucketBased: f.k.bucket}).Fill(context.Background(), f)
	return ft.DisableList(f.k.disable)
}

func (f *afFs) List(ctx context.Context, dir string) (fs.DirEntries, error) {
	c, err := f.k.before(ctx, "list", f.full(dir), "")
	if err != nil {
		return nil, err
	}
	es, err := f.Fs.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	var out fs.DirEntries
	for _, e := range es {
		if f.k.hide != nil && f.k.hide(path.Base(e.Remote())) {
			continue
		}
		if o, ok := e.(fs.Object); ok {
			e = f.wrap(o)
		}
		out = append(out, e)
	}
	if err := f.k.after(c); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *afFs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	c, err := f.k.before(ctx, "newobject", f.full(remote), "")
	if err != nil {
		return nil, err
	}
	o, err := f.Fs.NewObject(ctx, remote)
	if err != nil {
		return nil, err
	}
	if err := f.k.after(c); err != nil {
		return nil, err
	}
	return f.wrap(o), nil
}

func (f *afFs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	c, err := f.k.before(ctx, "put", f.full(src.Remote()), "")
	if err != nil {
		return nil, err
	}
	o, err := f.Fs.Put(ctx, in, src, options...)
	if err != nil {
		return nil, err
	}
	w := &afObj{Object: o, f: f, extra: f.k.putExtra}
	if err := f.k.after(c); err != nil {
		if f.k.putDrop {
			return nil, err
		}
		return w, err
	}
	return w, nil
}

func (f *afFs) Mkdir(ctx context.Context, dir string) error {
	c, err := f.k.before(ctx, "mkdir", f.full(dir), "")
	if err != nil {
		return err
	}
	if err := f.Fs.Mkdir(ctx, dir); err != nil {
		return err
	}
	return f.k.after(c)
}

func (f *afFs) Rmdir(ctx context.Context, dir string) error {
	c, err := f.k.before(ctx, "rmdir", f.full(dir), "")
	if err != nil {
		return err
	}
	if err := f.Fs.Rmdir(ctx, dir); err != nil {
		return err
	}
	return f.k.after(c)
}

func (f *afFs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	so, ok := src.(*afObj)
	if !ok {
		return nil, fs.ErrorCantMove
	}
	c, err := f.k.before(ctx, "move", so.full(), f.full(remote))
	if err != nil {
		return nil, err
	}
	o, err := f.Fs.Features().Move(ctx, so.Object, remote)
	if err != nil {
		return nil, err
	}
	if err := f.k.after(c); err != nil {
		return nil, err
	}
	return f.wrap(o), nil
}

// Copy on the server: local has none, so read and write here.
func (f *afFs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	so, ok := src.(*afObj)
	if !ok {
		return nil, fs.ErrorCantCopy
	}
	c, err := f.k.before(ctx, "copy", so.full(), f.full(remote))
	if err != nil {
		return nil, err
	}
	rc, err := so.Object.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	o, err := f.Fs.Put(ctx, rc, object.NewStaticObjectInfo(remote, so.ModTime(ctx), so.Object.Size(), true, nil, f))
	if err != nil {
		return nil, err
	}
	if err := f.k.after(c); err != nil {
		return nil, err
	}
	return f.wrap(o), nil
}

func (f *afFs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	sf, ok := src.(*afFs)
	if !ok {
		return fs.ErrorCantDirMove
	}
	c, err := f.k.before(ctx, "dirmove", sf.full(srcRemote), f.full(dstRemote))
	if err != nil {
		return err
	}
	if err := f.Fs.Features().DirMove(ctx, sf.Fs, srcRemote, dstRemote); err != nil {
		return err
	}
	return f.k.after(c)
}

func (f *afFs) Purge(ctx context.Context, dir string) error {
	c, err := f.k.before(ctx, "purge", f.full(dir), "")
	if err != nil {
		return err
	}
	p := filepath.Join(f.k.dir, f.full(dir))
	if _, err := os.Stat(p); err != nil {
		return fs.ErrorDirNotFound
	}
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	return f.k.after(c)
}

func (f *afFs) About(ctx context.Context) (*fs.Usage, error) {
	if _, err := f.k.before(ctx, "about", "", ""); err != nil {
		return nil, err
	}
	return f.k.about, nil
}

func (f *afFs) Shutdown(ctx context.Context) error {
	if f.k.shut != nil {
		select {
		case f.k.shut <- f:
		default:
		}
	}
	return nil
}

type afObj struct {
	fs.Object
	f     *afFs
	extra int64
}

func (o *afObj) full() string   { return o.f.full(o.Remote()) }
func (o *afObj) Fs() fs.Info    { return o.f }
func (o *afObj) Size() int64    { return o.Object.Size() + o.extra }
func (o *afObj) String() string { return o.Remote() }

func (o *afObj) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	c, err := o.f.k.before(ctx, "open", o.full(), "")
	if err != nil {
		return nil, err
	}
	rc, err := o.Object.Open(ctx, options...)
	if err != nil {
		return nil, err
	}
	if err := o.f.k.after(c); err != nil {
		rc.Close()
		return nil, err
	}
	if s := o.f.k.stream; s != nil {
		rc = s(c.n, rc)
	}
	return rc, nil
}

func (o *afObj) Remove(ctx context.Context) error {
	c, err := o.f.k.before(ctx, "remove", o.full(), "")
	if err != nil {
		return err
	}
	if err := o.Object.Remove(ctx); err != nil {
		return err
	}
	return o.f.k.after(c)
}

// afCut is a stream that fails with err after left bytes.
type afCut struct {
	io.ReadCloser
	left int64
	err  error
}

func (r *afCut) Read(b []byte) (int, error) {
	if r.left <= 0 {
		return 0, r.err
	}
	if int64(len(b)) > r.left {
		b = b[:r.left]
	}
	n, err := r.ReadCloser.Read(b)
	r.left -= int64(n)
	return n, err
}

// afServer adds a fault-injecting server for one test and returns its id.
func afServer(t *testing.T) (string, *afFaults) {
	t.Helper()
	id := "a-faulty-" + token()
	k := &afFaults{dir: t.TempDir(), calls: map[string]int{}}
	afKnobs.Store(id, k)
	if err := SetRemote(id, "afaulty", `{"knob":"`+id+`"}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		RemoveRemote(id)
		afKnobs.Delete(id)
	})
	return id, k
}

// write puts a file straight into the backing folder.
func (k *afFaults) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(k.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// read is a file's content in the backing folder, and whether it is there.
func (k *afFaults) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(k.dir, rel))
	return string(b), err == nil
}

// names lists a folder of the backing folder, sorted.
func (k *afFaults) names(t *testing.T, rel string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(k.dir, rel))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// has checks that rel holds want in the backing folder.
func (k *afFaults) has(t *testing.T, rel, want string) {
	t.Helper()
	if got, ok := k.read(rel); !ok || got != want {
		t.Fatalf("%s holds %q (there: %v), want %q", rel, got, ok, want)
	}
}

// gone checks that nothing is at rel in the backing folder.
func (k *afFaults) gone(t *testing.T, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(k.dir, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s: %v, want it gone", rel, err)
	}
}

// noTemp checks that no temporary file is left in the folder rel.
func (k *afFaults) noTemp(t *testing.T, rel string) {
	t.Helper()
	for _, n := range k.names(t, rel) {
		if strings.HasPrefix(n, ".driveway-") {
			t.Fatalf("temporary file left behind in %q: %s", rel, n)
		}
	}
}

func afWant(t *testing.T, err error, kind, words string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), "["+kind+"]") || !strings.Contains(err.Error(), words) {
		t.Fatalf("want [%s] ...%s..., got %v", kind, words, err)
	}
}

func afErr(s string) error { return errors.New(s) }

// The fault-free server behaves like a real one through every call the app
// makes, so a failure in the tests below is the fault's doing.
func TestFaultyServerClean(t *testing.T) {
	id, k := afServer(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Check(id))
	must(Upload(id, "d/a.txt", local(t, []byte("first"))))
	k.has(t, "d/a.txt", "first")
	must(Upload(id, "d/a.txt", local(t, []byte("second version"))))
	k.has(t, "d/a.txt", "second version")
	k.noTemp(t, "d")
	r, err := Open(id, "d/a.txt")
	must(err)
	if r.Size() != int64(len("second version")) {
		t.Fatalf("size %d", r.Size())
	}
	got, err := r.ReadAt(7, 100)
	must(err)
	r.Close()
	if string(got) != "version" {
		t.Fatalf("read %q", got)
	}
	st, err := Stat(id, "")
	must(err)
	if st != `{"name":"","dir":true,"size":0,"mtime":0}` {
		t.Fatalf("stat of the top: %s", st)
	}
	must(Move(id, "d/a.txt", "d/b.txt"))
	must(Copy(id, "d/b.txt", "d/c.txt"))
	k.has(t, "d/c.txt", "second version")
	must(Rename(id, "d", "e"))
	if got := k.names(t, "e"); strings.Join(got, ",") != "b.txt,c.txt" {
		t.Fatalf("after the folder rename: %v", got)
	}
	ls, err := ListNow(id, "e")
	must(err)
	if !strings.Contains(ls, `"name":"b.txt"`) || !strings.Contains(ls, `"mime":"text/plain`) {
		t.Fatalf("listing: %s", ls)
	}
	must(Delete(id, "e/c.txt"))
	k.gone(t, "e/c.txt")
	must(Delete(id, "e"))
	k.gone(t, "e")
}

// A service or settings Driveway can't use is refused with a reason, and an
// empty setting is left out rather than stored as "".
func TestSetRemoteRefusals(t *testing.T) {
	afWant(t, SetRemote("a-x", "no-such-service", `{}`), "other", `unknown service "no-such-service"`)
	afWant(t, SetRemote("a-x", "sftp", `{"host":`), "other", "bad settings")
	if err := SetRemote("a-x", "sftp", `{"host":"example.com","user":""}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("a-x")
	if _, ok := store.GetValue("a-x", "user"); ok {
		t.Fatal("an empty setting was stored")
	}
	if h, _ := store.GetValue("a-x", "host"); h != "example.com" {
		t.Fatalf("host %q", h)
	}
}

// Every call on a server the app never added (or just removed) says
// [notfound] instead of reaching for a connection.
func TestNoSuchServer(t *testing.T) {
	const id = "a-never-added"
	_, err := List(id, "")
	afWant(t, err, "notfound", "no server")
	_, err = ListNow(id, "")
	afWant(t, err, "notfound", "no server")
	_, err = Stat(id, "x")
	afWant(t, err, "notfound", "no server")
	_, err = Open(id, "x")
	afWant(t, err, "notfound", "no server")
	afWant(t, Create(id, "x"), "notfound", "no server")
	afWant(t, Upload(id, "x", local(t, []byte("x"))), "notfound", "no server")
	afWant(t, Mkdir(id, "x"), "notfound", "no server")
	afWant(t, Delete(id, "x"), "notfound", "no server")
	afWant(t, Move(id, "x", "y"), "notfound", "no server")
	afWant(t, Copy(id, "x", "y"), "notfound", "no server")
	_, err = CreateNew(id, "", "x", false, "")
	afWant(t, err, "notfound", "no server")
	if s := Space(id); s != "" {
		t.Fatalf("space of no server: %q", s)
	}
}

// reset closes SFTP's and SMB's connections (the next call opens new ones)
// and leaves other services' alone: NFS's would cut off reads in progress.
func TestResetOnlySFTPAndSMB(t *testing.T) {
	for _, c := range []struct {
		backend, params string
		drops           bool
	}{
		{"sftp", `{"host":"127.0.0.1","port":"1"}`, true},
		{"smb", `{"host":"127.0.0.1","port":"1"}`, true},
		{"nfs", `{"host":"127.0.0.1","port":"1","export":"/"}`, false},
	} {
		id := "a-reset-" + c.backend
		if err := SetRemote(id, c.backend, c.params); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		remotes[id] = &afFs{k: &afFaults{}}
		before := gens[id]
		mu.Unlock()
		reset(id)
		mu.Lock()
		_, kept := remotes[id]
		bumped := gens[id] != before
		mu.Unlock()
		if kept == c.drops || bumped != c.drops {
			t.Errorf("%s: connection kept %v, generation bumped %v", c.backend, kept, bumped)
		}
		RemoveRemote(id)
	}
}

// Two calls that connect at once share the connection the first to finish
// made; the other closes its own instead of leaving it open.
func TestConnectRace(t *testing.T) {
	id, k := afServer(t)
	k.shut = make(chan fs.Fs, 4)
	entered, release := make(chan struct{}), make(chan struct{})
	k.pre = func(c afCall) error {
		if c.op == "newfs" && c.n == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	type res struct {
		f   fs.Fs
		err error
	}
	slow := make(chan res)
	go func() {
		f, err := remote(ctx(), id)
		slow <- res{f, err}
	}()
	<-entered
	fast, err := remote(ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	s := <-slow
	if s.err != nil {
		t.Fatal(s.err)
	}
	if s.f != fast {
		t.Fatal("the slower connect didn't use the connection already made")
	}
	select {
	case closed := <-k.shut:
		if closed == fast {
			t.Fatal("closed the connection in use")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the spare connection was never closed")
	}
	if again, _ := remote(ctx(), id); again != fast {
		t.Fatal("the cached connection changed")
	}
}

// A connection that finishes after its server was removed or its settings
// changed is closed, never handed out with nothing to close it later: a
// removed server is not found, and changed settings get a connection made
// with them.
func TestConnectSuperseded(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(id string)
	}{
		{"removed", RemoveRemote},
		{"settings changed", func(id string) {
			if err := SetRemote(id, "afaulty", `{"knob":"`+id+`"}`); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.shut = make(chan fs.Fs, 4)
			entered, release := make(chan struct{}), make(chan struct{})
			k.pre = func(c afCall) error {
				if c.op == "newfs" && c.n == 1 {
					close(entered)
					<-release
				}
				return nil
			}
			type res struct {
				f   fs.Fs
				err error
			}
			slow := make(chan res)
			go func() {
				f, err := remote(ctx(), id)
				slow <- res{f, err}
			}()
			<-entered
			c.change(id)
			close(release)
			s := <-slow
			var stale fs.Fs
			select {
			case stale = <-k.shut:
			case <-time.After(5 * time.Second):
				t.Fatal("the connection made for the old settings was never closed")
			}
			if s.f == stale {
				t.Fatal("handed out the connection it closed")
			}
			mu.Lock()
			cached := remotes[id]
			mu.Unlock()
			if c.name == "removed" {
				if s.f != nil || s.err == nil || !strings.HasPrefix(s.err.Error(), "[notfound]") {
					t.Fatalf("got %v, %v; want [notfound]", s.f, s.err)
				}
				return
			}
			if s.err != nil || s.f == nil || cached != s.f || k.count("newfs") != 2 {
				t.Fatalf("got %v, %v (cached %v, %d connects); want a new connection, kept", s.f, s.err, cached, k.count("newfs"))
			}
		})
	}
	// Settings that change again while it connects the second time: the
	// call says so rather than go on trying, and both connections close.
	id, k := afServer(t)
	k.shut = make(chan fs.Fs, 4)
	k.pre = func(c afCall) error {
		if c.op == "newfs" && c.n <= 2 {
			return SetRemote(id, "afaulty", `{"knob":"`+id+`"}`)
		}
		return nil
	}
	f, err := remote(ctx(), id)
	if f != nil || err == nil || err.Error() != "[other] the server's settings changed while connecting; try again" {
		t.Fatalf("settings changing twice: %v, %v", f, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-k.shut:
		case <-time.After(5 * time.Second):
			t.Fatalf("%d of 2 connections closed", i)
		}
	}
}

// shortCalls makes every call Files waits on end limit after it starts, for
// one test.
func shortCalls(t *testing.T, limit time.Duration) {
	t.Helper()
	old := callLimit
	callLimit = limit
	t.Cleanup(func() { callLimit = old })
}

// Every call Files waits on ends within one limit from its start: a slow
// connection and then a step that would wait its own whole limit don't add
// up past it.
func TestOneDeadlinePerCall(t *testing.T) {
	const limit = time.Second
	for _, c := range []struct {
		name      string
		op        string // the step that hangs until its context ends
		call      func(id string) error
		kind, msg string
	}{
		{"stat", "newobject", func(id string) error { _, err := Stat(id, "a.txt"); return err }, "network", "deadline"},
		{"listing", "list", func(id string) error { _, err := ListNow(id, ""); return err }, "network", "deadline"},
		{"open", "newobject", func(id string) error { _, err := Open(id, "a.txt"); return err }, "network", "deadline"},
		{"create", "put", func(id string) error { return Create(id, "new.txt") }, "network", "deadline"},
		{"new document", "put", func(id string) error { _, err := CreateNew(id, "", "new.txt", false, ""); return err }, "network", "deadline"},
		{"new folder", "mkdir", func(id string) error { return Mkdir(id, "d") }, "network", "deadline"},
		{"delete", "remove", func(id string) error { return Delete(id, "a.txt") }, "network", "deadline"},
		{"move", "move", func(id string) error { return Move(id, "a.txt", "b.txt") }, "network", "deadline"},
		{"folder rename", "move", func(id string) error { return Rename(id, "from", "to") }, "other", "stopped part way"},
		{"copy", "copy", func(id string) error { return Copy(id, "a.txt", "b.txt") }, "network", "deadline"},
	} {
		t.Run(c.name, func(t *testing.T) {
			shortCalls(t, limit)
			id, k := afServer(t)
			k.disable = []string{"DirMove"}
			k.write(t, "a.txt", "a")
			k.write(t, "from/1.txt", "1")
			k.pre = func(a afCall) error {
				switch {
				case a.op == "newfs" && a.path == "":
					time.Sleep(limit * 6 / 10)
				case a.op == c.op:
					<-a.ctx.Done()
					return a.ctx.Err()
				}
				return nil
			}
			start := time.Now()
			err := c.call(id)
			took := time.Since(start)
			afWant(t, err, c.kind, c.msg)
			if took > limit+400*time.Millisecond {
				t.Fatalf("took %v, want the call over by %v from its start", took, limit)
			}
		})
	}
}

// A call the app has already spent part of Files' wait on (a pending save of
// the same file) gets only what is left: the budget it passes, not a whole
// call limit of its own.
func TestCallBudget(t *testing.T) {
	const budget = 400 * time.Millisecond
	ms := budget.Milliseconds()
	for _, c := range []struct {
		name string
		op   string
		call func(id string) error
	}{
		{"delete", "remove", func(id string) error { return DeleteWithin(id, "a.txt", ms) }},
		{"move", "move", func(id string) error { return MoveWithin(id, "a.txt", "b.txt", ms) }},
		{"rename", "move", func(id string) error { return RenameWithin(id, "a.txt", "b.txt", ms) }},
		{"copy", "copy", func(id string) error { _, err := CopyNew(id, "a.txt", "", "a.txt", ms); return err }},
	} {
		t.Run(c.name, func(t *testing.T) {
			shortCalls(t, 10*time.Second)
			id, k := afServer(t)
			k.write(t, "a.txt", "a")
			k.pre = func(a afCall) error {
				if a.op == c.op {
					<-a.ctx.Done()
					return a.ctx.Err()
				}
				return nil
			}
			start := time.Now()
			err := c.call(id)
			afWant(t, err, "network", "deadline")
			if took := time.Since(start); took > budget+400*time.Millisecond {
				t.Fatalf("took %v, want it over by %v", took, budget)
			}
		})
	}
	// No budget (0): the usual call limit, as Delete and the rest use.
	shortCalls(t, 300*time.Millisecond)
	id, k := afServer(t)
	k.write(t, "a.txt", "a")
	k.pre = func(a afCall) error {
		if a.op == "remove" {
			<-a.ctx.Done()
			return a.ctx.Err()
		}
		return nil
	}
	start := time.Now()
	afWant(t, DeleteWithin(id, "a.txt", 0), "network", "deadline")
	if took := time.Since(start); took < 250*time.Millisecond || took > time.Second {
		t.Fatalf("no budget took %v, want the call limit (300ms)", took)
	}
}

// A copy on the server picks its own free name in the folder, in the same
// call, as Android's providers name copies: "a (1).txt", then "a (2).txt"; a
// name the listing missed is never written over.
func TestCopyNew(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "a.txt", "a")
	k.write(t, "d/x", "x")
	for _, want := range []string{"a (1).txt", "a (2).txt"} {
		got, err := CopyNew(id, "a.txt", "", "a.txt", 0)
		if err != nil || got != want {
			t.Fatalf("copy: %q, %v; want %q", got, err, want)
		}
		k.has(t, want, "a")
	}
	got, err := CopyNew(id, "a.txt", "d", "a.txt", 0)
	if err != nil || got != "a.txt" {
		t.Fatalf("copy into another folder: %q, %v", got, err)
	}
	k.has(t, "d/a.txt", "a")
	// The listing misses "a (3).txt" (another device made it just now): the
	// copy finds it taken and picks the next name.
	k.write(t, "a (3).txt", "someone else's")
	k.hide = func(name string) bool { return name == "a (3).txt" }
	got, err = CopyNew(id, "a.txt", "", "a.txt", 0)
	if err != nil || got != "a (4).txt" {
		t.Fatalf("copy past a name the listing missed: %q, %v", got, err)
	}
	k.has(t, "a (3).txt", "someone else's")
	k.has(t, "a (4).txt", "a")
	// Taken on both tries (the listing misses every name): it gives up
	// rather than overwrite.
	k.hide = func(name string) bool { return strings.HasPrefix(name, "a") }
	_, err = CopyNew(id, "a.txt", "", "a.txt", 0)
	afWant(t, err, "exists", "already exists")
	// A folder that can't be listed fails the copy (the app then copies the
	// ordinary way).
	k.hide = nil
	k.pre = func(a afCall) error {
		if a.op == "list" {
			return afErr("permission denied")
		}
		return nil
	}
	_, err = CopyNew(id, "a.txt", "", "a.txt", 0)
	afWant(t, err, "denied", "permission denied")
	_, err = CopyNew("a-no-such-server", "a.txt", "", "a.txt", 0)
	afWant(t, err, "notfound", "no server")
}

// A connection that takes longer than the call waiting for it fails that
// call, and is kept for the next one, not made again.
func TestLateConnectionKept(t *testing.T) {
	shortCalls(t, 300*time.Millisecond)
	id, k := afServer(t)
	k.shut = make(chan fs.Fs, 4)
	k.write(t, "a.txt", "a")
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	k.pre = func(a afCall) error {
		if a.op == "newfs" && a.n == 1 {
			<-release
		}
		return nil
	}
	start := time.Now()
	_, err := Stat(id, "a.txt")
	afWant(t, err, "network", "connecting took too long")
	if took := time.Since(start); took > time.Second {
		t.Fatalf("took %v waiting for the connection", took)
	}
	once.Do(func() { close(release) })
	for deadline := time.Now().Add(5 * time.Second); ; {
		mu.Lock()
		_, kept := remotes[id]
		mu.Unlock()
		if kept {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the late connection was never kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := Stat(id, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if n := k.count("newfs"); n != 1 {
		t.Fatalf("%d connections made, want the late one used", n)
	}
	select {
	case f := <-k.shut:
		t.Fatalf("closed %v", f)
	default:
	}
}

// A folder rename that would fit its own estimate but not the time its call
// has left (a slow connection used some) is refused before anything moves.
func TestFolderRenameShortOfTime(t *testing.T) {
	shortCalls(t, time.Second)
	id, k := afServer(t)
	k.disable = []string{"DirMove"}
	files := []string{"1.txt", "2.txt", "3.txt", "4.txt", "5.txt"}
	for _, f := range files {
		k.write(t, "from/"+f, f)
	}
	k.pre = func(a afCall) error {
		switch {
		case a.op == "newfs" && a.path == "":
			time.Sleep(600 * time.Millisecond)
		case a.op == "list" && a.path == "from":
			time.Sleep(30 * time.Millisecond) // 27 round trips: under 10 s, over what's left
		}
		return nil
	}
	afWant(t, Rename(id, "from", "to"), "toobig", "moving 5 files would take about")
	if got := k.names(t, "from"); len(got) != len(files) {
		t.Fatalf("from holds %v", got)
	}
	k.gone(t, "to")
}

// Listing and stat failures keep their category: a listing that fails, a
// lookup refused for a reason other than "not found", and a missing file
// whose folder can't be listed to look for a folder of that name.
func TestListAndStatFailures(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "d/a.txt", "a")
	k.pre = func(c afCall) error {
		if c.op == "list" && c.path == "d" {
			return afErr("read tcp 10.0.0.2:5123->10.0.0.9:445: connection reset by peer")
		}
		return nil
	}
	_, err := List(id, "d")
	afWant(t, err, "network", "connection reset")
	_, err = ListNow(id, "d")
	afWant(t, err, "network", "connection reset")
	_, err = Stat(id, "d/missing")
	afWant(t, err, "network", "connection reset")
	k.pre = func(c afCall) error {
		if c.op == "newobject" && c.path == "d/a.txt" {
			return afErr("permission denied")
		}
		return nil
	}
	_, err = Stat(id, "d/a.txt")
	afWant(t, err, "denied", "permission denied")
	_, err = Open(id, "d/a.txt")
	afWant(t, err, "denied", "permission denied")
}

// Sequential reads a little apart reuse one stream, skipping the bytes in
// between; a stream that breaks while skipping is reopened at the offset.
func TestReadSkipAhead(t *testing.T) {
	id, k := afServer(t)
	data := strings.Repeat("0123456789", 10000)
	k.write(t, "f.bin", data)
	read := func(r *Reader, off, n int) {
		t.Helper()
		got, err := r.ReadAt(int64(off), n)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != data[off:off+n] {
			t.Fatalf("ReadAt(%d, %d) returned other bytes", off, n)
		}
	}
	r, err := Open(id, "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	read(r, 0, 100)
	read(r, 1000, 100)
	read(r, 50000, 100)
	r.Close()
	if n := k.count("open"); n != 1 {
		t.Fatalf("%d streams for reads a little apart, want 1", n)
	}
	k.stream = func(n int, rc io.ReadCloser) io.ReadCloser {
		if n == 2 { // this read's first stream breaks after 200 bytes
			return &afCut{ReadCloser: rc, left: 200, err: afErr("connection reset by peer")}
		}
		return rc
	}
	r, err = Open(id, "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	read(r, 0, 100)
	read(r, 1000, 100)
	if n := k.count("open"); n != 3 {
		t.Fatalf("%d streams in all, want 3 (the broken one reopened once)", n)
	}
}

// A stream that can't be opened: a network failure gets one fresh
// connection; a second one, any other failure, a file gone meanwhile or a
// server removed meanwhile ends the read with the right category.
func TestReadOpenFailures(t *testing.T) {
	netErr := afErr("dial tcp 10.0.0.9:22: connect: no route to host")
	for _, c := range []struct {
		name      string
		fail      func(id string, k *afFaults, n int) error
		kind, msg string
		opens     int
	}{
		{"network once", func(_ string, _ *afFaults, n int) error {
			if n == 1 {
				return netErr
			}
			return nil
		}, "", "", 2},
		{"network twice", func(string, *afFaults, int) error { return netErr }, "network", "no route to host", 2},
		{"denied", func(string, *afFaults, int) error { return afErr("permission denied") }, "denied", "permission denied", 1},
		{"file gone", func(_ string, k *afFaults, _ int) error {
			_ = os.Remove(filepath.Join(k.dir, "f.bin"))
			return netErr
		}, "notfound", "not found", 1},
		{"server removed", func(id string, _ *afFaults, _ int) error {
			RemoveRemote(id)
			return netErr
		}, "notfound", "no server", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.write(t, "f.bin", "the whole file")
			r, err := Open(id, "f.bin")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			k.pre = func(call afCall) error {
				if call.op == "open" {
					return c.fail(id, k, call.n)
				}
				return nil
			}
			got, err := r.ReadAt(4, 5)
			if c.kind == "" {
				if err != nil || string(got) != "whole" {
					t.Fatalf("read %q, %v", got, err)
				}
			} else {
				afWant(t, err, c.kind, c.msg)
			}
			if n := k.count("open"); n != c.opens {
				t.Fatalf("%d opens, want %d", n, c.opens)
			}
		})
	}
}

type afProgress struct{ done, total []int64 }

func (p *afProgress) Update(done, total int64) {
	p.done = append(p.done, done)
	p.total = append(p.total, total)
}

// An upload reports its progress, ending at the whole size; a local copy
// that can't be opened fails before anything reaches the server.
func TestUploadProgress(t *testing.T) {
	id, k := afServer(t)
	data := strings.Repeat("x", 300000)
	var p afProgress
	if err := UploadWithProgress(id, "big.bin", local(t, []byte(data)), &p); err != nil {
		t.Fatal(err)
	}
	k.has(t, "big.bin", data)
	if n := len(p.done); n == 0 || p.done[n-1] != int64(len(data)) || p.total[n-1] != int64(len(data)) {
		t.Fatalf("progress %v of %v", p.done, p.total)
	}
	afWant(t, UploadWithProgress(id, "x.bin", filepath.Join(t.TempDir(), "missing"), nil), "other", "no such file")
	if k.count("put") != 1 {
		t.Fatal("a missing local copy reached the server")
	}
}

// CreateNew trusts the app's list of taken names when it parses, lists the
// folder itself when it doesn't, passes on a failed listing or check, and
// never writes over a name that turns out to be taken: after two tries it
// gives up with [exists].
func TestCreateNewFailures(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "d/new.txt", "keep")
	var looked []string
	k.pre = func(c afCall) error {
		if c.op == "newobject" {
			looked = append(looked, c.path)
		}
		return nil
	}
	// The folder is listed first, so the taken name is never tried.
	got, err := CreateNew(id, "d", "new.txt", false, "not json")
	if err != nil || got != "new (1).txt" || strings.Contains(strings.Join(looked, ","), "d/new.txt") {
		t.Fatalf("with an unreadable list: %q %v, looked up %v", got, err, looked)
	}
	got, err = CreateNew(id, "d", "free.txt", false, `["free.txt"]`)
	if err != nil || got != "free (1).txt" {
		t.Fatalf("with the app's list: %q %v", got, err)
	}
	k.pre = func(c afCall) error {
		if c.op == "list" {
			return afErr("i/o timeout")
		}
		return nil
	}
	_, err = CreateNew(id, "d", "x.txt", false, "")
	afWant(t, err, "network", "i/o timeout")
	k.pre = func(c afCall) error {
		if c.op == "newobject" && c.path == "d/y.txt" {
			return afErr("permission denied")
		}
		return nil
	}
	_, err = CreateNew(id, "d", "y.txt", false, "[]")
	afWant(t, err, "denied", "permission denied")
	k.gone(t, "d/y.txt")

	// A listing that misses what is there: both picks are taken.
	k.write(t, "d/new (1).txt", "keep too")
	k.hide = func(string) bool { return true }
	looked = nil
	k.pre = func(c afCall) error {
		if c.op == "newobject" {
			looked = append(looked, c.path)
		}
		return nil
	}
	_, err = CreateNew(id, "d", "new.txt", false, "[]")
	afWant(t, err, "exists", "new.txt already exists")
	if strings.Join(looked, ",") != "d/new.txt,d/new (1).txt" {
		t.Fatalf("looked up %v, want both picks once", looked)
	}
	k.has(t, "d/new.txt", "keep")
	k.has(t, "d/new (1).txt", "keep too")
	k.noTemp(t, "d")
}

// A failed upload leaves the file as it was and no temporary file, whether
// the service handed back what it stored, nothing, or failed before
// storing anything; a short store is caught by the size check.
func TestUploadPutFailures(t *testing.T) {
	reset := afErr("write tcp 10.0.0.2:5123->10.0.0.9:22: write: broken pipe")
	for _, c := range []struct {
		name      string
		set       func(k *afFaults)
		kind, msg string
	}{
		{"failed after storing", func(k *afFaults) {
			k.post = func(c afCall) error {
				if c.op == "put" {
					return reset
				}
				return nil
			}
		}, "network", "broken pipe"},
		{"failed after storing, no object", func(k *afFaults) {
			k.putDrop = true
			k.post = func(c afCall) error {
				if c.op == "put" {
					return reset
				}
				return nil
			}
		}, "network", "broken pipe"},
		{"failed at once", func(k *afFaults) {
			k.pre = func(c afCall) error {
				if c.op == "put" {
					return reset
				}
				return nil
			}
		}, "network", "broken pipe"},
		{"short store", func(k *afFaults) { k.putExtra = 1 }, "other", "stored 4 bytes of 3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.write(t, "d/f.txt", "old")
			c.set(k)
			afWant(t, Upload(id, "d/f.txt", local(t, []byte("new"))), c.kind, c.msg)
			k.has(t, "d/f.txt", "old")
			k.noTemp(t, "d")
		})
	}
}

// On a service with no rename (object stores) the upload goes straight to
// its name: a refused upload or a short store is reported.
func TestUploadNoRename(t *testing.T) {
	id, k := afServer(t)
	k.disable = []string{"Move"}
	k.pre = func(c afCall) error {
		if c.op == "put" {
			return afErr("QuotaExceeded: the bucket's quota is used up")
		}
		return nil
	}
	afWant(t, Upload(id, "f.txt", local(t, []byte("new"))), "full", "quota")
	k.gone(t, "f.txt")
	k.pre = nil
	k.putExtra = 1
	afWant(t, Upload(id, "f.txt", local(t, []byte("new"))), "other", "stored 4 bytes of 3")
	k.putExtra = 0
	if err := Upload(id, "f.txt", local(t, []byte("newer"))); err != nil {
		t.Fatal(err)
	}
	k.has(t, "f.txt", "newer")
	if n := k.count("move"); n != 0 {
		t.Fatalf("%d renames on a service without them", n)
	}
}

// replace puts the uploaded file in place without ever losing the old one:
// after any failure the old file is at its name with its old content, or the
// error says where it is.
func TestReplaceFailures(t *testing.T) {
	refused := afErr("412 Precondition Failed")
	denied := afErr("permission denied")
	type step func(c afCall, k *afFaults) error
	for _, c := range []struct {
		name      string
		existing  bool
		pre, post step
		kind, msg string // "" for success
		want      string // what f.txt holds after
	}{
		{"network failure", true, func(c afCall, _ *afFaults) error {
			if c.n == 1 { // no further tries on a connection that just failed
				return afErr("read tcp 10.0.0.2:5123->10.0.0.9:22: i/o timeout")
			}
			return nil
		}, nil, "network", "i/o timeout", "old"},
		{"rename landed, answer lost", true, nil, func(c afCall, _ *afFaults) error {
			return afErr("the server's answer was garbled")
		}, "", "", "new version"},
		{"refused while the file exists", true, func(c afCall, k *afFaults) error {
			if _, err := os.Stat(filepath.Join(k.dir, c.to)); err == nil {
				return refused
			}
			return nil
		}, nil, "", "", "new version"},
		{"refused, new file", false, func(afCall, *afFaults) error { return refused }, nil,
			"notfound", "can't put the new file in place", ""},
		{"can't move the old file aside", true, func(afCall, *afFaults) error { return denied }, nil,
			"denied", "can't replace d/f.txt", "old"},
		{"put back after a failed rename", true, func(c afCall, _ *afFaults) error {
			switch c.n {
			case 1:
				return refused
			case 3:
				return denied
			}
			return nil
		}, nil, "denied", "permission denied", "old"},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			if c.existing {
				k.write(t, "d/f.txt", "old")
			} else {
				k.write(t, "d/other.txt", "x")
			}
			if c.pre != nil {
				k.pre = func(call afCall) error {
					if call.op == "move" {
						return c.pre(call, k)
					}
					return nil
				}
			}
			if c.post != nil {
				k.post = func(call afCall) error {
					if call.op == "move" {
						return c.post(call, k)
					}
					return nil
				}
			}
			err := Upload(id, "d/f.txt", local(t, []byte("new version")))
			if c.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				afWant(t, err, c.kind, c.msg)
			}
			if c.want == "" {
				k.gone(t, "d/f.txt")
			} else {
				k.has(t, "d/f.txt", c.want)
			}
			k.noTemp(t, "d")
		})
	}
}

// When the old file was moved aside and can't be put back, the error names
// where it is, and it is there whole.
func TestReplaceCantPutBack(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "d/f.txt", "old")
	k.pre = func(c afCall) error {
		if c.op == "move" && c.n != 2 { // only the move aside works
			return afErr("412 Precondition Failed")
		}
		return nil
	}
	err := Upload(id, "d/f.txt", local(t, []byte("new version")))
	afWant(t, err, "other", "couldn't put the old file back: it is ")
	aside := err.Error()[strings.LastIndex(err.Error(), "it is ")+len("it is "):]
	if !strings.HasPrefix(aside, "d/.driveway-old-") {
		t.Fatalf("aside name %q", aside)
	}
	k.has(t, aside, "old")
	k.gone(t, "d/f.txt")
	for _, n := range k.names(t, "d") {
		if strings.HasPrefix(n, ".driveway-") && !strings.HasPrefix(n, ".driveway-old-") {
			t.Fatalf("the upload's temporary file was left: %s", n)
		}
	}
}

// Delete goes to the folder's purge only when looking for a file positively
// found none there: a lookup that failed otherwise (the network) fails the
// delete with its own category, and nothing is purged.
func TestDeleteLookupFails(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "x", "file")
	k.write(t, "x.d/a.txt", "a")
	k.pre = func(c afCall) error {
		if c.op == "newobject" {
			return afErr("read tcp 10.0.0.2:5123->10.0.0.9:2049: connection reset by peer")
		}
		return nil
	}
	afWant(t, Delete(id, "x"), "network", "connection reset")
	afWant(t, Delete(id, "x.d"), "network", "connection reset")
	if n := k.count("purge"); n != 0 {
		t.Fatalf("%d purges after a failed lookup", n)
	}
	k.has(t, "x", "file")
	k.has(t, "x.d/a.txt", "a")
	k.pre = nil
	if err := Delete(id, "x.d"); err != nil {
		t.Fatal(err)
	}
	k.gone(t, "x.d")
}

// Delete on a service with no purge removes a folder's contents one by one,
// subfolders included, and stops with the failure's category.
func TestDeleteSlowPurge(t *testing.T) {
	for _, c := range []struct {
		op, path, kept string
	}{{"list", "d", "d/a.txt"}, {"list", "d/sub/deeper", "d/sub/deeper/c.txt"}, {"remove", "d/sub/b.txt", "d/sub/b.txt"}, {"", "", ""}} {
		t.Run(c.op+" "+c.path, func(t *testing.T) {
			id, k := afServer(t)
			k.disable = []string{"Purge"}
			k.write(t, "d/a.txt", "d/a.txt")
			k.write(t, "d/sub/b.txt", "d/sub/b.txt")
			k.write(t, "d/sub/deeper/c.txt", "d/sub/deeper/c.txt")
			k.pre = func(call afCall) error {
				if call.op == c.op && call.path == c.path {
					return afErr("NFS3ERR_ACCES")
				}
				return nil
			}
			err := Delete(id, "d")
			if c.op == "" {
				if err != nil {
					t.Fatal(err)
				}
				k.gone(t, "d")
				return
			}
			afWant(t, err, "denied", "NFS3ERR_ACCES")
			k.has(t, c.kept, c.kept)
		})
	}
}

// A file move on a service with no rename copies on the server and deletes
// the original. A failed delete leaves exactly one of the two, unless it
// can't be told which, and then the error says both may hold it.
func TestMoveByCopy(t *testing.T) {
	denied := afErr("permission denied")
	for _, c := range []struct {
		name      string
		pre, post func(c afCall, failed *bool) error
		kind, msg string
		from, to  bool // which names hold the file after
	}{
		{"copy fails", func(c afCall, _ *bool) error {
			if c.op == "copy" {
				return afErr("insufficient storage")
			}
			return nil
		}, nil, "full", "insufficient storage", true, false},
		{"delete landed, answer lost", nil, func(c afCall, _ *bool) error {
			if c.op == "remove" {
				return afErr("connection lost")
			}
			return nil
		}, "", "", false, true},
		{"delete refused", func(c afCall, _ *bool) error {
			if c.op == "remove" && c.path == "a.txt" {
				return denied
			}
			return nil
		}, nil, "denied", "permission denied", true, false},
		{"delete refused, can't check", func(c afCall, failed *bool) error {
			if c.op == "remove" && c.path == "a.txt" {
				*failed = true
				return denied
			}
			if c.op == "newobject" && c.path == "a.txt" && *failed {
				return afErr("i/o timeout")
			}
			return nil
		}, nil, "other", "both names may now hold it", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.disable = []string{"Move"}
			k.write(t, "a.txt", "content")
			failed := false
			if c.pre != nil {
				k.pre = func(call afCall) error { return c.pre(call, &failed) }
			}
			if c.post != nil {
				k.post = func(call afCall) error { return c.post(call, &failed) }
			}
			err := Move(id, "a.txt", "b.txt")
			if c.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				afWant(t, err, c.kind, c.msg)
			}
			for name, want := range map[string]bool{"a.txt": c.from, "b.txt": c.to} {
				if want {
					k.has(t, name, "content")
				} else {
					k.gone(t, name)
				}
			}
		})
	}
}

// A file move fails cleanly where the service can neither rename nor copy,
// and when the file can't be looked up.
func TestMoveCantStart(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "a.txt", "content")
	k.disable = []string{"Move", "Copy"}
	afWant(t, Move(id, "a.txt", "b.txt"), "other", "can't rename files")
	k.has(t, "a.txt", "content")
	k.disable = nil
	k.pre = func(c afCall) error {
		if c.op == "newobject" && c.path == "a.txt" {
			return afErr("403 Forbidden")
		}
		return nil
	}
	afWant(t, Move(id, "a.txt", "b.txt"), "denied", "403 Forbidden")
	k.has(t, "a.txt", "content")
}

// A folder rename on a service with no folder rename moves the files one by
// one, and when it can't it says why: counting failed, it would take too
// long, a connection couldn't be made, the move failed, or it stopped part
// way at Files' limit. No file is ever lost.
func TestFolderRenameFailures(t *testing.T) {
	files := []string{"1.txt", "2.txt", "3.txt", "4.txt", "5.txt"}
	for _, c := range []struct {
		name      string
		limit     time.Duration
		pre       func(c afCall) error
		kind, msg string
	}{
		{"counting fails", 0, func(c afCall) error {
			if c.op == "list" && c.path == "from" {
				return afErr("permission denied")
			}
			return nil
		}, "denied", "permission denied"},
		{"slow server", 0, func(c afCall) error {
			if c.op == "list" && c.path == "from" {
				time.Sleep(700 * time.Millisecond)
			}
			return nil
		}, "toobig", "moving 5 files would take about"},
		{"can't open the folder", 0, func(c afCall) error {
			if c.op == "newfs" && c.path == "from" {
				return afErr("permission denied")
			}
			return nil
		}, "denied", "permission denied"},
		{"can't open the new folder", 0, func(c afCall) error {
			if c.op == "newfs" && c.path == "to" {
				return afErr("connection refused")
			}
			return nil
		}, "network", "connection refused"},
		{"moves fail", 0, func(c afCall) error {
			if c.op == "move" || c.op == "copy" || c.op == "put" {
				return afErr("permission denied")
			}
			return nil
		}, "denied", "permission denied"},
		{"the folders open too slowly", 300 * time.Millisecond, func(c afCall) error {
			if c.op == "newfs" && c.path == "to" {
				select {
				case <-c.ctx.Done():
					return c.ctx.Err()
				case <-time.After(3 * time.Second):
				}
			}
			return nil
		}, "network", "context deadline exceeded"},
		{"stopped part way", 300 * time.Millisecond, func(c afCall) error {
			if c.op == "move" && c.n > 1 { // the first file moves, the rest hang
				<-c.ctx.Done()
				return c.ctx.Err()
			}
			return nil
		}, "other", "the rename of from stopped part way: some files are in to and the rest are still in from"},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.disable = []string{"DirMove"}
			for _, f := range files {
				k.write(t, "from/"+f, f)
			}
			if c.limit != 0 {
				old := renameLimit
				renameLimit = c.limit
				defer func() { renameLimit = old }()
			}
			k.pre = c.pre
			afWant(t, Rename(id, "from", "to"), c.kind, c.msg)
			var all []string
			for _, d := range []string{"from", "to"} {
				for _, n := range k.names(t, d) {
					k.has(t, d+"/"+n, n)
					all = append(all, n)
				}
			}
			sort.Strings(all)
			if strings.Join(all, ",") != strings.Join(files, ",") {
				t.Fatalf("files after: %v", all)
			}
			if c.name == "stopped part way" && (len(k.names(t, "to")) == 0 || len(k.names(t, "from")) == 0) {
				t.Fatalf("not part way: from %v, to %v", k.names(t, "from"), k.names(t, "to"))
			}
		})
	}
}

// A folder rename without a server rename closes the two connections it made
// for its folders when it ends, whether it finished or one couldn't be made.
func TestFolderRenameClosesItsFolders(t *testing.T) {
	for _, c := range []struct {
		name string
		pre  func(c afCall) error
		want []string // roots of the connections closed
	}{
		{"moved", nil, []string{"from", "to"}},
		{"can't open the new folder", func(c afCall) error {
			if c.op == "newfs" && c.path == "to" {
				return afErr("connection refused")
			}
			return nil
		}, []string{"from"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, k := afServer(t)
			k.disable = []string{"DirMove"}
			k.write(t, "from/1.txt", "1")
			k.shut = make(chan fs.Fs, 8)
			k.pre = c.pre
			_ = Rename(id, "from", "to")
			var got []string
			for deadline := time.After(5 * time.Second); len(got) < len(c.want); {
				select {
				case f := <-k.shut:
					got = append(got, f.(*afFs).root)
				case <-deadline:
					t.Fatalf("closed %v, want %v", got, c.want)
				}
			}
			select {
			case f := <-k.shut:
				got = append(got, f.(*afFs).root)
			case <-time.After(200 * time.Millisecond):
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("closed %v, want %v", got, c.want)
			}
		})
	}
}

// Deleting a bucket with folder markers closes the markers-off connection it
// made for the delete, and leaves the server's own open.
func TestDeleteBucketClosesItsConnection(t *testing.T) {
	id, k := afServer(t)
	if err := SetRemote(id, "afaulty", `{"knob":"`+id+`","directory_markers":"true"}`); err != nil {
		t.Fatal(err)
	}
	k.bucket = true
	k.write(t, "b/a.txt", "a")
	cached, err := remote(ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	k.shut = make(chan fs.Fs, 4)
	if err := Delete(id, "b"); err != nil {
		t.Fatal(err)
	}
	k.gone(t, "b")
	if n := k.count("newfs"); n != 2 {
		t.Fatalf("%d connections made, want the server's and the delete's", n)
	}
	select {
	case f := <-k.shut:
		if f == cached {
			t.Fatal("closed the server's own connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delete's own connection was never closed")
	}
}

// A server-side copy says [unsupported] whenever the app should copy the
// ordinary way instead: no copy on the service, the service refusing it,
// or a file too big to copy inside Files' limit. Other failures keep
// their category.
func TestCopyFailures(t *testing.T) {
	id, k := afServer(t)
	k.write(t, "a.txt", "content")
	k.disable = []string{"Copy"}
	afWant(t, Copy(id, "a.txt", "b.txt"), "unsupported", "can't copy on the server")
	k.disable = nil
	afWant(t, Copy(id, "missing.txt", "b.txt"), "notfound", "not found")
	k.pre = func(c afCall) error {
		if c.op == "copy" {
			return fs.ErrorCantCopy
		}
		return nil
	}
	afWant(t, Copy(id, "a.txt", "b.txt"), "unsupported", "can't copy")
	k.pre = func(c afCall) error {
		if c.op == "copy" {
			return afErr("connection reset by peer")
		}
		return nil
	}
	afWant(t, Copy(id, "a.txt", "b.txt"), "network", "connection reset")
	k.gone(t, "b.txt")
	k.pre = nil
	// A sparse file: big on paper, nothing on disk.
	big := filepath.Join(k.dir, "big.bin")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, copyLimit+1); err != nil {
		t.Fatal(err)
	}
	afWant(t, Copy(id, "big.bin", "big2.bin"), "unsupported", "too big")
	if n := k.count("copy"); n != 2 {
		t.Fatalf("%d copies started, want 2", n)
	}
}

// Space reports total and free bytes only when the service gives both.
func TestSpace(t *testing.T) {
	id, k := afServer(t)
	total, free := int64(1000), int64(250)
	k.about = &fs.Usage{Total: &total, Free: &free}
	if s := Space(id); s != `{"free":250,"total":1000}` {
		t.Fatalf("space %q", s)
	}
	k.about = &fs.Usage{Free: &free}
	if s := Space(id); s != "" {
		t.Fatalf("space without a total: %q", s)
	}
	k.about = nil
	if s := Space(id); s != "" {
		t.Fatalf("space without an answer: %q", s)
	}
	k.about = &fs.Usage{Total: &total, Free: &free}
	k.pre = func(c afCall) error {
		if c.op == "about" {
			return afErr("connection reset by peer")
		}
		return nil
	}
	if s := Space(id); s != "" {
		t.Fatalf("space after a failure: %q", s)
	}
	k.pre = nil
	k.disable = []string{"About"}
	if s := Space(id); s != "" {
		t.Fatalf("space on a service that doesn't say: %q", s)
	}
}

// A long name (in a script of two-byte characters) still gets a temporary
// name within the 255-byte limit, cut between characters.
func TestLongNameUpload(t *testing.T) {
	id, k := afServer(t)
	name := "a" + strings.Repeat("é", 120) + ".txt"
	var tmp string
	k.pre = func(c afCall) error {
		if c.op == "put" {
			tmp = path.Base(c.path)
		}
		return nil
	}
	if err := Upload(id, name, local(t, []byte("x"))); err != nil {
		t.Fatal(err)
	}
	k.has(t, name, "x")
	if len(tmp) > 255 || !strings.HasPrefix(tmp, ".driveway-") || !utf8.ValidString(tmp) {
		t.Fatalf("temporary name %q (%d bytes)", tmp, len(tmp))
	}
	c := capName(name)
	if len(c) > 200 || !utf8.ValidString(c) || !strings.HasPrefix(name, c) || len(c) < 199 {
		t.Fatalf("capName: %d bytes", len(c))
	}
}

// classify gives each failure the category the app reports, from the words
// real services use; numbers inside file names or ports never decide it.
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		err  error
		kind string
	}{
		{&net.OpError{Op: "dial", Net: "tcp", Err: afErr("something")}, "network"},
		{fmt.Errorf("couldn't list: %w", context.DeadlineExceeded), "network"},
		{io.ErrUnexpectedEOF, "network"},
		{afErr("dial tcp 192.168.1.20:22: connect: connection refused"), "network"},
		{afErr("read tcp 10.0.0.2:5123->10.0.0.9:445: i/o timeout"), "network"},
		{afErr("write: broken pipe"), "network"},
		{afErr("couldn't read header: EOF"), "network"},
		{afErr("use of closed network connection"), "network"},
		{afErr("dial tcp: lookup nas.local: no such host"), "network"},
		{fs.ErrorObjectNotFound, "notfound"},
		{fs.ErrorDirNotFound, "notfound"},
		{&os.PathError{Op: "open", Path: "/x", Err: os.ErrNotExist}, "notfound"},
		{afErr("DSC04011.JPG: no such file"), "notfound"},
		{afErr("NoSuchKey: The specified key does not exist."), "notfound"},
		{fs.ErrorDirExists, "exists"},
		{&os.PathError{Op: "mkdir", Path: "/x", Err: os.ErrExist}, "exists"},
		{afErr("409 Conflict: a file with that name already exists"), "exists"},
		{fs.ErrorDirectoryNotEmpty, "notempty"},
		{afErr("rmdir: directory not empty"), "notempty"},
		{afErr("InvalidBucketName: The specified bucket is not valid."), "bucketname"},
		{afErr("write /data/x: no space left on device"), "full"},
		{afErr("507 Insufficient Storage"), "full"},
		{afErr("QuotaExceeded"), "full"},
		{afErr(`sftp: "Failure" (SSH_FX_FAILURE)`), "full"},
		{afErr("NFS3ERR_NOSPC"), "full"},
		{afErr("NFS3ERR_DQUOT"), "full"},
		{afErr("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password]"), "auth"},
		{afErr("401 Unauthorized"), "auth"},
		{afErr("oauth2: \"invalid_grant\" \"Token has been expired or revoked.\""), "auth"},
		{afErr("couldn't fetch token: maybe token expired? - try refreshing with \"rclone config reconnect\""), "auth"},
		// The service refused the refresh, inside the API call that needed it.
		{&url.Error{Op: "Post", URL: "https://api.dropboxapi.com/2/files/list_folder",
			Err: afErr("couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with \"rclone config reconnect r-dx:\"")}, "auth"},
		// The refresh itself couldn't reach the service.
		{&url.Error{Op: "Post", URL: "https://api.dropboxapi.com/oauth2/token",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: afErr("connection refused")}}, "network"},
		{afErr("knownhosts: key mismatch"), "auth"},
		{afErr("smb2: logon failure"), "auth"},
		{afErr("permission denied"), "denied"},
		{afErr("403 Forbidden"), "denied"},
		{afErr("AccessDenied: Access Denied"), "denied"},
		{afErr("NFS3ERR_ACCES"), "denied"},
		{afErr("NFS3ERR_PERM"), "denied"},
		{&os.PathError{Op: "open", Path: "/x", Err: os.ErrPermission}, "denied"},
		{afErr("can't open DSC04011.JPG on port 4013"), "other"},
		{afErr("[exists] already tagged, kept as it is"), "exists"},
	} {
		got := classify(c.err)
		if got == nil || !strings.HasPrefix(got.Error(), "["+c.kind+"]") {
			t.Errorf("classify(%v) = %v, want [%s]", c.err, got, c.kind)
			continue
		}
		if !errors.Is(got, c.err) {
			t.Errorf("classify(%v) lost the error it wraps", c.err)
		}
	}
	if classify(nil) != nil || tag("other", nil) != nil {
		t.Error("no error became one")
	}
	if got := classify(afErr("[exists] x")).Error(); got != "[exists] x" {
		t.Errorf("a tagged error was tagged again: %s", got)
	}
}

// On S3, deleting a whole bucket works with the folder markers Driveway
// turns on: rclone's markers would trip over the bucket's own.
func TestDeleteBucket(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	s := servers[5]
	if s.backend != "s3" {
		t.Fatal("servers[5] isn't S3")
	}
	const id = "a-s3"
	if err := SetRemote(id, s.backend, s.params); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote(id)
	bucket := "a-del-" + token()
	if err := Mkdir(id, bucket); err != nil {
		t.Fatal(err)
	}
	if err := Upload(id, bucket+"/sub/a.txt", local(t, []byte("a"))); err != nil {
		Delete(id, bucket)
		t.Fatal(err)
	}
	if err := Delete(id, bucket); err != nil {
		t.Fatal(err)
	}
	ls, err := List(id, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ls, bucket) {
		t.Fatalf("the bucket is still there: %s", ls)
	}
}

// On SFTP, the server-side copy checks the file first: a missing one says
// [notfound], one too big to copy inside Files' limit says [unsupported]
// before any copy starts.
func TestSFTPCopyChecks(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	s := servers[0]
	if s.backend != "sftp" {
		t.Fatal("servers[0] isn't SFTP")
	}
	const id = "a-sftp"
	if err := SetRemote(id, s.backend, s.params); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote(id)
	dir := join(s.base, "a-cp-"+token())
	defer Delete(id, dir)
	if err := Mkdir(id, dir); err != nil {
		t.Fatal(err)
	}
	afWant(t, Copy(id, dir+"/missing.bin", dir+"/b.bin"), "notfound", "not found")

	// A sparse file, made over SFTP itself: big on paper, nothing on disk.
	conn, err := ssh.Dial("tcp", "127.0.0.1:2222", &ssh.ClientConfig{User: "driveway",
		Auth:            []ssh.AuthMethod{ssh.Password("driveway-test")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sc, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	f, err := sc.Create(dir + "/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(copyLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	afWant(t, Copy(id, dir+"/big.bin", dir+"/big2.bin"), "unsupported", "too big")
	if _, err := Stat(id, dir+"/big2.bin"); err == nil || !strings.HasPrefix(err.Error(), "[notfound]") {
		t.Fatalf("a too-big copy: %v, want [notfound] (never started)", err)
	}
}
