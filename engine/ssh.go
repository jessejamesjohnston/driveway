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
)

// NewKey makes an Ed25519 key for SFTP: {"private": PEM, "public":
// authorized_keys line}. The app keeps the private half; the person adds the
// public half to the server.
func NewKey(comment string) (string, error) {
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
// any login: {"line": known_hosts line, "fingerprint": SHA256:...}. The app
// shows the fingerprint the first time and pins the line afterwards.
func HostKey(host string, port int) (string, error) {
	if port == 0 {
		port = 22
	}
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
	hostField := host
	if port != 22 {
		hostField = "[" + host + "]:" + strconv.Itoa(port)
	}
	line := hostField + " " + string(ssh.MarshalAuthorizedKey(key))
	j, _ := json.Marshal(map[string]string{"line": line[:len(line)-1], "fingerprint": ssh.FingerprintSHA256(key)})
	return string(j), nil
}

var errHaveKey = errors.New("have host key")

// hostKeyLimit bounds the whole identity check, connecting included.
var hostKeyLimit = 15 * time.Second
