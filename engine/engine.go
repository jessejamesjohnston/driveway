// SPDX-License-Identifier: Apache-2.0

// Package engine is Driveway's file engine: rclone, behind a small API the
// Android app calls through gomobile. The app keeps the list of servers and
// their secrets; it hands each one to SetRemote, and rclone never reads or
// writes a config file.
//
// Errors carry a category in brackets at the start of the message, which the
// app maps to Android's file errors: [notfound], [exists], [notempty],
// [auth], [network] or [other].
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/obscure"
)

var (
	mu      sync.Mutex
	store   = newMemStore()
	remotes = map[string]fs.Fs{} // id -> the remote's root, made on first use
	gens    = map[string]int{}   // id -> bumped whenever its settings or connections change
	ready   sync.Once
)

// callLimit bounds one call Files waits on (a stat, a new document, a
// rename, a copy), from start to finish, connecting included: Files gives a
// provider about 20 seconds, and this is rclone's 10 s connect plus an 8 s
// step, leaving 2 s for the app's side of the call. (A variable so a test
// can use a short one.)
var callLimit = 18 * time.Second

// opTimeout bounds one step of such a call (a lookup, a delete, a rename's
// checks); it never runs past the call's own end.
const opTimeout = 8 * time.Second

// begin starts one call Files waits on: c ends callLimit from now, and the
// server's connection is waited for within it. Every step of the call takes
// its context from c, so whatever its own limit, it ends with the call.
func begin(id string) (fs.Fs, context.Context, context.CancelFunc, error) {
	return beginWithin(id, 0)
}

// beginWithin is begin for a call the app has already spent part of Files'
// wait on (waiting for a save of the same file): budgetMs is what is left,
// and the call ends then if that is sooner than callLimit. 0 or less: the
// usual callLimit.
func beginWithin(id string, budgetMs int64) (fs.Fs, context.Context, context.CancelFunc, error) {
	limit := callLimit
	if b := time.Duration(budgetMs) * time.Millisecond; b > 0 && b < limit {
		limit = b
	}
	c, cancel := context.WithTimeout(context.Background(), limit)
	f, err := remote(c, id)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	return f, c, cancel, nil
}

// op is the context for one step of the call c: opTimeout from now, or the
// call's end if that comes first.
func op(c context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c, opTimeout)
}

// within is limit, or the time c has left if that is less.
func within(c context.Context, limit time.Duration) time.Duration {
	if d, ok := c.Deadline(); ok && time.Until(d) < limit {
		return time.Until(d)
	}
	return limit
}

func setup() {
	ready.Do(func() {
		config.SetData(store)
		ci := fs.GetConfig(context.Background())
		ci.ConnectTimeout = fs.Duration(10 * time.Second)
		ci.Timeout = fs.Duration(60 * time.Second)
		ci.LowLevelRetries = 2
		ci.UserAgent = "Driveway"
		ci.LogLevel = fs.LogLevelNotice
		redactLogs()
	})
}

func ctx() context.Context { return context.Background() }

// SetRemote adds or replaces the server id. backend is an rclone backend name
// ("sftp", "smb", "webdav", "nfs", ...); params is a JSON object of its
// options, with passwords in the clear (they're obscured here, as rclone
// expects).
func SetRemote(id, backend, params string) (err error) {
	defer scrub(&err)
	setup()
	ri, err := fs.Find(backend)
	if err != nil {
		return tag("other", fmt.Errorf("unknown service %q", backend))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(params), &m); err != nil {
		return tag("badsettings", fmt.Errorf("bad settings: %w", err))
	}
	secret := map[string]bool{}
	for _, o := range ri.Options {
		if o.IsPassword {
			secret[o.Name] = true
		}
	}
	applyDefaults(ri, m)
	// A server signed in with Merriment Labs' Box registration: the app
	// never holds the registration's secret (ConfigValues leaves it out),
	// and rclone needs it for every token refresh.
	if reg, ok := usesOwnRegistration(backend, m); ok && reg.Secret != "" &&
		fmtValue(m["client_id"]) == reg.ClientID && fmtValue(m["client_secret"]) == "" {
		m["client_secret"] = reg.Secret
	}
	mu.Lock()
	defer mu.Unlock()
	dropRemote(id)
	store.DeleteSection(id)
	store.setQuiet(id, "type", backend)
	for k, v := range m {
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		s = oneLine(s)
		if k == "host" {
			// An IPv6 address in the form this backend needs (hosts.go).
			s = hostFor(backend, s)
		}
		if k == "key_pem" {
			// rclone reads the PEM as one line with \n escapes.
			s = strings.ReplaceAll(strings.TrimSpace(s), "\n", `\n`)
		}
		if secret[k] {
			if s, err = obscure.Obscure(s); err != nil {
				return tag("other", err)
			}
		}
		store.setQuiet(id, k, s)
	}
	return nil
}

