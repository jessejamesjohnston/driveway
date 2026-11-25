// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// failing answers every request the match accepts with the NFS error
// status, without asking the server.
func failing(status uint32, match func(rpcCall) bool) func(rpcCall) fault {
	return func(c rpcCall) fault {
		if match(c) {
			return fault{answer: accepted(status)}
		}
		return fault{}
	}
}

func proc(n int) func(rpcCall) bool { return func(c rpcCall) bool { return c.nfs(n) } }

// named matches requests of procedure n whose arguments carry name (a
// LOOKUP, CREATE or MKDIR of it).
func named(n int, name string) func(rpcCall) bool {
	return func(c rpcCall) bool { return c.nfs(n) && bytes.Contains(c.args, []byte(name)) }
}

// A mount the server refuses or can't find says what to fix, end to end.
func TestMountRefused(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	_, err := NewFs(ctx, "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": "2050", "export": "/nope"})
	if err == nil || err.Error() != `the server has no export "/nope"` {
		t.Errorf("missing export: %v", err)
	}
	p := newProxy(t, "127.0.0.1:0")
	p.setRule(func(c rpcCall) fault {
		if c.mount() {
			return fault{answer: accepted(nfsc.MNT3ErrAcces)}
		}
		return fault{}
	})
	_, err = NewFs(ctx, "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
	if err == nil || !strings.Contains(err.Error(), `refused to mount "/"`) || !strings.Contains(err.Error(), "non-privileged ports") {
		t.Errorf("refused: %v", err)
	}
	// The refused connection isn't kept.
	p.waitMountEnds(t, 1, 2*time.Second)

	// A server that turns down the request's credentials (an RPC
	// authentication error), at the mount or at the first NFS request.
	for i, refused := range []func(rpcCall) bool{rpcCall.mount, proc(nfsc.NFSProc3FSInfo)} {
		p.setRule(func(c rpcCall) fault {
			if refused(c) {
				return fault{answer: words(1, 1, 1, 5)} // MSG_DENIED, AUTH_ERROR, AUTH_TOOWEAK
			}
			return fault{}
		})
		_, err = NewFs(ctx, "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
		if err == nil || !strings.Contains(err.Error(), `refused to mount "/"`) || !strings.Contains(err.Error(), "non-privileged ports") {
			t.Errorf("credentials refused (%d): %v", i, err)
		}
		p.waitMountEnds(t, int32(i+2), 2*time.Second)
	}
}

// A mount that's answered after the caller gave up fails at the caller's
// deadline, and the connection that arrives late is closed, not kept.
func TestMountAnswerTooLate(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	p.setRule(func(c rpcCall) fault {
		if c.mount() {
			return fault{delay: 1500 * time.Millisecond}
		}
		return fault{}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewFs(ctx, "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
	took := time.Since(start)
	if !errors.Is(err, errNoAnswer) {
		t.Fatalf("err %v", err)
	}
	if took > 1200*time.Millisecond {
		t.Fatalf("gave up after %v, want about 0.5 s", took)
	}
	p.waitMountEnds(t, 1, 3*time.Second)
}

// A server that answers too slowly (hung, or gone mid-session) fails the
// call within the caller's bound, not after the NFS client's eight minutes
// of resends. The stuck connection is closed once its request ends, and the
// next call connects afresh.
func TestSlowServerGivenUp(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3GetAttr) {
			return fault{delay: 1500 * time.Millisecond}
		}
		return fault{}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := f.NewObject(ctx, "")
	took := time.Since(start)
	if !errors.Is(err, errNoAnswer) || errors.Is(err, fs.ErrorObjectNotFound) {
		t.Fatalf("err %v", err)
	}
	if took > 1200*time.Millisecond {
		t.Fatalf("gave up after %v, want about 0.5 s", took)
	}
	if f.current() != nil {
		t.Fatal("the stuck connection is still the current one")
	}
	if p.mountEnds.Load() != 0 {
		t.Fatal("closed the connection while its request was still waiting")
	}
	p.waitMountEnds(t, 1, 3*time.Second)
	p.setRule(nil)
	if _, err := f.NewObject(context.Background(), ""); !errors.Is(err, fs.ErrorIsDir) {
		t.Fatalf("next call: %v", err)
	}
	if n := p.mounts.Load(); n != 2 {
		t.Fatalf("%d mounts, want 2 (connected afresh)", n)
	}
}

// A request that fails because the connection broke is tried once more on
// a new connection; if the server can't be reached for that, the call says
// so.
func TestReconnect(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	var seen []*nfsc.Target
	err := f.do(context.Background(), func(tg *nfsc.Target) error {
		seen = append(seen, tg)
		if len(seen) == 1 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil || len(seen) != 2 || seen[0] == seen[1] {
		t.Fatalf("err %v, tries %d", err, len(seen))
	}
	if n := p.mounts.Load(); n != 2 {
		t.Fatalf("%d mounts, want 2", n)
	}

	// The connection breaks and the server is gone with it.
	seen = nil
	err = f.do(context.Background(), func(*nfsc.Target) error {
		seen = append(seen, nil)
		p.l.Close()
		return io.EOF
	})
	if len(seen) != 1 || err == nil || !strings.HasPrefix(err.Error(), "can't reach the NFS server") {
		t.Fatalf("tries %d, err %v", len(seen), err)
	}
}

// Shutdown while a request is still waiting doesn't close the connection
// under it (the NFS client would crash); the connection closes once the
// request ends.
func TestShutdownWhileBusy(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	fsys, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	f.mu <- struct{}{} // a request in progress
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := f.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("shutdown waited %v", took)
	}
	time.Sleep(200 * time.Millisecond)
	if p.mountEnds.Load() != 0 || f.current() == nil {
		t.Fatal("closed the connection under the waiting request")
	}
	f.unlock() // the request ends
	p.waitMountEnds(t, 1, 2*time.Second)
	if f.current() != nil {
		t.Fatal("connection kept after shutdown")
	}
}

// Errors other than "not there" pass through as they are: a folder Files
// may not read is not a missing folder.
func TestServerErrorsPassThrough(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	o := put(t, f, dir+"/secret.txt", "s")
	acces := func(err error) bool { return err != nil && err.Error() == "NFS3ERR_ACCES" }

	p.setRule(failing(nfsc.NFS3ErrAcces, proc(nfsc.NFSProc3ReadDirPlus)))
	if _, err := f.List(ctx, dir); !acces(err) {
		t.Errorf("list: %v", err)
	}
	p.setRule(failing(nfsc.NFS3ErrAcces, named(nfsc.NFSProc3Lookup, "secret.txt")))
	if _, err := f.NewObject(ctx, dir+"/secret.txt"); !acces(err) {
		t.Errorf("new object: %v", err)
	}
	if _, err := o.Open(ctx); !acces(err) {
		t.Errorf("open: %v", err)
	}
	if err := f.Mkdir(ctx, dir+"/secret.txt"); !acces(err) {
		t.Errorf("mkdir: %v", err)
	}
	p.setRule(failing(nfsc.NFS3ErrROFS, proc(nfsc.NFSProc3Create)))
	if _, err := f.Put(ctx, strings.NewReader("x"), object.NewStaticObjectInfo(dir+"/new.txt", time.Now(), -1, true, nil, f)); err == nil || err.Error() != "NFS3ERR_ROFS" {
		t.Errorf("put on a read-only export: %v", err)
	}
	p.setRule(nil)
	if got := names(t, f, dir); len(got) != 1 || got[0] != "secret.txt" {
		t.Errorf("left %v", got)
	}
}

// Deleting and renaming pass the server's errors on, at each step, and
// change nothing they shouldn't on the way.
func TestChangesServerFails(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	o := put(t, f, dir+"/from-here/a.txt", "a")
	put(t, f, dir+"/full/b.txt", "b")
	if err := f.Mkdir(ctx, dir+"/to-here"); err != nil {
		t.Fatal(err)
	}
	acces := func(what string, err error) {
		t.Helper()
		if err == nil || err.Error() != "NFS3ERR_ACCES" {
			t.Errorf("%s: %v", what, err)
		}
	}
	// nth fails the n-th LOOKUP of name.
	nth := func(name string, n int32) func(rpcCall) fault {
		var seen atomic.Int32
		return func(c rpcCall) fault {
			if named(nfsc.NFSProc3Lookup, name)(c) && seen.Add(1) == n {
				return fault{answer: accepted(nfsc.NFS3ErrAcces)}
			}
			return fault{}
		}
	}

	p.setRule(nth("from-here", 1))
	acces("delete, its folder can't be looked up", o.Remove(ctx))
	p.setRule(nth("from-here", 1))
	_, err := f.Move(ctx, o, dir+"/moved.txt")
	acces("move, the source folder can't be looked up", err)
	// The destination folder: looked up by the move's own checks first.
	p.setRule(nth("to-here", 3))
	_, err = f.Move(ctx, o, dir+"/to-here/moved.txt")
	acces("move, the destination folder can't be looked up", err)
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3Rename) {
			return fault{answer: words(1, 1, 1, 5)} // denied: an authentication error
		}
		return fault{}
	})
	if _, err := f.Move(ctx, o, dir+"/moved.txt"); err == nil || !strings.Contains(err.Error(), "rejectedStatus") {
		t.Errorf("move refused: %v", err)
	}
	p.setRule(nth("full", 1))
	acces("purge, the folder can't be looked up", f.Purge(ctx, dir+"/full"))
	p.setRule(failing(nfsc.NFS3ErrAcces, proc(nfsc.NFSProc3ReadDirPlus)))
	acces("purge, the folder can't be listed", f.Purge(ctx, dir+"/full"))
	p.setRule(nil)
	if got := names(t, f, dir); strings.Join(got, ",") != "from-here,full,to-here" {
		t.Fatalf("left %v", got)
	}

	// A reply cut short after its header fails the removal.
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3Remove) {
			return fault{answer: accepted()}
		}
		return fault{}
	})
	if err := o.Remove(ctx); err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Errorf("reply cut short: %v", err)
	}
}

