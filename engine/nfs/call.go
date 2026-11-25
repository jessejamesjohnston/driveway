// SPDX-License-Identifier: Apache-2.0

package nfs

// One call at a time, within its deadline: the lock, the limits and the path helpers every call uses.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// do runs fn against the mounted export, reconnecting once if the connection
// dropped (the server restarted, the network changed).
// The call ends by ctx's deadline when it has one sooner than callLimit,
// time spent waiting for an earlier call included.
func (f *Fs) do(ctx context.Context, fn func(t *nfsc.Target) error) error {
	return f.doWithin(ctx, callLimit, fn)
}

// doWithin is do for an fn of many requests (a whole folder's listing or
// deletion), with its own time limit.
func (f *Fs) doWithin(ctx context.Context, limit time.Duration, fn func(t *nfsc.Target) error) error {
	limit, err := f.lock(ctx, limit)
	if err != nil {
		return err
	}
	defer f.unlock()
	epoch := f.currentEpoch()
	return f.boundedWithin(limit, func() error {
		t := f.current()
		if t == nil {
			var err error
			if t, err = f.connect(epoch); err != nil {
				return err
			}
		}
		err := fn(t)
		if isConnError(err) {
			var cerr error
			if t, cerr = f.connect(epoch); cerr != nil {
				return cerr
			}
			err = fn(t)
		}
		return err
	})
}

// TempPrefix starts the names of the temporary files saves are uploaded
// under (engine.upload) before they are renamed into place.
const TempPrefix = ".driveway-"

// OldPrefix starts the name a file is moved aside to while a save replaces
// it the slow way (engine.replace); after a failed swap it is moved back
// and must keep its own mode.
const OldPrefix = TempPrefix + "old-"

// isUploadTemp reports whether name is a save's temporary copy (made
// owner-only while it uploads), not a file moved aside under OldPrefix.
func isUploadTemp(name string) bool {
	return strings.HasPrefix(name, TempPrefix) && !strings.HasPrefix(name, OldPrefix)
}

// newFileMode is the mode of a file Driveway makes.
const newFileMode = 0o664

// callLimit bounds one call to the server. The NFS client resends a request
// that gets no answer every 5 seconds, up to 100 times (about 8 minutes), so
// a server that went away mid-session would hold every call that long, and
// Files stops Driveway after about 20 seconds. One request moves at most
// about 1 MB, so 10 s is generous even over a slow link. Calls of many
// requests (listing or deleting a whole folder) get folderLimit. (callLimit
// is a variable so a test can give up on a read without waiting 10 s.)
var callLimit = 10 * time.Second

const folderLimit = 2 * time.Minute

var (
	errNoAnswer = fmt.Errorf("the NFS server stopped answering: %w", os.ErrDeadlineExceeded)
	errBusy     = fmt.Errorf("the NFS server is still busy with an earlier request: %w", os.ErrDeadlineExceeded)
)

// lock waits for the connection, for at most limit or until ctx's deadline,
// whichever comes first, and returns how long the call itself may then take
// (at least a second). A caller that can't have the connection in time
// fails as a server that didn't answer would. Release with unlock.
func (f *Fs) lock(ctx context.Context, limit time.Duration) (time.Duration, error) {
	until := time.Now().Add(limit)
	if d, ok := ctx.Deadline(); ok && d.Before(until) {
		until = d
	}
	wait := time.NewTimer(time.Until(until))
	defer wait.Stop()
	select {
	case f.mu <- struct{}{}:
	case <-wait.C:
		return 0, errBusy
	case <-ctx.Done():
		return 0, errBusy
	}
	// Both can be ready at once (select picks either): a call whose time is
	// already up never starts a request.
	left := time.Until(until)
	if left <= 0 || ctx.Err() != nil {
		<-f.mu
		return 0, errBusy
	}
	return left, nil
}

func (f *Fs) unlock() { <-f.mu }

// boundedWithin runs fn, giving up after limit. The stuck request can't be
// interrupted (the NFS client crashes if its connection is closed while a
// request waits), so its connection is set aside, closed when the request
// ends by itself, and the next call connects afresh. Caller holds the lock.
func (f *Fs) boundedWithin(limit time.Duration, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		// The NFS client panics on some replies it can't read (a denied
		// call, one cut short): fail the call, not the whole app.
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("the NFS server sent a reply that can't be read (%v)", r)
			}
		}()
		done <- fn()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
	}
	f.connMu.Lock()
	f.epoch++
	mount, target := f.mount, f.target
	f.mount, f.target = nil, nil
	f.connMu.Unlock()
	go func() {
		<-done
		if target != nil {
			target.Close()
		}
		if mount != nil {
			mount.Close()
		}
	}()
	return errNoAnswer
}

func isConnError(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed)
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

func isNotEmpty(err error) bool { return nfsc.IsNotEmptyError(err) }

func parent(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// abs is the path inside the export.
func (f *Fs) abs(remote string) string {
	p := path.Join(f.root, remote)
	if p == "" {
		return "."
	}
	return p
}

func (f *Fs) getattr(ctx context.Context, p string) (os.FileInfo, error) {
	var fi os.FileInfo
	err := f.do(ctx, func(t *nfsc.Target) error {
		var err error
		if p == "" || p == "." {
			fi, err = t.Getattr(".")
		} else {
			fi, _, err = t.Lookup(p)
		}
		return err
	})
	if err != nil {
		// fi isn't ours to read: a request given up on may still set it.
		return nil, err
	}
	return fi, nil
}
