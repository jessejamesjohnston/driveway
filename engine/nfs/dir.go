// SPDX-License-Identifier: Apache-2.0

package nfs

// The Fs side of the backend: listings, folders, moves and purges.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// Name of the remote.
func (f *Fs) Name() string { return f.name }

// Root of the remote.
func (f *Fs) Root() string { return f.root }

// String describes the remote.
func (f *Fs) String() string { return fmt.Sprintf("NFS %s:%s/%s", f.opt.Host, f.opt.Export, f.root) }

// Precision of modification times: not settable here, so not compared.
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }

// Hashes supported: none.
func (f *Fs) Hashes() hash.Set { return hash.Set(hash.None) }

// Features of the remote.
func (f *Fs) Features() *fs.Features { return f.features }

// List the directory dir.
func (f *Fs) List(ctx context.Context, dir string) (fs.DirEntries, error) {
	var entries []*nfsc.EntryPlus
	// A caller with a deadline (a listing Files waits for) gets no more
	// than that (doWithin): the NFS client can't be interrupted otherwise.
	err := f.doWithin(ctx, folderLimit, func(t *nfsc.Target) error {
		var err error
		entries, err = t.ReadDirPlus(f.abs(dir))
		return err
	})
	if isNotExist(err) || nfsc.IsNotDirError(err) {
		return nil, fs.ErrorDirNotFound
	}
	if err != nil {
		return nil, err
	}
	out := make(fs.DirEntries, 0, len(entries))
	for _, e := range entries {
		remote := path.Join(dir, e.Name())
		if e.IsDir() {
			out = append(out, fs.NewDir(remote, e.ModTime()))
		} else if e.Mode().IsRegular() {
			out = append(out, &Object{fs: f, remote: remote, size: e.Size(), modTime: e.ModTime()})
		}
	}
	return out, nil
}

// NewObject finds the file at remote.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	fi, err := f.getattr(ctx, f.abs(remote))
	if isNotExist(err) {
		return nil, fs.ErrorObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fs.ErrorIsDir
	}
	return &Object{fs: f, remote: remote, size: fi.Size(), modTime: fi.ModTime()}, nil
}

// Put uploads a new file.
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	o := &Object{fs: f, remote: src.Remote()}
	return o, o.Update(ctx, in, src, options...)
}

// Mkdir makes dir and any missing parents.
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	return f.mkdirAll(ctx, f.abs(dir))
}

func (f *Fs) mkdirAll(ctx context.Context, p string) error {
	if p == "" || p == "." {
		return nil
	}
	fi, err := f.getattr(ctx, p)
	if err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s exists and is not a directory", p)
	}
	if !isNotExist(err) {
		return err
	}
	if err := f.mkdirAll(ctx, parent(p)); err != nil {
		return err
	}
	err = f.do(ctx, func(t *nfsc.Target) error {
		_, err := t.Mkdir(p, 0o775)
		return err
	})
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

// Rmdir removes dir if it's empty.
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	p := f.abs(dir)
	err := f.do(ctx, func(t *nfsc.Target) error { return f.removeEntry(t, nfsc.NFSProc3RmDir, p) })
	if isNotExist(err) {
		return fs.ErrorDirNotFound
	}
	if isNotEmpty(err) || isIOError(err) && f.hasEntries(ctx, p) {
		return fs.ErrorDirectoryNotEmpty
	}
	return err
}

// isIOError reports the server's generic I/O error, which rclone serve nfs
// (and servers built on its NFS library) send for a folder that isn't empty.
func isIOError(err error) bool {
	var ne *nfsc.Error
	return errors.As(err, &ne) && ne.ErrorNum == nfsc.NFS3ErrIO
}

// hasEntries reports whether the folder p could be listed and has something
// in it.
func (f *Fs) hasEntries(ctx context.Context, p string) bool {
	n := 0
	err := f.do(ctx, func(t *nfsc.Target) error {
		entries, err := t.ReadDirPlus(p)
		n = len(entries)
		return err
	})
	// n isn't ours to read after a failure: a request given up on may still set it.
	return err == nil && n > 0
}