// A kernel NFS server refuses to remove a folder with something in it
// (NFS3ERR_NOTEMPTY); rclone gets fs.ErrorDirectoryNotEmpty.
func TestRmdirNotEmpty(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	put(t, f, dir+"/full/note.txt", "hi")
	p.setRule(failing(nfsc.NFS3ErrNotEmpty, proc(nfsc.NFSProc3RmDir)))
	if err := f.Rmdir(context.Background(), dir+"/full"); !errors.Is(err, fs.ErrorDirectoryNotEmpty) {
		t.Fatalf("err %v", err)
	}
}

// An I/O error removing a folder that is empty stays an I/O error, and one
// whose contents can't be listed to tell stays what the server said.
func TestRmdirIOError(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	put(t, f, dir+"/full/note.txt", "hi")
	if err := f.Mkdir(ctx, dir+"/empty"); err != nil {
		t.Fatal(err)
	}
	p.setRule(failing(nfsc.NFS3ErrIO, proc(nfsc.NFSProc3RmDir)))
	if err := f.Rmdir(ctx, dir+"/empty"); err == nil || err.Error() != "NFS3ERR_IO" {
		t.Errorf("empty: %v", err)
	}
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3RmDir) {
			return fault{answer: accepted(nfsc.NFS3ErrIO)}
		}
		if c.nfs(nfsc.NFSProc3ReadDirPlus) {
			return fault{answer: accepted(nfsc.NFS3ErrAcces)}
		}
		return fault{}
	})
	if err := f.Rmdir(ctx, dir+"/full"); err == nil || err.Error() != "NFS3ERR_IO" {
		t.Errorf("can't list: %v", err)
	}
	p.setRule(nil)
	if got := names(t, f, dir); strings.Join(got, ",") != "empty,full" {
		t.Fatalf("left %v", got)
	}
}

