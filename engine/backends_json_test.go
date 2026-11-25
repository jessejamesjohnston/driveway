// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"os"
	"testing"
)

// TestBackendsJSON writes Backends() to $DRIVEWAY_BACKENDS_JSON for the
// app's JVM checks (./build.sh test), so they read the engine's real
// catalog instead of a copy that could drift from it.
func TestBackendsJSON(t *testing.T) {
	out := os.Getenv("DRIVEWAY_BACKENDS_JSON")
	if out == "" {
		t.Skip("set DRIVEWAY_BACKENDS_JSON to write the catalog")
	}
	j := Backends()
	var list []map[string]any
	if err := json.Unmarshal([]byte(j), &list); err != nil || len(list) == 0 {
		t.Fatalf("Backends() isn't a JSON list of services: %v", err)
	}
	if err := os.WriteFile(out, []byte(j), 0o644); err != nil {
		t.Fatal(err)
	}
}
