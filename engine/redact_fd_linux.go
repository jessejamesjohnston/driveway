// SPDX-License-Identifier: Apache-2.0

//go:build linux

package engine

import (
	"bufio"
	"os"
	"sync"
	"syscall"
)

// filtered keeps the write ends of the pipes filterFD puts in place, so they
// are never collected and closed.
var (
	filteredMu sync.Mutex
	filtered   []*os.File
)

// filterFD puts redaction between fd and wherever it led: fd becomes the
// write end of a pipe, and each line read from the pipe is redacted and
// written on to fd's old destination (on Android, gomobile's pipe to the
// GoLog tag). Android builds count as linux.
func filterFD(fd int) error {
	old, err := syscall.Dup(fd)
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		syscall.Close(old)
		return err
	}
	if err := syscall.Dup3(int(w.Fd()), fd, 0); err != nil {
		syscall.Close(old)
		r.Close()
		w.Close()
		return err
	}
	filteredMu.Lock()
	filtered = append(filtered, w)
	filteredMu.Unlock()
	out := os.NewFile(uintptr(old), "log")
	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				_, _ = out.WriteString(redact(line))
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}
