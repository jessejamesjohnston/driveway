// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"testing"

	"github.com/cloudsoda/sddl"
)

// The stand-in for cloudsoda/sddl keeps its own copy of a descriptor's bytes
// and hands back exactly those.
func TestSDDLStandIn(t *testing.T) {
	b := []byte{1, 0, 4, 0x80, 0x14, 0, 0, 0}
	sd, err := sddl.FromBinary(b)
	if err != nil {
		t.Fatal(err)
	}
	b[0] = 9
	if got := sd.Binary(); len(got) != 8 || got[0] != 1 || got[3] != 0x80 {
		t.Fatalf("descriptor %v", got)
	}
}
