// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"testing"
)

// A server found or typed in by its IPv6 address (discovery offers those)
// mounts like one with an IPv4 address.
func TestIPv6Host(t *testing.T) {
	needServers(t)
	p := newProxy(t, "[::1]:0")
	f := connectTo(t, "::1", p.port())
	if _, err := f.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if p.mounts.Load() != 1 {
		t.Fatalf("%d mounts through the IPv6 address, want 1", p.mounts.Load())
	}
}
