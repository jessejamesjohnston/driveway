// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/hash"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// countingListener counts the connections made to it and closes each.
func countingListener(t *testing.T) (port int, accepts *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	accepts = new(atomic.Int32)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, accepts
}

// closedPort is a port nothing listens on any more.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// unconnected is an Fs that hasn't dialed yet.
func unconnected(port int) *Fs {
	return &Fs{name: "b-nfs", opt: Options{Host: "127.0.0.1", Export: "/", Port: port}, mu: make(chan struct{}, 1)}
}

// A port that isn't a number is a configuration error, reported before any
// connection is tried.
func TestBadPort(t *testing.T) {
	_, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "export": "/", "port": "nfs"})
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("err %v", err)
	}
}

// A connection attempt whose caller has already given up fails as a server
// that didn't answer would, without dialing.
func TestNewFsTimeUp(t *testing.T) {
	port, accepts := countingListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewFs(ctx, "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "export": "/", "port": strconv.Itoa(port)})
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepts.Load(); n != 0 {
		t.Fatalf("dialed %d times", n)
	}
}

// A connection belonging to a call that was given up on (its epoch has
// passed) is never made: the caller has moved on.
func TestConnectAfterGiveUp(t *testing.T) {
	port, accepts := countingListener(t)
	f := unconnected(port)
	f.epoch = 1
	if _, err := f.connect(0); !errors.Is(err, errNoAnswer) {
		t.Fatalf("err %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepts.Load(); n != 0 {
		t.Fatalf("dialed %d times", n)
	}
}

// With no connection and a server that's gone, a call says the server
// can't be reached, without running the request.
func TestServerGone(t *testing.T) {
	f := unconnected(closedPort(t))
	ran := false
	err := f.do(context.Background(), func(*nfsc.Target) error { ran = true; return nil })
	if ran || err == nil || !strings.HasPrefix(err.Error(), "can't reach the NFS server 127.0.0.1: ") {
		t.Fatalf("ran %v, err %v", ran, err)
	}
	if f.current() != nil {
		t.Fatal("a connection was kept")
	}
}

// The words people see when a mount fails, for each answer go-nfs-client
// turns into an error (mount.go). A refusal names the two settings to check.
func TestMountErrorWords(t *testing.T) {
	for _, tc := range []struct {
		cause string
		want  []string
	}{
		{"MNT3ERR_NOENT", []string{`the server has no export "/volume1/share"`}},
		{"MNT3ERR_PERM", []string{`refused to mount "/volume1/share"`, "this device's address", "non-privileged ports", `"insecure" in /etc/exports`, "Synology"}},
		{"MNT3ERR_ACCES", []string{`refused to mount "/volume1/share"`, "non-privileged ports"}},
		{"rpc: auth error", []string{`refused to mount "/volume1/share"`, "non-privileged ports"}},
		{"rejectedStatus was not valid: 1", []string{`refused to mount "/volume1/share"`, "this device", "non-privileged ports", "AUTH_SYS"}},
		{"rejectedStatus was not valid: 10", []string{`can't mount "/volume1/share": rejectedStatus was not valid: 10`}},
		{"MNT3ERR_IO", []string{`can't mount "/volume1/share": MNT3ERR_IO`}},
	} {
		cause := errors.New(tc.cause)
		err := mountError("/volume1/share", cause)
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", tc.cause, err, w)
			}
		}
		if tc.cause != "MNT3ERR_NOENT" && !errors.Is(err, cause) {
			t.Errorf("%s: cause not kept: %v", tc.cause, err)
		}
	}
}

// The remote's description, and what the backend says it can't do: no
// settable times (so none compared), no hashes.
func TestDescriptions(t *testing.T) {
	f := &Fs{name: "b-nfs", root: "photos/2026", opt: Options{Host: "nas.local", Export: "/volume1/share"}}
	if f.Name() != "b-nfs" || f.Root() != "photos/2026" {
		t.Errorf("name %q, root %q", f.Name(), f.Root())
	}
	if s := f.String(); s != "NFS nas.local:/volume1/share/photos/2026" {
		t.Errorf("string %q", s)
	}
	if f.Precision() != fs.ModTimeNotSupported {
		t.Errorf("precision %v", f.Precision())
	}
	o := &Object{fs: f, remote: "a/b.txt", size: 3}
	if o.Fs() != fs.Info(f) || o.String() != "a/b.txt" || o.Remote() != "a/b.txt" || !o.Storable() {
		t.Errorf("object %v %q %q %v", o.Fs(), o.String(), o.Remote(), o.Storable())
	}
	if _, err := o.Hash(context.Background(), hash.MD5); !errors.Is(err, hash.ErrUnsupported) {
		t.Errorf("hash: %v", err)
	}
	if err := o.SetModTime(context.Background(), time.Now()); !errors.Is(err, fs.ErrorCantSetModTime) {
		t.Errorf("set mod time: %v", err)
	}
}

// Moves only happen on the server between paths of one connection's export;
// across servers rclone copies instead.
func TestCantMoveAcross(t *testing.T) {
	ctx := context.Background()
	a, b := unconnected(2050), unconnected(2051)
	if _, err := a.Move(ctx, &Object{fs: b, remote: "x"}, "y"); !errors.Is(err, fs.ErrorCantMove) {
		t.Errorf("move: %v", err)
	}
	if err := a.DirMove(ctx, b, "x", "y"); !errors.Is(err, fs.ErrorCantDirMove) {
		t.Errorf("dir move: %v", err)
	}
}
