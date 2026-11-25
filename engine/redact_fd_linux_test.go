// SPDX-License-Identifier: Apache-2.0

//go:build linux

package engine

import (
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
)

const filterable = true

func dupFD(fd int) (int, error) { return syscall.Dup(fd) }

// captureFD sends what is written to fd to w until the returned function is
// called, which puts fd back as it was.
func captureFD(t *testing.T, fd int, w io.Writer) func() {
	old, err := syscall.Dup(fd)
	if err != nil {
		t.Fatal(err)
	}
	r, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Dup3(int(pw.Fd()), fd, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(w, r)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Dup3(old, fd, 0)
			syscall.Close(old)
			pw.Close()
			wg.Wait()
			r.Close()
		})
	}
}
