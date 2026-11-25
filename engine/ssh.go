// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// NewKey makes an Ed25519 key for SFTP: {"private": PEM, "public":
// authorized_keys line}. The app keeps the private half; the person adds the
// public half to the server.
func NewKey(comment string) (_ string, err error) {
	defer scrub(&err)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", tag("other", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", tag("other", err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", tag("other", err)
	}
	line := string(ssh.MarshalAuthorizedKey(sp))
	line = line[:len(line)-1] + " " + comment
	j, _ := json.Marshal(map[string]string{"private": string(pem.EncodeToMemory(block)), "public": line})
	return string(j), nil
}

// HostKey connects to an SSH server and returns the key it presents, before
// any login: {"line": known_hosts line, "fingerprint": SHA256:..., "type":
// the key's type, such as ssh-ed25519 or ssh-rsa}. The app
// shows the fingerprint the first time and pins the line afterwards. An
// IPv6 host may come with or without brackets.
func HostKey(host string, port int) (_ string, err error) {
	defer scrub(&err)
	if port == 0 {
		port = 22
	}
	host = bareHost(host)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var key ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "driveway-hostkey-check",
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k
			return errHaveKey
		},
		Timeout: 15 * time.Second,
	}
	// The handshake is bounded too: a server that takes the connection and
	// never speaks SSH would otherwise hold the check open.
	var late atomic.Bool
	c, timer, err := dialWithin(addr, cfg, hostKeyLimit, &late)
	if c != nil {
		timer.Stop()
		c.Close()
	}
	if key == nil {
		if late.Load() {
			return "", tag("network", errors.New("the server took the connection but didn't answer"))
		}
		return "", classify(err)
	}
	j, _ := json.Marshal(map[string]string{"line": knownHostsLine(host, port, key), "fingerprint": ssh.FingerprintSHA256(key),
		"type": key.Type()})
	return string(j), nil
}

// knownHostsLine is the known_hosts line that pins key for host (bare or in
// brackets) at port, as rclone and the server copy look it up.
func knownHostsLine(host string, port int, key ssh.PublicKey) string {
	return knownhosts.Line([]string{net.JoinHostPort(bareHost(host), strconv.Itoa(port))}, key)
}

var errHaveKey = errors.New("have host key")

// hostKeyLimit bounds the whole identity check, connecting included.
var hostKeyLimit = 15 * time.Second