// Making folders: one that appears between the lookup and the mkdir (made
// from another device) is fine; a parent the server won't make stops the
// child, and nothing is made.
func TestMkdirOnTheServer(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)

	p.setRule(func(c rpcCall) fault {
		if named(nfsc.NFSProc3Mkdir, "raced")(c) {
			// The server makes it, and says it was already there.
			return fault{edit: setStatus(nfsc.NFS3ErrExist)}
		}
		return fault{}
	})
	if err := f.Mkdir(ctx, dir+"/raced"); err != nil {
		t.Fatalf("folder made meanwhile: %v", err)
	}
	p.setRule(failing(nfsc.NFS3ErrROFS, proc(nfsc.NFSProc3Mkdir)))
	if err := f.Mkdir(ctx, dir+"/a/b"); err == nil || err.Error() != "NFS3ERR_ROFS" {
		t.Fatalf("parent refused: %v", err)
	}
	p.setRule(nil)
	if got := names(t, f, dir); len(got) != 1 || got[0] != "raced" {
		t.Fatalf("left %v", got)
	}
}

// A save fails, saying why, when the server can't take the data (disk
// full), can't commit it, or reports the file other than as written.
func TestSaveServerFails(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	save := func(name string) error {
		_, err := f.Put(ctx, strings.NewReader("hello"), object.NewStaticObjectInfo(dir+"/"+name, time.Now(), -1, true, nil, f))
		return err
	}

	p.setRule(failing(nfsc.NFS3ErrNoSpc, proc(nfsc.NFSProc3Write)))
	if err := save("full.txt"); err == nil || err.Error() != "NFS3ERR_NOSPC" {
		t.Errorf("disk full: %v", err)
	}
	p.setRule(failing(nfsc.NFS3ErrIO, proc(nfsc.NFSProc3Commit)))
	if err := save("commit.txt"); err == nil || err.Error() != "NFS3ERR_IO" {
		t.Errorf("commit fails: %v", err)
	}

	// After the commit, the server answers the check of what it has with
	// an error, then with one byte more than was written.
	var committed atomic.Bool
	afterCommit := func(name string, f fault) func(rpcCall) fault {
		committed.Store(false)
		return func(c rpcCall) fault {
			if c.nfs(nfsc.NFSProc3Commit) {
				committed.Store(true)
			}
			if committed.Load() && named(nfsc.NFSProc3Lookup, name)(c) {
				return f
			}
			return fault{}
		}
	}
	p.setRule(afterCommit("check.txt", fault{answer: accepted(nfsc.NFS3ErrAcces)}))
	if err := save("check.txt"); err == nil || err.Error() != "NFS3ERR_ACCES" {
		t.Errorf("check fails: %v", err)
	}
	p.setRule(afterCommit("short.txt", fault{edit: func(res []byte) {
		// LOOKUP3resok: status, file handle, then the file's attributes
		// (present flag, type, mode, nlink, uid, gid, size).
		fh := int(binary.BigEndian.Uint32(res[4:]))
		at := 8 + (fh+3)&^3 + 4 + 20
		binary.BigEndian.PutUint64(res[at:], binary.BigEndian.Uint64(res[at:])+1)
	}}))
	if err := save("short.txt"); err == nil || err.Error() != "wrote 5 bytes but the server has 6" {
		t.Errorf("size differs: %v", err)
	}
}

