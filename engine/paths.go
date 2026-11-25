// SPDX-License-Identifier: Apache-2.0

package engine

// Path and name helpers.

import (
	"crypto/rand"
	"encoding/hex"
	"path"
	"strings"
	"unicode/utf8"
)

func clean(p string) string {
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

func parent(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// capName keeps a name short enough that the temporary names built on it
// still fit the 255-byte limit of most file systems, without splitting a
// character.
func capName(name string) string {
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

func token() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
