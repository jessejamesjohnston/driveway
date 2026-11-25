// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// An NFS server that takes the connection and never answers (it hung, or
// went away mid-session) must fail a call in seconds, not after the NFS
// client's eight minutes of resends.
func TestNFSNoAnswer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { // read and drop everything
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	if err := SetRemote("t-hang", "nfs", `{"host":"127.0.0.1","port":"`+strconv.Itoa(port)+`","export":"/"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-hang")
	start := time.Now()
	err = Check("t-hang")
	took := time.Since(start)
	if err == nil || !strings.HasPrefix(err.Error(), "[network]") {
		t.Fatalf("check: %v", err)
	}
	if took > 25*time.Second {
		t.Fatalf("took %v", took)
	}
	t.Logf("failed in %v: %v", took.Round(time.Second), err)
}