// A call whose caller's time is already up reports the deadline, never
// "not found" (which would tell Files the file is gone).
func TestTimeUpIsNotNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := unconnected(closedPort(t))
	deadline := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, fs.ErrorObjectNotFound) || errors.Is(err, fs.ErrorDirNotFound) {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err := f.NewObject(ctx, "a.txt")
	deadline("new object", err)
	_, err = f.List(ctx, "")
	deadline("list", err)
	deadline("rmdir", f.Rmdir(ctx, "a"))
	deadline("remove", (&Object{fs: f, remote: "a.txt"}).Remove(ctx))
	_, err = (&Object{fs: f, remote: "a.txt"}).Open(ctx)
	deadline("open", err)
}

// A save's temporary copy takes a new file's mode only when the server says
// nothing is at the destination. If looking there fails for another reason,
// it may be replacing a file whose mode it must keep, so it stays
// owner-only.
func TestSaveModeWhenLookupFails(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	tmp := put(t, f, dir+"/"+TempPrefix+"x", "new")
	var lookups, setattrs atomic.Int32
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3SetAttr) {
			setattrs.Add(1)
		}
		if named(nfsc.NFSProc3Lookup, "target.txt")(c) && lookups.Add(1) == 1 {
			return fault{answer: accepted(nfsc.NFS3ErrIO)}
		}
		return fault{}
	})
	if _, err := f.Move(ctx, tmp, dir+"/target.txt"); err != nil {
		t.Fatal(err)
	}
	if n := setattrs.Load(); n != 0 {
		t.Fatalf("%d SETATTR requests after a failed lookup, want none (the copy stays owner-only)", n)
	}
	if got := names(t, f, dir); len(got) != 1 || got[0] != "target.txt" {
		t.Fatalf("left %v", got)
	}
	// When the server does say nothing is there, the new file's mode is set.
	tmp = put(t, f, dir+"/"+TempPrefix+"y", "new")
	if _, err := f.Move(ctx, tmp, dir+"/fresh.txt"); err != nil {
		t.Fatal(err)
	}
	if n := setattrs.Load(); n != 1 {
		t.Fatalf("%d SETATTR requests for a new file, want 1", n)
	}
}

