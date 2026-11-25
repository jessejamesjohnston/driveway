// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"net"
	"strings"
)

// The app saves a server's host the way a person reads it: an IPv6 address
// bare ("fd00::5", as servers found on the network are saved), though one
// typed long ago may be in brackets. Each backend gets the form it needs from
// here, so every saved server works whichever form it holds:
//
//   - rclone's SFTP, SMB and FTP join host and port with a bare colon, so an
//     IPv6 address reaches them in brackets ("[fd00::5]").
//   - Driveway's own NFS client and dialers (the identity check, the server
//     copy) join with net.JoinHostPort, which adds the brackets: bare.
//
// Anything that isn't an IPv6 address (a name, an IPv4 address) is left as
// it is.

// bracketsHost lists the backends that need an IPv6 host in brackets.
var bracketsHost = map[string]bool{"sftp": true, "smb": true, "ftp": true}

// bareHost is host without the brackets around an IPv6 address (with or
// without a zone, "fe80::1%wlan0"). Anything else comes back unchanged.
func bareHost(host string) string {
	if len(host) > 2 && host[0] == '[' && host[len(host)-1] == ']' && isIPv6(host[1:len(host)-1]) {
		return host[1 : len(host)-1]
	}
	return host
}

// isIPv6 reports whether s is an IPv6 address, with or without a zone.
func isIPv6(s string) bool {
	addr, _, _ := strings.Cut(s, "%")
	ip := net.ParseIP(addr)
	return ip != nil && strings.Contains(addr, ":")
}

// hostFor is host in the form backend needs.
func hostFor(backend, host string) string {
	bare := bareHost(host)
	if bracketsHost[backend] && isIPv6(bare) {
		return "[" + bare + "]"
	}
	if backend == "nfs" {
		return bare
	}
	return host
}
