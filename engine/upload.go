// SPDX-License-Identifier: Apache-2.0

package engine

// Saving a file whole or not at all: upload beside the original, check, rename over it.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/object"
)

// Upload writes the local file src to p on the server, without ever leaving
// p half-written: it uploads to a temporary name beside p, checks the size,
// then renames it over p. If anything fails, p is as it was.
func Upload(id, p, src string) error {
	return UploadWithProgress(id, p, src, nil)
}

// Progress hears how far an upload has got.
type Progress interface {
	Update(done, total int64)
}

// counter reports bytes read to a Progress, at most a few times a second.
type counter struct {
	io.Reader
	prog  Progress
	done  int64
	total int64
	last  time.Time
}

func (c *counter) Read(b []byte) (int, error) {
	n, err := c.Reader.Read(b)
	c.done += int64(n)
	if c.prog != nil && (time.Since(c.last) > 250*time.Millisecond || err == io.EOF) {
		c.last = time.Now()
		c.prog.Update(c.done, c.total)
	}
	return n, err
}

// UploadWithProgress is Upload, reporting progress to prog (which may be nil).
func UploadWithProgress(id, p, src string, prog Progress) error {
	in, err := os.Open(src)
	if err != nil {
		return tag("other", err)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return tag("other", err)
	}
	// The temporary name comes from the local copy: a save resumed after
	// Driveway was stopped writes over its own unfinished temporary file
	// instead of leaving it.
	return upload(ctx(), id, p, &counter{Reader: in, prog: prog, total: st.Size()}, st.Size(), st.ModTime(), src)
}

// Create makes an empty file at p (a save dialog's new document) within one
// call's deadline: Files is waiting for the answer.
func Create(id, p string) error {
	_, c, done, err := begin(id)
	if err != nil {
		return err
	}
	defer done()
	w, cancel := op(c)
	defer cancel()
	return upload(w, id, p, bytes.NewReader(nil), 0, time.Now(), "create:"+p)
}

// createLimit bounds CreateNew's work once connected (looking for a free
// name, checking it, making the file), inside the call's own limit.
const createLimit = 15 * time.Second

// CreateNew makes an empty file, or a folder, called name in the folder dir
// (a save dialog's new document, Files' new folder) and returns the name it
// used: name itself, or "name (1).ext" and so on when that is taken, as
// Android's own providers do. taken is a JSON array of names the app knows
// are in dir from a listing it just made, or "" to list dir here. Every
// step, connecting included, shares one deadline. A name that turns out to
// be taken after all (the listing was out of date) is never written over:
// dir is listed again and another name picked.
func CreateNew(id, dir, name string, folder bool, taken string) (string, error) {
	f, call, done, err := begin(id)
	if err != nil {
		return "", err
	}
	defer done()
	c, cancel := context.WithTimeout(call, createLimit)
	defer cancel()
	dir = clean(dir)
	names := map[string]bool{}
	list := taken == ""
	if !list {
		var known []string
		if err := json.Unmarshal([]byte(taken), &known); err != nil {
			list = true
		}
		for _, n := range known {
			names[n] = true
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if list {
			es, err := f.List(c, dir)
			if err != nil {
				return "", classify(err)
			}
			for _, e := range es {
				names[path.Base(e.Remote())] = true
			}
		}
		pick := freeName(names, name)
		p := path.Join(dir, pick)
		err := free(c, f, p)
		if err == nil {
			if folder {
				return pick, classify(f.Mkdir(c, p))
			}
			return pick, upload(c, id, p, bytes.NewReader(nil), 0, time.Now(), "create:"+p)
		}
		if !strings.HasPrefix(err.Error(), "[exists]") {
			return "", err
		}
		names[pick] = true
		list = true
	}
	return "", tag("exists", fmt.Errorf("%s already exists", name))
}

// freeName is name, or "name (n).ext" with the lowest n not in taken.
func freeName(taken map[string]bool, name string) string {
	if !taken[name] {
		return name
	}
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base, ext = name[:i], name[i:]
	}
	for n := 1; ; n++ {
		if c := fmt.Sprintf("%s (%d)%s", base, n, ext); !taken[c] {
			return c
		}
	}
}