// A rename or save that was given up on changes nothing more when its
// late answer arrives: the caller already reported it failed, and the
// person may have moved on (here, saved the destination again).
func TestGivenUpChangesNothingMore(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	read := func(t *testing.T, f *Fs, remote string) string {
		t.Helper()
		o, err := f.NewObject(ctx, remote)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := o.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// late holds back the server's answer to the first request match
	// accepts, and counts the requests want accepts.
	late := func(p *rpcProxy, match, want func(rpcCall) bool) *atomic.Int32 {
		var seen atomic.Bool
		var wanted atomic.Int32
		p.setRule(func(c rpcCall) fault {
			if want != nil && want(c) {
				wanted.Add(1)
			}
			if match(c) && seen.CompareAndSwap(false, true) {
				return fault{delay: time.Second}
			}
			return fault{}
		})
		return &wanted
	}
	givenUp := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, errNoAnswer) {
			t.Fatalf("err %v, want the call given up on", err)
		}
	}
	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, 300*time.Millisecond)
	}

	t.Run("rename, destination looked up late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		o := put(t, f, dir+"/moving.txt", "moving")
		late(p, named(nfsc.NFSProc3Lookup, "keep.txt"), nil)
		c, cancel := short()
		defer cancel()
		_, err := f.Move(c, o, dir+"/keep.txt")
		givenUp(t, err)
		put(t, f, dir+"/keep.txt", "saved since")
		p.waitMountEnds(t, 1, 3*time.Second)
		if got := read(t, f, dir+"/keep.txt"); got != "saved since" {
			t.Fatalf("keep.txt holds %q: the rename given up on went ahead", got)
		}
		if got := names(t, f, dir); len(got) != 2 {
			t.Fatalf("left %v", got)
		}
	})
	t.Run("rename, mode set late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		put(t, f, dir+"/keep.txt", "old")
		o := put(t, f, dir+"/"+TempPrefix+"k", "new")
		late(p, proc(nfsc.NFSProc3SetAttr), nil)
		c, cancel := short()
		defer cancel()
		_, err := f.Move(c, o, dir+"/keep.txt")
		givenUp(t, err)
		p.waitMountEnds(t, 1, 3*time.Second)
		if got := read(t, f, dir+"/keep.txt"); got != "old" {
			t.Fatalf("keep.txt holds %q: the rename given up on went ahead", got)
		}
	})
	// go-nfs-client's Rename looks up both folders before it sends the
	// RENAME: a lookup answered late must not lead to the rename.
	t.Run("rename, source folder looked up late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		o := put(t, f, dir+"/from-here/moving.txt", "moving")
		renames := late(p, named(nfsc.NFSProc3Lookup, "from-here"), proc(nfsc.NFSProc3Rename))
		c, cancel := short()
		defer cancel()
		_, err := f.Move(c, o, dir+"/keep.txt")
		givenUp(t, err)
		put(t, f, dir+"/keep.txt", "saved since")
		p.waitMountEnds(t, 1, 3*time.Second)
		if n := renames.Load(); n != 0 {
			t.Fatalf("%d RENAME requests after the move was given up on", n)
		}
		if got := read(t, f, dir+"/keep.txt"); got != "saved since" {
			t.Fatalf("keep.txt holds %q: the rename given up on went ahead", got)
		}
	})
	t.Run("folder rename, source folder looked up late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		put(t, f, dir+"/from-here/a/note.txt", "hi")
		renames := late(p, named(nfsc.NFSProc3Lookup, "from-here"), proc(nfsc.NFSProc3Rename))
		c, cancel := short()
		defer cancel()
		givenUp(t, f.DirMove(c, f, dir+"/from-here/a", dir+"/b"))
		p.waitMountEnds(t, 1, 3*time.Second)
		if n := renames.Load(); n != 0 {
			t.Fatalf("%d RENAME requests after the folder rename was given up on", n)
		}
		if got := names(t, f, dir+"/from-here/a"); len(got) != 1 {
			t.Fatalf("folder after: %v", got)
		}
	})
	t.Run("delete, folder looked up late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		o := put(t, f, dir+"/from-here/gone.txt", "x")
		removes := late(p, named(nfsc.NFSProc3Lookup, "from-here"), proc(nfsc.NFSProc3Remove))
		c, cancel := short()
		defer cancel()
		givenUp(t, o.Remove(c))
		p.waitMountEnds(t, 1, 3*time.Second)
		if n := removes.Load(); n != 0 {
			t.Fatalf("%d REMOVE requests after the deletion was given up on", n)
		}
		if got := names(t, f, dir+"/from-here"); len(got) != 1 {
			t.Fatalf("left %v", got)
		}
	})
	// A folder's deletion stops at the first removal answered late, rather
	// than deleting the rest after the caller reported it failed.
	t.Run("purge, a removal answered late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		for _, n := range []string{"a", "b", "c"} {
			put(t, f, dir+"/full/"+n+".txt", n)
		}
		removals := late(p, proc(nfsc.NFSProc3Remove), func(c rpcCall) bool {
			return c.nfs(nfsc.NFSProc3Remove) || c.nfs(nfsc.NFSProc3RmDir)
		})
		c, cancel := short()
		defer cancel()
		givenUp(t, f.Purge(c, dir+"/full"))
		p.waitMountEnds(t, 1, 3*time.Second)
		if n := removals.Load(); n != 1 {
			t.Fatalf("%d REMOVE and RMDIR requests, want 1 (the one answered late)", n)
		}
		if got := names(t, f, dir+"/full"); len(got) != 2 {
			t.Fatalf("left %v, want two files", got)
		}
	})
	t.Run("save, old file removed late", func(t *testing.T) {
		p := newProxy(t, "127.0.0.1:0")
		f := connectTo(t, "127.0.0.1", p.port())
		dir := testDir(t, f)
		put(t, f, dir+"/x.txt", "old")
		creates := late(p, proc(nfsc.NFSProc3Remove), proc(nfsc.NFSProc3Create))
		c, cancel := short()
		defer cancel()
		_, err := f.Put(c, strings.NewReader("new"), object.NewStaticObjectInfo(dir+"/x.txt", time.Now(), -1, true, nil, f))
		givenUp(t, err)
		p.waitMountEnds(t, 1, 3*time.Second)
		if n := creates.Load(); n != 0 {
			t.Fatalf("%d CREATE requests after the save was given up on", n)
		}
	})
}

