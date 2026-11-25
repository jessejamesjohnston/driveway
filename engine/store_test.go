// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"strings"
	"testing"

	"github.com/rclone/rclone/fs/config"
)

// rclone's view of the servers held in memory: sections listed in order,
// keys removed one at a time, nothing written anywhere.
func TestMemStore(t *testing.T) {
	s := newMemStore()
	names := []string{"pear", "apple", "fig", "kiwi", "date", "lime", "plum", "cherry"}
	for _, n := range names {
		s.SetValue(n, "type", "sftp")
	}
	if got := strings.Join(s.GetSectionList(), " "); got != "apple cherry date fig kiwi lime pear plum" {
		t.Errorf("sections %q", got)
	}
	s.SetValue("fig", "host", "h")
	if !s.DeleteKey("fig", "host") {
		t.Error("deleting a key that was there said it wasn't")
	}
	if _, ok := s.GetValue("fig", "host"); ok {
		t.Error("the key is still there")
	}
	if s.DeleteKey("fig", "host") || s.DeleteKey("nowhere", "host") {
		t.Error("deleting a key that wasn't there said it was")
	}
	if out, err := s.Serialize(); out != "" || err != nil {
		t.Errorf("serialized %q %v", out, err)
	}
}

// The engine's store is the one rclone's config calls reach.
func TestMemStoreIsRclones(t *testing.T) {
	defer RemoveRemote("c-store")
	if err := SetRemote("c-store", "sftp", `{"host":"127.0.0.1","port":"2222"}`); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range config.FileSections() {
		found = found || n == "c-store"
	}
	if !found {
		t.Fatalf("rclone doesn't see c-store in %q", config.FileSections())
	}
	if !config.FileDeleteKey("c-store", "port") {
		t.Fatal("rclone couldn't remove a key")
	}
	if _, ok := store.GetValue("c-store", "port"); ok {
		t.Error("the key is still in the engine's store")
	}
}