// upload puts size bytes from body at p by way of a temporary name made from
// key (the same key, the same temporary name).
func upload(c context.Context, id, p string, body io.Reader, size int64, mtime time.Time, key string) error {
	f, err := remote(c, id)
	if err != nil {
		return err
	}
	p = clean(p)
	if f.Features().Move == nil {
		// No rename on this service (object stores): a single upload
		// replaces the object whole, or not at all.
		o, err := f.Put(c, body, object.NewStaticObjectInfo(p, mtime, size, true, nil, f))
		if err != nil {
			return classify(err)
		}
		return sizeCheck(o, size)
	}
	sum := sha1.Sum([]byte(key))
	tmp := path.Join(parent(p), ".driveway-"+hex.EncodeToString(sum[:4])+"-"+capName(path.Base(p)))
	o, err := f.Put(c, body, object.NewStaticObjectInfo(tmp, mtime, size, true, nil, f))
	// Tidying up after a failure is best effort and short, and has its own
	// limit: a create whose upload ran out of time still removes what it
	// left, up to tidyLimit past the call's end.
	tidy, tidyCancel := context.WithTimeout(context.Background(), tidyLimit)
	defer tidyCancel()
	if err != nil {
		if o != nil {
			_ = o.Remove(tidy)
		} else if old, nerr := f.NewObject(tidy, tmp); nerr == nil {
			_ = old.Remove(tidy)
		}
		// A failed upload can leave the server holding the deleted temporary
		// file open (rclone's SFTP keeps the handle), so its space isn't freed
		// until the connection closes: close them.
		reset(id)
		return classify(err)
	}
	if err := sizeCheck(o, size); err != nil {
		_ = o.Remove(tidy)
		return err
	}
	if err := replace(c, f, o, p); err != nil {
		_ = o.Remove(tidy)
		return err
	}
	return nil
}

// tidyLimit bounds removing a failed upload's temporary file, or a failed
// server copy's partial one (sftpCopy).
const tidyLimit = 4 * time.Second

// replace renames tmp over p. Most services replace p in one step; for those
// that refuse while p exists, p is moved aside first and put back on failure.
func replace(c context.Context, f fs.Fs, tmp fs.Object, p string) error {
	move := f.Features().Move
	if _, err := move(c, tmp, p); err == nil {
		return nil
	} else if isNetwork(err) {
		return classify(err)
	}
	// The rename may have landed and only its answer failed: then the
	// temporary name is gone and p has the new size.
	if _, terr := f.NewObject(c, tmp.Remote()); errors.Is(terr, fs.ErrorObjectNotFound) {
		if now, perr := f.NewObject(c, p); perr == nil && now.Size() == tmp.Size() {
			return nil
		}
	}
	old, err := f.NewObject(c, p)
	if err != nil {
		return classify(fmt.Errorf("can't put the new file in place: %w", err))
	}
	aside := path.Join(parent(p), ".driveway-old-"+token()+"-"+capName(path.Base(p)))
	moved, err := move(c, old, aside)
	if err != nil {
		return classify(fmt.Errorf("can't replace %s: %w", p, err))
	}
	if _, err := move(c, tmp, p); err != nil {
		if _, rerr := move(c, moved, p); rerr != nil {
			return tag("other", fmt.Errorf("couldn't replace %s, and couldn't put the old file back: it is %s", p, aside))
		}
		return classify(err)
	}
	_ = moved.Remove(ctx())
	return nil
}

func sizeCheck(o fs.Object, want int64) error {
	if got := o.Size(); got >= 0 && got != want {
		return tag("other", fmt.Errorf("the server stored %d bytes of %d", got, want))
	}
	return nil
}
