// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// The NFS client panics on some replies it can't read (a denied call, an
// accepted reply cut short). From a server that sends one, the call fails;
// the app (the whole documents provider) keeps running, and the next call
// works.
func TestMalformedReply(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	f := connectTo(t, "127.0.0.1", p.port())
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reply []byte
	}{
		{"denied, RPC version mismatch", words(1, 1, 0, 2, 2)},
		{"accepted, cut short", words(1, 0)},
	} {
		bad := true
		p.setRule(func(c rpcCall) fault {
			if bad && c.nfs(nfsc.NFSProc3GetAttr) {
				bad = false
				return fault{answer: tc.reply}
			}
			return fault{}
		})
		if _, err := f.NewObject(ctx, ""); err == nil || !strings.Contains(err.Error(), "reply that can't be read") {
			t.Errorf("%s: %v", tc.name, err)
		}
		// The root is a folder: the server answered properly this time.
		if _, err := f.NewObject(ctx, ""); !errors.Is(err, fs.ErrorIsDir) {
			t.Errorf("%s: next call: %v", tc.name, err)
		}
	}
}

// A mount answered with a reply the NFS client panics on fails, and the
// connection it was made on is closed, not left open with its reader.
func TestMalformedMountReply(t *testing.T) {
	needServers(t)
	p := newProxy(t, "127.0.0.1:0")
	p.setRule(func(c rpcCall) fault {
		if c.mount() {
			return fault{answer: words(1, 1, 0, 2, 2)} // denied, RPC version mismatch
		}
		return fault{}
	})
	_, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
	if err == nil || !strings.Contains(err.Error(), "reply that can't be read") {
		t.Fatalf("err %v", err)
	}
	p.waitMountEnds(t, 1, 2*time.Second)
}

// Shutdown runs in the background (engine.dropRemote), where a panic would
// end the app: an unmount answered with a reply the NFS client panics on,
// or not answered at all, still ends Shutdown, in time, and the connection
// is closed.
func TestShutdownUnmountFails(t *testing.T) {
	needServers(t)
	for _, tc := range []struct {
		name  string
		fault fault
	}{
		{"malformed", fault{answer: words(1, 1, 0, 2, 2)}},
		{"no answer", fault{drop: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProxy(t, "127.0.0.1:0")
			fsys, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": strconv.Itoa(p.port()), "export": "/"})
			if err != nil {
				t.Fatal(err)
			}
			p.setRule(func(c rpcCall) fault {
				if c.prog == nfsc.MountProg && c.proc == nfsc.MountProc3UMNT {
					return tc.fault
				}
				return fault{}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			start := time.Now()
			if err := fsys.Features().Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(start); took > 1500*time.Millisecond {
				t.Fatalf("shutdown took %v", took)
			}
			if fsys.(*Fs).current() != nil {
				t.Fatal("connection kept after shutdown")
			}
			if tc.fault.answer != nil {
				p.waitMountEnds(t, 1, 2*time.Second)
			}
		})
	}
}