// defaults are settings Driveway needs that rclone leaves off, unless the
// person set them: on FTP, list hidden files (Driveway's temporary upload
// names start with a dot, and servers like vsftpd hide those, so a finished
// upload couldn't be found); on object stores, keep empty folders (a folder
// made in Files would vanish until something was put in it).
var defaults = map[string]string{"force_list_hidden": "true", "directory_markers": "true"}

func applyDefaults(ri *fs.RegInfo, m map[string]any) {
	for _, o := range ri.Options {
		if v, ok := defaults[o.Name]; ok {
			if _, set := m[o.Name]; !set {
				m[o.Name] = v
			}
		}
	}
}

// RemoveRemote forgets the server id and closes its connections.
func RemoveRemote(id string) {
	mu.Lock()
	defer mu.Unlock()
	dropRemote(id)
	store.DeleteSection(id)
}

// dropRemote forgets the cached remote and closes its connections. Caller
// holds mu.
func dropRemote(id string) {
	gens[id]++
	if f, ok := remotes[id]; ok {
		delete(remotes, id)
		shutdown(f)
	}
}

// shutdown closes a remote's connections in the background (closing can wait
// on the network).
func shutdown(f fs.Fs) {
	if s := f.Features().Shutdown; s != nil {
		go func() { _ = s(context.Background()) }()
	}
}

// reset closes the server's connections; the next call opens new ones. Only
// for SFTP and SMB: rclone closes their idle connections and leaves busy
// ones alone, while NFS's would cut off every read in progress (and NFS
// reconnects by itself).
func reset(id string) {
	if t, _ := store.GetValue(id, "type"); t != "sftp" && t != "smb" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	dropRemote(id)
}

// remote returns the server's root, connecting on first use, and waits for
// the connection no longer than c allows. The connection is made without
// holding the lock, so a server that doesn't answer can't hold up the
// others, and on context.Background() (some backends keep the context they
// were made with): one that finishes after its caller gave up is still kept
// for the next call.
func remote(c context.Context, id string) (fs.Fs, error) {
	setup()
	for try := 1; ; try++ {
		mu.Lock()
		if f, ok := remotes[id]; ok {
			mu.Unlock()
			return f, nil
		}
		if !store.HasSection(id) {
			mu.Unlock()
			return nil, tag("notfound", fmt.Errorf("no server %q", id))
		}
		gen := gens[id]
		mu.Unlock()
		got := make(chan connected, 1) // never blocks a connection that finishes late
		go func() { got <- connect(id, gen) }()
		var r connected
		select {
		case r = <-got:
		case <-c.Done():
			return nil, tag("network", fmt.Errorf("connecting took too long: %w", c.Err()))
		}
		if r.f != nil || r.err != nil {
			return r.f, r.err
		}
		if try == 2 {
			return nil, tag("retry", errors.New("the server's settings changed while connecting"))
		}
	}
}

// connected is what connect made: the connection to use, or an error, or
// neither when the settings changed meanwhile (look again).
type connected struct {
	f   fs.Fs
	err error
}

// connect makes the server's connection with the settings of generation gen
// and keeps it for later calls. Not kept, it is closed, as nothing else would
// close it: another call connected first (that one is used), or the server
// was removed or its settings changed while this one connected.
func connect(id string, gen int) connected {
	f, err := fs.NewFs(context.Background(), id+":")
	if err != nil {
		return connected{err: classify(err)}
	}
	mu.Lock()
	cur, other := remotes[id]
	kept := !other && gens[id] == gen
	if kept {
		remotes[id] = f
	}
	mu.Unlock()
	if kept {
		return connected{f: f}
	}
	shutdown(f)
	return connected{f: cur}
}

// Check connects to the server id and lists its top folder: the app's
// "Test connection".
func Check(id string) (err error) {
	defer scrub(&err)
	mu.Lock()
	dropRemote(id)
	mu.Unlock()
	f, err := remote(ctx(), id)
	if err != nil {
		return err
	}
	_, err = f.List(ctx(), "")
	return classify(err)
}
