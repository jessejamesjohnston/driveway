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
	// An address in the settings that Go can't read is the settings' fault,
	// not the network's (a url.Error is a net.Error too), and Go's text
	// repeats the address, password and all, so none of it is kept.
	case unreadableAddress(err):
		return tag("badsettings", &addressError{err})
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

// parseWords are the endings of Go's url.Parse errors, for an error whose
// chain was cut by %v.
var parseWords = []string{"invalid userinfo", "invalid port", "invalid url escape", "invalid character",
	"missing protocol scheme", "first path segment in url cannot contain colon", "invalid control character in url"}

// unreadableAddress: err comes from an address Go couldn't read (url.Parse
// failed, or the HTTP client found no scheme it knows).
func unreadableAddress(err error) bool {
	found := false
	eachWrapped(err, func(e error) {
		if ue, ok := e.(*url.Error); ok && (ue.Op == "parse" || strings.HasPrefix(ue.Err.Error(), "unsupported protocol scheme")) {
			found = true
		}
	})
	if found {
		return true
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "unsupported protocol scheme") {
		return true
	}
	if !strings.Contains(s, `parse "`) {
		return false
	}
	for _, w := range parseWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
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
