// SPDX-License-Identifier: Apache-2.0

package engine

// Copies on the server itself, and a server's free space.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/rclone/rclone/fs"
)

// copyLimit is the largest file copied on the server. Files waits about 20
// seconds for a copy and stops Driveway after that; a bigger file (or a slow
// disk) could take longer, so Files copies those itself, through the device,
// where there is no such limit.
const copyLimit = 512 << 20

// Copy copies the file from to to on the server itself, without the bytes
// passing through the device. It fails with [unsupported] when the service
// can't (SMB, FTP, NFS, folders); the app then copies the ordinary way.
func Copy(id, from, to string) (err error) {
	defer scrub(&err)
	f, call, done, err := begin(id)
	if err != nil {
		return err
	}
	defer done()
	return copyTo(call, f, id, clean(from), clean(to))
}

// CopyNew copies the file from into the folder dir on the server, under
// name or, when that is taken, "name (1).ext" and so on, as Android's own
// providers name copies; it returns the name used. Picking the name and
// copying are one call, within what is left of Files' wait (beginWithin). A
// name the listing missed is never written over: dir is listed again and
// another name picked.
func CopyNew(id, from, dir, name string, budgetMs int64) (_ string, err error) {
	defer scrub(&err)
	f, call, done, err := beginWithin(id, budgetMs)
	if err != nil {
		return "", err
	}
	defer done()
	from, dir = clean(from), clean(dir)
	names := map[string]bool{}
	for attempt := 0; attempt < 2; attempt++ {
		c, cancel := op(call)
		es, err := f.List(c, dir)
		cancel()
		if err != nil {
			return "", classify(err)
		}
		for _, e := range es {
			names[path.Base(e.Remote())] = true
		}
		pick := freeName(names, name)
		err = copyTo(call, f, id, from, path.Join(dir, pick))
		if err == nil {
			return pick, nil
		}
		if !strings.HasPrefix(err.Error(), "[exists]") {
			return "", err
		}
		names[pick] = true
	}
	return "", tag("exists", fmt.Errorf("%s already exists", name))
}

// copyTo is a copy on the server within the call call.
func copyTo(call context.Context, f fs.Fs, id, from, to string) error {
	c, cancel := op(call)
	defer cancel()
	if t, _ := store.GetValue(id, "type"); t == "sftp" {
		if err := free(c, f, to); err != nil {
			return err
		}
		o, err := f.NewObject(c, from)
		if err != nil {
			return classify(err)
		}
		if o.Size() > copyLimit {
			return tag("unsupported", errors.New("too big to copy on the server inside Files' time limit"))
		}
		return sftpCopy(call, id, from, to)
	}
	cp := f.Features().Copy
	if cp == nil {
		return tag("unsupported", errors.New("this service can't copy on the server"))
	}
	o, err := f.NewObject(c, from)
	if err != nil {
		return classify(err)
	}
	if o.Size() > copyLimit {
		return tag("unsupported", errors.New("too big to copy on the server inside Files' time limit"))
	}
	if err := free(c, f, to); err != nil {
		return err
	}
	if _, err := cp(c, o, to); err != nil {
		if errors.Is(err, fs.ErrorCantCopy) {
			return tag("unsupported", err)
		}
		return classify(err)
	}
	return nil
}

// Space returns {"total":..,"free":..} in bytes, or "" if the service doesn't say.
func Space(id string) string {
	f, err := remote(ctx(), id)
	if err != nil {
		return ""
	}
	about := f.Features().About
	if about == nil {
		return ""
	}
	u, err := about(ctx())
	if err != nil || u == nil || u.Total == nil || u.Free == nil {
		return ""
	}
	b, _ := json.Marshal(map[string]int64{"total": *u.Total, "free": *u.Free})
	return string(b)
}
