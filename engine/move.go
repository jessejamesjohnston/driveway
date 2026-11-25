// SPDX-License-Identifier: Apache-2.0

package engine

// New folders, deletes, moves and renames on the server.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
	rsync "github.com/rclone/rclone/fs/sync"
	"github.com/rclone/rclone/fs/walk"
)

// Mkdir makes the folder p and any missing parents.
func Mkdir(id, p string) error {
	f, c, done, err := begin(id)
	if err != nil {
		return err
	}
	defer done()
	w, cancel := op(c)
	defer cancel()
	return classify(f.Mkdir(w, clean(p)))
}

// Delete removes the file or folder p; a folder goes with everything in it.
func Delete(id, p string) error {
	return DeleteWithin(id, p, 0)
}

// DeleteWithin is Delete within what is left of Files' wait (beginWithin).
func DeleteWithin(id, p string, budgetMs int64) error {
	f, call, done, err := beginWithin(id, budgetMs)
	if err != nil {
		return err
	}
	defer done()
	p = clean(p)
	c, cancel := op(call)
	defer cancel()
	if o, err := f.NewObject(c, p); err == nil {
		return classify(o.Remove(c))
	} else if !errors.Is(err, fs.ErrorObjectNotFound) && !errors.Is(err, fs.ErrorIsDir) && !errors.Is(err, fs.ErrorNotAFile) &&
		(strings.Contains(p, "/") || !f.Features().BucketBased) {
		return classify(err) // not known to be a folder (or a bucket): never purge on a guess
	}
	// A whole bucket on an object store: rclone's folder markers trip over
	// the bucket's own (empty) marker name, so delete it with markers off.
	if !strings.Contains(p, "/") && f.Features().BucketBased {
		if t, _ := store.GetValue(id, "directory_markers"); t == "true" {
			if plain, err := fs.NewFs(c, id+",directory_markers=false:"); err == nil {
				defer shutdown(plain) // made for this delete only
				f = plain
			}
		}
	}
	if purge := f.Features().Purge; purge != nil {
		return classify(purge(c, p))
	}
	return classify(purgeSlow(c, f, p))
}

func purgeSlow(c context.Context, f fs.Fs, dir string) error {
	es, err := f.List(c, dir)
	if err != nil {
		return err
	}
	for _, e := range es {
		switch x := e.(type) {
		case fs.Object:
			if err := x.Remove(c); err != nil {
				return err
			}
		case fs.Directory:
			if err := purgeSlow(c, f, x.Remote()); err != nil {
				return err
			}
		}
	}
	return f.Rmdir(c, dir)
}

// free reports whether nothing is at p: only a clear "not found" counts, so
// a check that failed for another reason never lets something be overwritten.
func free(c context.Context, f fs.Fs, p string) error {
	_, err := stat(c, f, p)
	switch {
	case err == nil:
		return tag("exists", fmt.Errorf("%s already exists", path.Base(p)))
	case strings.HasPrefix(err.Error(), "[notfound]"):
		return nil
	}
	return err
}

// Move moves the file or folder from to to (Files' move). It fails if to
// exists. A folder on a service with no folder rename (object stores) fails
// at once with [unsupported]: Files then moves what's inside one by one
// (each file on the server, by Move) and deletes the emptied folder, so no
// single call has to move a whole folder inside Files' time limit.
func Move(id, from, to string) error {
	return move(id, from, to, false, 0)
}

// MoveWithin is Move within what is left of Files' wait (beginWithin).
func MoveWithin(id, from, to string, budgetMs int64) error {
	return move(id, from, to, false, budgetMs)
}

// Rename renames the file or folder from to to. Files has no other way to
// rename, so a folder on a service with no folder rename is moved here, one
// object at a time on the server, but only when it is small enough to
// finish well inside Files' time limit: a bigger one fails with [toobig]
// before anything moves, rather than stopping half way.
func Rename(id, from, to string) error {
	return move(id, from, to, true, 0)
}

// RenameWithin is Rename within what is left of Files' wait (beginWithin).
func RenameWithin(id, from, to string, budgetMs int64) error {
	return move(id, from, to, true, budgetMs)
}

// renameDirMax is the most objects a folder rename moves one by one, and
// renameBudget how long it may expect to take, estimated from how long
// counting them took (Files waits about 20 seconds).
const (
	renameDirMax = 100
	renameBudget = 10 * time.Second
)

