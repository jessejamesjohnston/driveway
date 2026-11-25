// SPDX-License-Identifier: Apache-2.0

package engine

// Listings: a folder's entries and one entry's details, as JSON for the app.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/rclone/rclone/fs"
)

// Entry is one file or folder in a listing.
type entry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // milliseconds since the epoch, 0 if unknown
	Mime  string `json:"mime,omitempty"`
}

func toEntry(e fs.DirEntry) entry {
	t := e.ModTime(ctx())
	out := entry{Name: path.Base(e.Remote()), Size: e.Size()}
	if !t.IsZero() && t.Unix() > 0 {
		out.MTime = t.UnixMilli()
	}
	if _, ok := e.(fs.Directory); ok {
		out.Dir = true
		out.Size = 0
	} else {
		out.Mime = fs.MimeTypeFromName(e.Remote())
	}
	return out
}

// List returns the folder dir ("" for the top) as a JSON array of entries.
func List(id, dir string) (string, error) {
	f, err := remote(ctx(), id)
	if err != nil {
		return "", err
	}
	es, err := f.List(ctx(), clean(dir))
	if err != nil {
		return "", classify(err)
	}
	out := make([]entry, 0, len(es))
	for _, e := range es {
		out = append(out, toEntry(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	b, _ := json.Marshal(out)
	return string(b), nil
}

// ListNow is List within one call's deadline, for the app's synchronous
// calls (picking a free name while Files waits for an answer).
func ListNow(id, dir string) (string, error) {
	f, c, done, err := begin(id)
	if err != nil {
		return "", err
	}
	defer done()
	w, cancel := op(c)
	defer cancel()
	es, err := f.List(w, clean(dir))
	if err != nil {
		return "", classify(err)
	}
	out := make([]entry, 0, len(es))
	for _, e := range es {
		out = append(out, toEntry(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	b, _ := json.Marshal(out)
	return string(b), nil
}

// Stat returns one entry as JSON.
func Stat(id, p string) (string, error) {
	f, c, done, err := begin(id)
	if err != nil {
		return "", err
	}
	defer done()
	w, cancel := op(c)
	defer cancel()
	return stat(w, f, p)
}

func stat(c context.Context, f fs.Fs, p string) (string, error) {
	p = clean(p)
	if p == "" {
		b, _ := json.Marshal(entry{Name: "", Dir: true})
		return string(b), nil
	}
	o, err := f.NewObject(c, p)
	if err == nil {
		b, _ := json.Marshal(toEntry(o))
		return string(b), nil
	}
	if !errors.Is(err, fs.ErrorObjectNotFound) && !errors.Is(err, fs.ErrorIsDir) && !errors.Is(err, fs.ErrorNotAFile) {
		return "", classify(err)
	}
	// Not a file: look for a folder of that name in its parent.
	es, lerr := f.List(c, parent(p))
	if lerr != nil {
		return "", classify(lerr)
	}
	for _, e := range es {
		if d, ok := e.(fs.Directory); ok && d.Remote() == p {
			b, _ := json.Marshal(toEntry(d))
			return string(b), nil
		}
	}
	return "", tag("notfound", fmt.Errorf("%s not found", p))
}
