// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/object"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// A save's temporary copy is readable only by its owner while it uploads; the
// file it replaces keeps its mode, as does one put back after a failed
// replace, and a new file gets the usual one. Needs a
// kernel NFS server with an "insecure" export: DRIVEWAY_KNFS_RW=host.
func TestSaveModes(t *testing.T) {
	host := os.Getenv("DRIVEWAY_KNFS_RW")
	if host == "" {
		t.Skip("set DRIVEWAY_KNFS_RW=host with the kernel-nfs test server, export insecure")
	}
	ctx := context.Background()
	fsys, err := NewFs(ctx, "t-modes", "", configmap.Simple{"host": host, "export": "/export", "uid": "1000", "gid": "1000"})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	dir := "modes-" + time.Now().Format("150405.000")
	defer f.Purge(ctx, dir)
	put := func(name string, in io.Reader) {
		t.Helper()
		if _, err := f.Put(ctx, in, object.NewStaticObjectInfo(dir+"/"+name, time.Now(), -1, true, nil, f)); err != nil {
			t.Fatal(err)
		}
	}
	mode := func(name string) os.FileMode {
		t.Helper()
		var m os.FileMode
		if err := f.do(ctx, func(tg *nfsc.Target) error {
			a, err := tg.Getattr(f.abs(dir + "/" + name))
			if err == nil {
				m = os.FileMode(a.FileMode & 0o777)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return m
	}
	move := func(from, to string) {
		t.Helper()
		o, err := f.NewObject(ctx, dir+"/"+from)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Move(ctx, o, dir+"/"+to); err != nil {
			t.Fatal(err)
		}
	}

	put("private.txt", bytes.NewReader([]byte("old")))
	if err := f.do(ctx, func(tg *nfsc.Target) error {
		return tg.Setattr(f.abs(dir+"/private.txt"), nfsc.Sattr3{Mode: nfsc.SetMode{SetIt: true, Mode: 0o600}})
	}); err != nil {
		t.Fatal(err)
	}

	// The temporary copy, looked at while its upload is still running.
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() { put(TempPrefix+"a-private.txt", pr); close(done) }()
	_, _ = pw.Write([]byte("new "))
	time.Sleep(500 * time.Millisecond)
	if m := mode(TempPrefix + "a-private.txt"); m != 0o600 {
		t.Errorf("temporary copy mid-upload: %o, want 600", m)
	}
	_, _ = pw.Write([]byte("contents"))
	pw.Close()
	<-done

	move(TempPrefix+"a-private.txt", "private.txt")
	if m := mode("private.txt"); m != 0o600 {
		t.Errorf("replaced file: %o, want 600 (kept)", m)
	}
	// The slow replace's undo: the original moved aside, then back after
	// the swap failed. It keeps its own mode (not a new file's).
	move("private.txt", OldPrefix+"x-private.txt")
	move(OldPrefix+"x-private.txt", "private.txt")
	if m := mode("private.txt"); m != 0o600 {
		t.Errorf("file put back after a failed replace: %o, want 600 (kept)", m)
	}
	put(TempPrefix+"b-new.txt", bytes.NewReader([]byte("new file")))
	move(TempPrefix+"b-new.txt", "new.txt")
	if m := mode("new.txt"); m != newFileMode {
		t.Errorf("new file: %o, want %o", m, newFileMode)
	}
}
