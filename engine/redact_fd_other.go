// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package engine

import "errors"

// filterFD is only needed on Android (redactLogs); on other systems the
// engine runs only in tests.
func filterFD(int) error { return errors.New("not on this system") }
