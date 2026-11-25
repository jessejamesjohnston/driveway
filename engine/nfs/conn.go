// SPDX-License-Identifier: Apache-2.0

package nfs

// Connecting: dialing the server, mounting the export, and noticing a connection that has gone.

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

// connect drops the current connection, then dials and mounts, unless the
// call it belongs to (epoch) was given up on. Caller holds the lock.
func (f *Fs) connect(epoch int) (*nfsc.Target, error) {
	f.connMu.Lock()
	if f.epoch != epoch {
		f.connMu.Unlock()
		return nil, errNoAnswer
	}
	f.closeLocked()
	f.connMu.Unlock()
	// The NFS client dials with no time limit: check the server answers at
	// all first, so an address that's gone fails in seconds, not minutes.
	port := 111 // the portmapper
	if f.opt.Port > 0 {
		port = f.opt.Port
	}
	probe, err := net.DialTimeout("tcp", net.JoinHostPort(f.opt.Host, strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("can't reach the NFS server %s: %w", f.opt.Host, err)
	}
	_ = probe.Close()
	// The NFS client binds each connection to a random local port and
	// gives up if that one is taken (a recent connection's, still in
	// TIME_WAIT): try a few.
	var mount *nfsc.Mount
	var target *nfsc.Target
	for try := 1; ; try++ {
		mount, target, err = f.dialAndMount()
		if err == nil || try == addrTries || !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if f.epoch != epoch {
		// Given up on while it was connecting: the caller has moved on.
		target.Close()
		mount.Close()
		return nil, errNoAnswer
	}
	f.mount, f.target = mount, target
	return target, nil
}

// dialAndMount dials the server and mounts the export.
func (f *Fs) dialAndMount() (*nfsc.Mount, *nfsc.Target, error) {
	// The NFS client joins host and port with a bare colon: an IPv6
	// address needs its brackets (the probe's JoinHostPort adds them).
	host := f.opt.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	var mount *nfsc.Mount
	var err error
	if f.opt.Port > 0 {
		// One port for MOUNT and NFS (rclone serve nfs, some appliances).
		var c *rpc.Client
		c, err = dialAtPort(host, f.opt.Port)
		if err == nil {
			mount = &nfsc.Mount{Client: c}
		}
	} else {
		mount, err = nfsc.DialMount(host, 0)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("can't reach the NFS server %s: %w", f.opt.Host, err)
	}
	// The NFS client panics on some replies it can't read (boundedWithin
	// recovers): close the connection first, or it and its reader stay.
	defer func() {
		if r := recover(); r != nil {
			mount.Close()
			panic(r)
		}
	}()
	target, err := mount.Mount(f.opt.Export, f.auth)
	if err != nil {
		mount.Close()
		return nil, nil, mountError(f.opt.Export, err)
	}
	return mount, target, nil
}

// dialAtPort dials MOUNT and NFS on one port. A package var so a test can
// make the local port look taken (the client picks it at random).
var dialAtPort = nfsc.DialServiceAtPort

// addrTries is how many local ports connect tries.
const addrTries = 5

func (f *Fs) currentEpoch() int {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	return f.epoch
}

func (f *Fs) current() *nfsc.Target {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	return f.target
}

// stillOn fails with errNoAnswer once the call running on t was given up on
// (boundedWithin set t aside). An fn of several requests checks it before
// each one that changes something: a request answered late must not lead to
// changes after the caller has reported failure and the person moved on.
func (f *Fs) stillOn(t *nfsc.Target) error {
	if f.current() != t {
		return errNoAnswer
	}
	return nil
}