// Move renames src to remote on the server: atomic, and replaces an existing file.
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	so, ok := src.(*Object)
	if !ok || so.fs.opt != f.opt {
		return nil, fs.ErrorCantMove
	}
	dst := f.abs(remote)
	if err := f.mkdirAll(ctx, parent(dst)); err != nil {
		return nil, err
	}
	from := so.fs.abs(so.remote)
	if err := f.do(ctx, func(t *nfsc.Target) error {
		// A replaced file keeps its permissions. A new file from a save
		// (made under a temporary name, readable only by its owner while
		// it uploads) gets the mode new files always had, but only when the
		// server says nothing is there: a lookup that failed otherwise may
		// hide a file whose mode it must keep. (If the server won't set it,
		// or can't say, the file stays owner-only: never more open.)
		a, err := t.Getattr(dst)
		if gerr := f.stillOn(t); gerr != nil {
			return gerr
		}
		if err == nil && a.IsDir() {
			// A kernel server refuses this; rclone serve nfs would replace
			// the folder, and everything in it, with the file.
			return fs.ErrorIsDir
		}
		if err == nil {
			_ = t.Setattr(from, nfsc.Sattr3{Mode: nfsc.SetMode{SetIt: true, Mode: a.FileMode & 0o7777}})
		} else if isNotExist(err) && isUploadTemp(path.Base(from)) {
			_ = t.Setattr(from, nfsc.Sattr3{Mode: nfsc.SetMode{SetIt: true, Mode: newFileMode}})
		}
		return f.rename(t, from, dst)
	}); err != nil {
		return nil, err
	}
	return f.NewObject(ctx, remote)
}

// DirMove renames a directory on the server.
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	sf, ok := src.(*Fs)
	if !ok || sf.opt != f.opt {
		return fs.ErrorCantDirMove
	}
	dst := f.abs(dstRemote)
	if _, err := f.getattr(ctx, dst); err == nil {
		return fs.ErrorDirExists
	}
	if err := f.mkdirAll(ctx, parent(dst)); err != nil {
		return err
	}
	return f.do(ctx, func(t *nfsc.Target) error { return f.rename(t, sf.abs(srcRemote), dst) })
}

// Purge removes dir and everything in it.
func (f *Fs) Purge(ctx context.Context, dir string) error {
	err := f.doWithin(ctx, folderLimit, func(t *nfsc.Target) error { return f.removeAll(t, f.abs(dir)) })
	if isNotExist(err) {
		return fs.ErrorDirNotFound
	}
	return err
}

// removeAll removes the folder p and everything in it. A folder that is
// already gone is fine; one whose parent is gone is not found. Nothing is
// removed unless p is a folder that could be listed.
func (f *Fs) removeAll(t *nfsc.Target, p string) error {
	_, dir, err := t.Lookup(parent(p))
	if err != nil {
		return err
	}
	fi, fh, err := t.Lookup(p)
	if isNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return nfsc.NFS3Error(nfsc.NFS3ErrNotDir)
	}
	if err := f.empty(t, p, fh); err != nil {
		return err
	}
	return f.dirOp(t, nfsc.NFSProc3RmDir, dir, path.Base(p))
}

// empty removes everything in the folder p (handle fh), one request at a
// time (go-nfs-client's RemoveAll goes on deleting after the call was given
// up on).
func (f *Fs) empty(t *nfsc.Target, p string, fh []byte) error {
	entries, err := t.ReadDirPlus(p)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			err = f.dirOp(t, nfsc.NFSProc3Remove, fh, e.Name())
		} else {
			child := path.Join(p, e.Name())
			var sub []byte
			if _, sub, err = t.Lookup(child); err == nil {
				err = f.empty(t, child, sub)
			}
			if err == nil {
				err = f.dirOp(t, nfsc.NFSProc3RmDir, fh, e.Name())
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Shutdown unmounts.
func (f *Fs) Shutdown(ctx context.Context) error {
	limit, err := f.lock(ctx, callLimit)
	if err != nil {
		// Still busy: closing the connection under a request that waits
		// would crash the NFS client, so close it once that request ends.
		go func() {
			f.mu <- struct{}{}
			defer f.unlock()
			f.disconnect()
		}()
		return nil
	}
	defer f.unlock()
	f.connMu.Lock()
	mount := f.mount
	f.connMu.Unlock()
	if mount != nil {
		// Bounded like any call, and a reply the NFS client panics on fails
		// only the unmount: Shutdown runs in the background, where a panic
		// would end the app.
		_ = f.boundedWithin(limit, mount.Unmount)
	}
	f.disconnect()
	return nil
}
