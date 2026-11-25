// SPDX-License-Identifier: Apache-2.0

package engine

// Error categories: the bracketed kind the app maps to Android's file errors.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/rclone/rclone/fs"
)

func tag(kind string, err error) error {
	if err == nil {
		return nil
	}
	return cleanErr(fmt.Errorf("[%s] %w", kind, err))
}

// classify adds the category the app needs to report the error properly.
// Network failures are recognized first, and HTTP statuses only with their
// words ("401 unauthorized"): a port or a file name (DSC05071.JPG) can hold
// the same digits.
func classify(err error) error {
	if err == nil || strings.HasPrefix(err.Error(), "[") {
		return err
	}
	s := strings.ToLower(err.Error())
	var ne net.Error
	switch {
	// A service that refused to refresh the sign-in answered: an HTTP call
	// carries that answer inside a url.Error, which is a net.Error too.
	case strings.Contains(s, "invalid_grant"):
		return tag("auth", err)
	case errors.As(err, &ne), errors.Is(err, context.DeadlineExceeded), errors.Is(err, io.ErrUnexpectedEOF),
		strings.Contains(s, "connection refused"), strings.Contains(s, "no route to host"),
		strings.Contains(s, "i/o timeout"), strings.Contains(s, "no such host"), strings.Contains(s, "network is unreachable"),
		strings.Contains(s, "can't reach"), strings.Contains(s, "connection abort"), strings.Contains(s, "connection reset"),
		strings.Contains(s, "connection error"), strings.Contains(s, "broken pipe"), strings.Contains(s, "connection lost"),
		strings.Contains(s, "unexpected eof"), strings.HasSuffix(s, ": eof"), strings.Contains(s, "use of closed network connection"),
		strings.Contains(s, "context deadline exceeded"):
		return tag("network", err)
	case errors.Is(err, fs.ErrorObjectNotFound), errors.Is(err, fs.ErrorDirNotFound), errors.Is(err, os.ErrNotExist),
		strings.Contains(s, "not found"), strings.Contains(s, "no such file"), strings.Contains(s, "does not exist"):
		return tag("notfound", err)
	case errors.Is(err, fs.ErrorDirExists), errors.Is(err, os.ErrExist), strings.Contains(s, "already exists"):
		return tag("exists", err)
	case errors.Is(err, fs.ErrorDirectoryNotEmpty), strings.Contains(s, "not empty"):
		return tag("notempty", err)
	case strings.Contains(s, "invalidbucketname"):
		return tag("bucketname", err)
	case strings.Contains(s, "no space"), strings.Contains(s, "disk full"), strings.Contains(s, "disk_full"),
		strings.Contains(s, "quota"), strings.Contains(s, "insufficient storage"), strings.Contains(s, "507 insufficient"),
		strings.Contains(s, "ssh_fx_failure"), strings.Contains(s, "nfs3err_nospc"), strings.Contains(s, "nfs3err_dquot"):
		// SFTP's generic failure is most often a full disk or a read-only place.
		return tag("full", err)
	case strings.Contains(s, "unable to authenticate"), strings.Contains(s, "authentication"), strings.Contains(s, "logon failure"),
		strings.Contains(s, "401 unauthorized"), strings.Contains(s, "invalid_access_token"), strings.Contains(s, "expired_access_token"),
		strings.Contains(s, "invalid_grant"), strings.Contains(s, "token expired"), strings.Contains(s, "unauthorized"),
		strings.Contains(s, "couldn't fetch token"), strings.Contains(s, "failed to refresh token"),
		strings.Contains(s, "host key"), strings.Contains(s, "knownhosts"), strings.Contains(s, "refused to mount"), strings.Contains(s, "ssh: handshake failed"):
		return tag("auth", err)
	case strings.Contains(s, "permission denied"), strings.Contains(s, "access denied"), strings.Contains(s, "access is denied"),
		strings.Contains(s, "403 forbidden"), strings.Contains(s, "forbidden"), strings.Contains(s, "nfs3err_acces"),
		strings.Contains(s, "nfs3err_perm"), errors.Is(err, os.ErrPermission):
		return tag("denied", err)
	}
	return tag("other", err)
}

// addressError stands for an error about an address in the settings that Go
// couldn't read, in Driveway's own words; the error stays reachable for
// errors.Is and errors.As inside the engine.
type addressError struct{ err error }

func (e *addressError) Error() string {
	return "bad settings: an address in the settings can't be read"
}
func (e *addressError) Unwrap() error { return e.err }

// settingsAddressError: err is Go's url package refusing an address in the
// server id's own settings (rclone parses the address it is given when it
// connects), as opposed to an address a server sent (a redirect, a link in
// an answer): only the first is the settings' fault. Text a server controls
// decides nothing here: the address must be one of the server's settings.
func settingsAddressError(id string, err error) bool {
	found := false
	eachWrapped(err, func(e error) {
		ue, ok := e.(*url.Error)
		if !ok || ue.Op != "parse" {
			return
		}
		// Go names the address as rclone gave it, maybe with a / added, or
		// cut at a # (alice:pass#word@host).
		got := strings.TrimRight(ue.URL, "/")
		if !strings.Contains(got, "://") || strings.HasSuffix(got, "://") {
			return
		}
		for _, k := range store.GetKeyList(id) {
			if v, _ := store.GetValue(id, k); v != "" && strings.HasPrefix(strings.TrimSpace(v)+"/", got) {
				found = true
			}
		}
	})
	return found
}

// eachWrapped calls f on err and every error it wraps.
func eachWrapped(err error, f func(error)) {
	if err == nil {
		return
	}
	f(err)
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		eachWrapped(u.Unwrap(), f)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			eachWrapped(e, f)
		}
	}
}
