// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A folder on a service with no folder rename (S3): Files' move is refused at
// once, and a rename of a folder holding more than renameDirMax files is
// refused before anything moves.
func TestFolderMoveNoRename(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	s := servers[5]
	if s.id != "t-s3" {
		t.Fatal("servers[5] isn't S3")
	}
	if err := SetRemote(s.id, s.backend, s.params); err != nil {
		t.Fatal(err)
	}
	dir := join(s.base, "r-"+token())
	defer Delete(s.id, dir)
	src := local(t, []byte("x"))
	for i := 0; i <= renameDirMax; i++ {
		if err := Upload(s.id, fmt.Sprintf("%s/big/%03d.txt", dir, i), src); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	err := Move(s.id, dir+"/big", dir+"/moved")
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") || time.Since(start) > 5*time.Second {
		t.Fatalf("move: %v after %v", err, time.Since(start))
	}
	err = Rename(s.id, dir+"/big", dir+"/renamed")
	if err == nil || !strings.HasPrefix(err.Error(), "[toobig]") {
		t.Fatalf("rename of a big folder: %v", err)
	}
	ls, err := List(s.id, dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ls, "renamed") || strings.Contains(ls, "moved") {
		t.Fatalf("a refused move changed something: %s", ls)
	}
	ls, err = List(s.id, dir+"/big")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(ls, `"name"`); n != renameDirMax+1 {
		t.Fatalf("%d files left in the folder, want %d", n, renameDirMax+1)
	}
}

// A new document on a server that doesn't answer fails inside Files' wait,
// connecting included.
func TestCreateNoAnswer(t *testing.T) {
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
	if err := SetRemote("t-hang2", "nfs", `{"host":"127.0.0.1","port":"`+strconv.Itoa(port)+`","export":"/"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-hang2")
	start := time.Now()
	_, err = CreateNew("t-hang2", "", "new.txt", false, "")
	took := time.Since(start)
	if err == nil || !strings.HasPrefix(err.Error(), "[network]") {
		t.Fatalf("create: %v", err)
	}
	if took > createLimit+2*time.Second {
		t.Fatalf("took %v", took)
	}
	t.Logf("failed in %v: %v", took.Round(time.Second), err)
}
