// SPDX-License-Identifier: Apache-2.0

package nfs

// The Object side of the backend: reading, replacing and removing a file.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// Fs of the object.
func (o *Object) Fs() fs.Info { return o.fs }

// Remote path of the object.
func (o *Object) Remote() string { return o.remote }

// String of the object.
func (o *Object) String() string { return o.remote }

// ModTime of the object.
func (o *Object) ModTime(ctx context.Context) time.Time { return o.modTime }

// Size of the object.
func (o *Object) Size() int64 { return o.size }

// Hash isn't supported.
func (o *Object) Hash(ctx context.Context, ht hash.Type) (string, error) {
	return "", hash.ErrUnsupported
}

// Storable reports that this object can be transferred.
func (o *Object) Storable() bool { return true }

// SetModTime isn't supported.
func (o *Object) SetModTime(ctx context.Context, t time.Time) error { return fs.ErrorCantSetModTime }

// Open reads the file, honoring seek and range options.
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	var offset, limit int64 = 0, -1
	for _, opt := range options {
		switch x := opt.(type) {
		case *fs.SeekOption:
			offset = x.Offset
		case *fs.RangeOption:
			offset, limit = x.Decode(o.size)
		default:
			if opt.Mandatory() {
				fs.Logf(o, "unsupported mandatory option: %v", opt)
			}
		}
	}
	var file *nfsc.File
	err := o.fs.do(ctx, func(t *nfsc.Target) error {
		var err error
		file, err = t.Open(o.fs.abs(o.remote))
		return err
	})
	if isNotExist(err) {
		return nil, fs.ErrorObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return &reader{fs: o.fs, file: file, left: limit}, nil
}

type reader struct {
	fs   *Fs
	file *nfsc.File
	left int64  // -1 for no limit
	buf  []byte // what each request reads into, copied out once it returns
	err  error  // set once a read was given up on
}

func (r *reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.left == 0 {
		return 0, io.EOF
	}
	if r.left > 0 && int64(len(p)) > r.left {
		p = p[:r.left]
	}
	limit, err := r.fs.lock(context.Background(), callLimit)
	if err != nil {
		return 0, err
	}
	// A request given up on can still fill its buffer when its answer
	// arrives, so it never gets the caller's.
	if len(r.buf) < len(p) {
		r.buf = make([]byte, len(p))
	}
	buf := r.buf[:len(p)]
	var n int
	err = r.fs.boundedWithin(limit, func() (err error) {
		n, err = r.file.Read(buf)
		return err
	})
	r.fs.unlock()
	if errors.Is(err, errNoAnswer) {
		// n, buf and the file (on the connection set aside) are still the
		// late request's: this reader is done.
		r.buf, r.err = nil, err
		return 0, err
	}
	copy(p, buf[:n])
	if r.left > 0 {
		r.left -= int64(n)
	}
	return n, err
}

func (r *reader) Close() error { return nil }

// Update replaces the file's contents with in.
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	p := o.fs.abs(o.remote)
	if err := o.fs.mkdirAll(ctx, parent(p)); err != nil {
		return err
	}
	var file *nfsc.File
	err := o.fs.do(ctx, func(t *nfsc.Target) error {
		// Start from an empty file: OpenFile keeps existing contents.
		if err := o.fs.removeEntry(t, nfsc.NFSProc3Remove, p); err != nil && !isNotExist(err) {
			return err
		}
		if err := o.fs.stillOn(t); err != nil {
			return err
		}
		// A save's temporary copy is the owner's alone until it replaces
		// the file (whose mode it then takes, in Move).
		mode := os.FileMode(newFileMode)
		if strings.HasPrefix(path.Base(p), TempPrefix) {
			mode = 0o600
		}
		var err error
		file, err = t.OpenFile(p, mode)
		return err
	})
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	var written int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			limit, werr := o.fs.lock(ctx, callLimit)
			if werr == nil {
				werr = o.fs.boundedWithin(limit, func() error {
					_, err := file.Write(buf[:n])
					return err
				})
				o.fs.unlock()
			}
			if werr != nil {
				return werr
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	limit, cerr := o.fs.lock(ctx, callLimit)
	if cerr == nil {
		cerr = o.fs.boundedWithin(limit, file.Close)
		o.fs.unlock()
	}
	if cerr != nil {
		return cerr
	}
	fi, err := o.fs.getattr(ctx, p)
	if err != nil {
		return err
	}
	if fi.Size() != written {
		return fmt.Errorf("wrote %d bytes but the server has %d", written, fi.Size())
	}
	o.size, o.modTime = fi.Size(), fi.ModTime()
	return nil
}

// Remove deletes the file.
func (o *Object) Remove(ctx context.Context) error {
	err := o.fs.do(ctx, func(t *nfsc.Target) error { return o.fs.removeEntry(t, nfsc.NFSProc3Remove, o.fs.abs(o.remote)) })
	if isNotExist(err) {
		return fs.ErrorObjectNotFound
	}
	return err
}

var (
	_ fs.Fs       = (*Fs)(nil)
	_ fs.Mover    = (*Fs)(nil)
	_ fs.DirMover = (*Fs)(nil)
	_ fs.Purger   = (*Fs)(nil)
	_ fs.Object   = (*Object)(nil)
)