// A read given up on never touches the caller's buffer afterwards, even
// when its answer arrives later, and the reader fails from then on rather
// than read again on the connection set aside under the late request.
func TestReadGivenUp(t *testing.T) {
	needServers(t)
	ctx := context.Background()
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	dir := testDir(t, f)
	o := put(t, f, dir+"/r.txt", "contents")
	rc, err := o.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	old := callLimit
	callLimit = 300 * time.Millisecond
	defer func() { callLimit = old }()
	var reads atomic.Int32
	p.setRule(func(c rpcCall) fault {
		if c.nfs(nfsc.NFSProc3Read) && reads.Add(1) == 1 {
			return fault{delay: time.Second}
		}
		return fault{}
	})
	buf := bytes.Repeat([]byte{0xAA}, 64)
	n, err := rc.Read(buf)
	if n != 0 || !errors.Is(err, errNoAnswer) {
		t.Fatalf("read %d, %v; want the read given up on", n, err)
	}
	if n, err := rc.Read(make([]byte, 64)); n != 0 || !errors.Is(err, errNoAnswer) {
		t.Errorf("next read %d, %v; want the same failure", n, err)
	}
	p.waitMountEnds(t, 1, 3*time.Second)
	if !bytes.Equal(buf, bytes.Repeat([]byte{0xAA}, 64)) {
		t.Fatalf("the late answer was written into the caller's buffer: %q", buf)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("%d READ requests, want 1", n)
	}
}
