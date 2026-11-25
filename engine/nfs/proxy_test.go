// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rclone/rclone/fs/config/configmap"
	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// testServer is the userspace NFS test server (test/servers/compose.yml):
// MOUNT and NFS on one port, export "/".
const testServer = "127.0.0.1:2050"

func needServers(t *testing.T) {
	t.Helper()
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
}

// connectTo mounts export "/" at host:port.
func connectTo(t *testing.T, host string, port int) *Fs {
	t.Helper()
	fsys, err := NewFs(context.Background(), "b-nfs", "", configmap.Simple{
		"host": host, "port": strconv.Itoa(port), "export": "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := fsys.(*Fs)
	t.Cleanup(func() { _ = f.Shutdown(context.Background()) })
	return f
}

// rpcCall is one request the backend sent, as the proxy saw it.
type rpcCall struct {
	prog, proc uint32
	args       []byte // the procedure's arguments, after the RPC header
}

func (c rpcCall) mount() bool       { return c.prog == nfsc.MountProg && c.proc == nfsc.MountProc3MNT }
func (c rpcCall) nfs(proc int) bool { return c.prog == nfsc.Nfs3Prog && c.proc == uint32(proc) }

// fault is what the proxy does with one request. The zero fault passes it
// to the server and its reply back unchanged.
type fault struct {
	answer []byte           // reply with these words after the xid; the server never sees the request
	edit   func(res []byte) // change the server's result (the words after the accepted-reply header)
	delay  time.Duration    // pass the server's reply back this late
	drop   bool             // never answer
}

// accepted is a successful RPC reply carrying res, for fault.answer.
func accepted(res ...uint32) []byte {
	return words(append([]uint32{1, 0, 0, 0, 0}, res...)...)
}

func words(w ...uint32) []byte {
	b := make([]byte, 4*len(w))
	for i, v := range w {
		binary.BigEndian.PutUint32(b[4*i:], v)
	}
	return b
}

// setStatus is an edit that turns the server's answer into the error status.
func setStatus(status uint32) func([]byte) {
	return func(res []byte) { binary.BigEndian.PutUint32(res, status) }
}

// rpcProxy stands between the backend and the test NFS server and, by its
// rule, answers in the server's place, changes its replies or holds them
// back: a server that fails in a given way, everything else being real.
type rpcProxy struct {
	l        net.Listener
	upstream string

	mu   sync.Mutex
	rule func(rpcCall) fault

	mounts    atomic.Int32 // MNT requests seen
	mountEnds atomic.Int32 // connections that carried a MNT request and have since closed
}

// newProxy listens on addr ("127.0.0.1:0" for any port) and passes requests
// to the test server. It stops when the test ends.
func newProxy(t *testing.T, addr string) *rpcProxy {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("can't listen on %s: %v", addr, err)
	}
	p := &rpcProxy{l: l, upstream: testServer}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *rpcProxy) port() int { return p.l.Addr().(*net.TCPAddr).Port }

func (p *rpcProxy) setRule(rule func(rpcCall) fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rule = rule
}

func (p *rpcProxy) faultFor(c rpcCall) fault {
	if c.mount() {
		p.mounts.Add(1)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rule == nil {
		return fault{}
	}
	return p.rule(c)
}

// waitMountEnds waits until n connections that mounted have closed.
func (p *rpcProxy) waitMountEnds(t *testing.T, n int32, within time.Duration) {
	t.Helper()
	until := time.Now().Add(within)
	for p.mountEnds.Load() < n {
		if time.Now().After(until) {
			t.Fatalf("%d mounted connections closed after %v, want %d", p.mountEnds.Load(), within, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *rpcProxy) serve(c net.Conn) {
	var mounted atomic.Bool
	defer func() {
		c.Close()
		if mounted.Load() {
			p.mountEnds.Add(1)
		}
	}()
	up, err := net.Dial("tcp", p.upstream)
	if err != nil {
		return
	}
	defer up.Close()
	var wmu sync.Mutex // one reply at a time on c
	reply := func(msg []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = writeRecord(c, msg)
	}
	var pmu sync.Mutex
	pending := map[uint32]fault{}
	go func() {
		for {
			msg, err := readRecord(up)
			if err != nil || len(msg) < 4 {
				c.Close()
				return
			}
			xid := binary.BigEndian.Uint32(msg)
			pmu.Lock()
			f := pending[xid]
			delete(pending, xid)
			pmu.Unlock()
			if f.edit != nil {
				if off := resultOffset(msg); off > 0 {
					f.edit(msg[off:])
				}
			}
			if f.delay > 0 {
				go func() { time.Sleep(f.delay); reply(msg) }()
				continue
			}
			reply(msg)
		}
	}()
	for {
		msg, err := readRecord(c)
		if err != nil {
			return
		}
		call, ok := parseCall(msg)
		if !ok {
			return
		}
		if call.mount() {
			mounted.Store(true)
		}
		f := p.faultFor(call)
		switch {
		case f.drop:
		case f.answer != nil:
			reply(append(append([]byte{}, msg[:4]...), f.answer...))
		default:
			pmu.Lock()
			pending[binary.BigEndian.Uint32(msg)] = f
			pmu.Unlock()
			if err := writeRecord(up, msg); err != nil {
				return
			}
		}
	}
}

// parseCall reads an RPC call's header: xid, type, RPC version, program,
// version, procedure, then the credential and verifier.
func parseCall(msg []byte) (rpcCall, bool) {
	if len(msg) < 32 {
		return rpcCall{}, false
	}
	c := rpcCall{prog: binary.BigEndian.Uint32(msg[12:]), proc: binary.BigEndian.Uint32(msg[20:])}
	off := 24
	for i := 0; i < 2; i++ { // credential, verifier: flavor, length, body
		if len(msg) < off+8 {
			return rpcCall{}, false
		}
		n := int(binary.BigEndian.Uint32(msg[off+4:]))
		off += 8 + (n+3)&^3
	}
	if off > len(msg) {
		return rpcCall{}, false
	}
	c.args = msg[off:]
	return c, true
}

// resultOffset is where an accepted, successful reply's result starts:
// after xid, type, reply status and the verifier, and the accept status.
func resultOffset(msg []byte) int {
	if len(msg) < 20 || binary.BigEndian.Uint32(msg[8:]) != 0 {
		return 0
	}
	n := int(binary.BigEndian.Uint32(msg[16:]))
	off := 20 + (n+3)&^3 + 4
	if off > len(msg) {
		return 0
	}
	return off
}

// readRecord reads one RPC message, joining its record-marking fragments.
func readRecord(r io.Reader) ([]byte, error) {
	var msg []byte
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		h := binary.BigEndian.Uint32(hdr[:])
		frag := make([]byte, h&0x7fffffff)
		if _, err := io.ReadFull(r, frag); err != nil {
			return nil, err
		}
		msg = append(msg, frag...)
		if h&0x80000000 != 0 {
			return msg, nil
		}
	}
}

func writeRecord(w io.Writer, msg []byte) error {
	b := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(b, uint32(len(msg))|0x80000000)
	copy(b[4:], msg)
	_, err := w.Write(b)
	return err
}
