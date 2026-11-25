// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package engine

import (
	"errors"
	"io"
	"testing"
)

const filterable = false

func dupFD(int) (int, error) { return 0, errors.New("not on this system") }

// captureFD captures nothing here: stderr is only filtered on Android, and
// these checks run it on Linux.
func captureFD(*testing.T, int, io.Writer) func() { return func() {} }