// renameLimit bounds the move itself, whatever the estimate said, inside the
// call's own limit (a variable so a test can stop a move part way without
// waiting 14 seconds).
var renameLimit = 14 * time.Second

func move(id, from, to string, rename bool, budgetMs int64) error {
	f, call, done, err := beginWithin(id, budgetMs)
	if err != nil {
		return err
	}
	defer done()
	from, to = clean(from), clean(to)
	c, cancel := op(call)
	defer cancel()
	if err := free(c, f, to); err != nil {
		return err
	}
	o, err := f.NewObject(c, from)
	if err != nil && !errors.Is(err, fs.ErrorIsDir) && !errors.Is(err, fs.ErrorObjectNotFound) && !errors.Is(err, fs.ErrorNotAFile) {
		return classify(err)
	}
	if err == nil {
		if mv := f.Features().Move; mv != nil {
			_, err := mv(c, o, to)
			return classify(err)
		}
		// Object stores have no rename: copy on the server, then delete.
		if cp := f.Features().Copy; cp != nil {
			copied, err := cp(c, o, to)
			if err != nil {
				return classify(err)
			}
			if err := o.Remove(c); err != nil {
				// Its own limit, even past the call's end: the check decides
				// which of the two names keeps the file.
				uc, ucancel := context.WithTimeout(context.Background(), opTimeout)
				defer ucancel()
				// The delete may have happened with only its answer lost: then
				// the copy is the file now, and it stays.
				_, nerr := f.NewObject(uc, from)
				if errors.Is(nerr, fs.ErrorObjectNotFound) {
					return nil
				}
				if nerr != nil {
					// Can't tell whether the original is still there: keep
					// the copy, so at least one of the two names holds the
					// file.
					return tag("bothnames", fmt.Errorf("copied %s to %s but couldn't check that the original was removed; both names may now hold it", path.Base(from), path.Base(to)))
				}
				// The original is there for sure: take the copy back, so the
				// rename can simply be tried again.
				_ = copied.Remove(uc)
				return classify(err)
			}
			return nil
		}
		return tag("norename", errors.New("this service can't rename files"))
	}
	if dm := f.Features().DirMove; dm != nil {
		return classify(dm(c, f, from, to))
	}
	if !rename {
		return tag("unsupported", errors.New("this service can't move a folder in one step"))
	}
	// Folders without a rename: move what's in them, on the server where it
	// can, if there are few enough to finish in time. Counting stops at the
	// limit, within this call's deadline.
	n := 0
	errEnough := errors.New("enough")
	counting := time.Now()
	err = walk.ListR(c, f, from, true, -1, walk.ListObjects, func(es fs.DirEntries) error {
		n += len(es)
		if n > renameDirMax {
			return errEnough
		}
		return nil
	})
	if errors.Is(err, errEnough) {
		return tag("toobig", fmt.Errorf("%s holds more than %d files", path.Base(from), renameDirMax))
	}
	if err != nil {
		return classify(err)
	}
	// Counting took at least one round trip. Moving takes about a dozen
	// (looking at both folders, tidying up after) plus three per object
	// (copy, check, delete): a slow server gets a smaller folder, and so
	// does a call with less time left (a slow connection used some).
	if est := time.Duration(12+n*3) * time.Since(counting); est > within(call, renameBudget) {
		return tag("toobig", fmt.Errorf("moving %d files would take about %v", n, est.Round(time.Second)))
	}
	// The estimate can be wrong (the server slows down): the move itself
	// stops at its own limit or the call's end, and says how far it got.
	// Opening the two folders counts toward that limit: these connections
	// serve only this move, so unlike a cached one they can be made on its
	// deadline, and are closed when it ends.
	mc, mcancel := context.WithTimeout(call, renameLimit)
	defer mcancel()
	src, err := fs.NewFs(mc, id+":"+from)
	if err != nil {
		return classify(err)
	}
	defer shutdown(src)
	dst, err := fs.NewFs(mc, id+":"+to)
	if err != nil {
		return classify(err)
	}
	defer shutdown(dst)
	if err := rsync.MoveDir(mc, dst, src, true, true); err != nil {
		if mc.Err() != nil {
			return tag("partway", fmt.Errorf("the rename of %s stopped part way: some files are in %s and the rest are still in %s; move them across to finish", path.Base(from), path.Base(to), path.Base(from)))
		}
		return classify(err)
	}
	_ = operations.Rmdirs(mc, src, "", false)
	return nil
}
