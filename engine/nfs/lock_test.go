// SPDX-License-Identifier: Apache-2.0

package nfs

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// A call queued behind a slow one (a big folder's deletion holds the
// connection for up to two minutes) gives up at its own deadline.
func TestLockDeadline(t *testing.T) {
	f := &Fs{mu: make(chan struct{}, 1)}
	f.mu <- struct{}{} // the slow call
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	ran := false
	err := f.do(ctx, func(*nfsc.Target) error { ran = true; return nil })
	took := time.Since(start)
	if ran || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ran %v, err %v", ran, err)
	}
	if took < 900*time.Millisecond || took > 1500*time.Millisecond {
		t.Fatalf("gave up after %v, want about 1 s", took)
	}
	// Once the slow call is done, the connection is free again.
	<-f.mu
	limit, err := f.lock(context.Background(), callLimit)
	if err != nil || limit < callLimit-time.Second {
		t.Fatalf("free lock: %v, %v", limit, err)
	}
	f.unlock()
}

// A call whose time is already up never starts a request, even with the
// connection free (both select cases are ready then).
func TestLockExpired(t *testing.T) {
	// A connection is there (never dialed: fn would get it straight away).
	f := &Fs{mu: make(chan struct{}, 1), target: &nfsc.Target{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Bool
	for i := 0; i < 200; i++ {
		if err := f.do(ctx, func(*nfsc.Target) error { ran.Store(true); return nil }); err == nil {
			t.Fatalf("try %d: no error", i)
		}
		time.Sleep(time.Millisecond)
		if ran.Load() {
			t.Fatalf("try %d: a request started after the deadline", i)
		}
		if len(f.mu) != 0 {
			t.Fatal("lock left taken")
		}
	}
}
