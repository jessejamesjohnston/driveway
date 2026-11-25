// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

// go-nfs-client binds each connection to a random local port and fails if
// that port is taken (a recent connection's, still in TIME_WAIT: a device
// that just made many has plenty). Connecting tries other ports before it
// gives up, and then says why.
func TestLocalPortTaken(t *testing.T) {
	needServers(t)
	real := dialAtPort
	defer func() { dialAtPort = real }()
	taken := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}
	calls := 0
	dialAtPort = func(host string, port int) (*rpc.Client, error) {
		calls++
		if calls < 3 {
			return nil, taken
		}
		return real(host, port)
	}
	f := connectTo(t, "127.0.0.1", 2050)
	// The real dial can meet a port that is really taken (the very case
	// being tested), and be retried again.
	if calls < 3 {
		t.Fatalf("%d dials, want at least 3", calls)
	}
	if _, err := f.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	calls = 0
	dialAtPort = func(string, int) (*rpc.Client, error) { calls++; return nil, taken }
	_, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{"host": "127.0.0.1", "port": "2050", "export": "/"})
	if !errors.Is(err, syscall.EADDRINUSE) || !strings.HasPrefix(err.Error(), "can't reach the NFS server") {
		t.Fatalf("err %v", err)
	}
	if calls != addrTries {
		t.Fatalf("%d dials, want %d", calls, addrTries)
	}
}
