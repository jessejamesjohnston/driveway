// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// holdV6 stands in for another app on the device listening on
// [::1]:53682. Skips only where the test host has no IPv6 loopback; a port
// an earlier test is still letting go of is waited for, not skipped.
func holdV6(t *testing.T) net.Listener {
	t.Helper()
	l, err := bindLoopback("[::1]:" + loopbackPort)
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("[::1]:%s still held by an earlier test: %v", loopbackPort, err)
	}
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// A browser may deliver "localhost" to ::1, so a sign-in whose receiver
// can't hold ::1 because another process does must not start: that process
// would get the code and state.
func TestListenRefusesWhenV6LoopbackIsTaken(t *testing.T) {
	holdV6(t)
	start := time.Now()
	r, err := listen("state", "Dropbox")
	if err == nil {
		r.stop()
		t.Fatal("listen started a sign-in while another listener held [::1]:53682")
	}
	if !errors.Is(err, errV6Taken) {
		t.Fatalf("err = %v, want errV6Taken", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("refusal took %v", time.Since(start))
	}
	// The 127.0.0.1 listener it opened first was released.
	l, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatalf("127.0.0.1:%s still held after the refusal: %v", loopbackPort, err)
	}
	_ = l.Close()
}

func TestListenHoldsBothLoopbacks(t *testing.T) {
	r, err := listen("state", "Dropbox")
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()
	if _, err := net.Listen("tcp", "[::1]:"+loopbackPort); err == nil {
		t.Skip("no IPv6 loopback here")
	} else if !errors.Is(err, errAddrInUse()) {
		t.Fatalf("[::1] not held by the receiver: %v", err)
	}
}

// rclone's own sign-in listens on 127.0.0.1 only; Driveway's guard holds ::1
// and passes the browser on with the same path and query.
func TestRcloneGuardRedirectsV6ToV4(t *testing.T) {
	if l, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	} else {
		_ = l.Close()
	}
	if err := guardRcloneSignIn(); err != nil {
		t.Fatal(err)
	}
	defer releaseRcloneGuard()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get("http://[::1]:" + loopbackPort + "/?code=abc&state=xyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got, want := resp.Header.Get("Location"), "http://127.0.0.1:"+loopbackPort+"/?code=abc&state=xyz"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	releaseRcloneGuard()
	l, err := net.Listen("tcp", "[::1]:"+loopbackPort)
	if err != nil {
		t.Fatalf("[::1] still held after release: %v", err)
	}
	_ = l.Close()
}

func TestRcloneGuardRefusesWhenV6LoopbackIsTaken(t *testing.T) {
	holdV6(t)
	if err := guardRcloneSignIn(); !errors.Is(err, errV6Taken) {
		releaseRcloneGuard()
		t.Fatalf("err = %v, want errV6Taken", err)
	}
}

func errAddrInUse() error { return syscall.EADDRINUSE }

// rclone may open its page twice in one sign-in; the guard takes ::1 over
// from itself instead of refusing.
func TestRcloneGuardSecondOpen(t *testing.T) {
	if l, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	} else {
		_ = l.Close()
	}
	defer releaseRcloneGuard()
	if err := guardRcloneSignIn(); err != nil {
		t.Fatal(err)
	}
	if err := guardRcloneSignIn(); err != nil {
		t.Fatalf("second open refused: %v", err)
	}
}

// The person is told why: Driveway's own sign-in, held off by another
// listener on [::1], ends as [port] (Errors.java names the cause) and no
// page opens.
func TestOwnSignInPortTakenIsPlain(t *testing.T) {
	holdV6(t)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-port")
	_, err := ConfigStart("t-port", "dropbox", `{"auth_url":"http://127.0.0.1:9/authorize","token_url":"http://127.0.0.1:9/token"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "[port]") {
		t.Fatalf("err = %v, want [port]", err)
	}
	if len(h.urls) != 0 {
		t.Fatalf("a page opened: %q", h.urls)
	}
}

// rclone's own sign-in (someone else's client ID) refused the same way, not
// as a cancelled or lost sign-in, with its 127.0.0.1 receiver closed and
// nothing left over for the next setup.
func TestRcloneSignInPortTakenIsPlain(t *testing.T) {
	holdV6(t)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-port2")
	start := time.Now()
	_, err := ConfigStart("t-port2", "dropbox", `{"client_id":"someone-else","client_secret":"s","auth_url":"http://127.0.0.1:9/hang","token_url":"http://127.0.0.1:9/token"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "[port]") {
		t.Fatalf("err = %v, want [port]", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("refusal took %v", time.Since(start))
	}
	if len(h.urls) != 0 {
		t.Fatalf("a page opened: %q", h.urls)
	}
	if r := takeGuardRefusal(); r != nil {
		t.Fatalf("refusal left over for the next setup: %v", r)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatalf("127.0.0.1:%s still held after the refusal: %v", loopbackPort, err)
	}
	_ = l.Close()
}
