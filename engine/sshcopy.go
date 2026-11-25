// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"github.com/rclone/rclone/fs/config/obscure"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sftpCopy copies a file on an SFTP server by running cp there over the same
// login and pinned host key. (rclone's own SFTP "copy" makes a hard link, two
// names for one file, which a file manager's Copy must not do.) Servers that
// only allow SFTP, with no shell, return [unsupported]. It ends by c's end,
// the call it is part of.
func sftpCopy(c context.Context, id, from, to string) error {
	// shellQuote is POSIX quoting. fish, a login shell some accounts use,
	// treats a backslash inside single quotes as an escape, so a name with
	// one could end the quote early and run the rest as a command. Such
	// names take the ordinary copy instead.
	if strings.Contains(from, `\`) || strings.Contains(to, `\`) {
		return tag("unsupported", errors.New("a name with a backslash is copied the ordinary way"))
	}
	get := func(k string) string { v, _ := store.GetValue(id, k); return v }
	host, user := bareHost(get("host")), get("user")
	port := get("port")
	if port == "" {
		port = "22"
	}
	var auth []ssh.AuthMethod
	if pemText := get("key_pem"); pemText != "" {
		signer, err := ssh.ParsePrivateKey([]byte(strings.ReplaceAll(pemText, `\n`, "\n")))
		if err != nil {
			return tag("other", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if pass := get("pass"); pass != "" {
		clear, err := obscure.Reveal(pass)
		if err != nil {
			return tag("other", err)
		}
		auth = append(auth, ssh.Password(clear))
	}
	kh := get("known_hosts_file")
	if kh == "" {
		return tag("unsupported", errors.New("no pinned host key"))
	}
	check, err := knownhosts.New(kh)
	if err != nil {
		return tag("unsupported", err)
	}
	cfg := &ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: check, Timeout: 5 * time.Second}
	addr := net.JoinHostPort(host, port)
	// Files waits about 20 seconds for a copy: a login, shell or cp that
	// takes longer than copyLimitTime, or than the call has left, is stopped
	// (closing the connection ends it), its partial copy removed, and Files
	// copies the ordinary way instead.
	var late atomic.Bool
	conn, timer, err := dialWithin(addr, cfg, within(c, copyLimitTime), &late)
	if err != nil {
		if late.Load() {
			return tag("unsupported", errors.New("logging in for cp took too long"))
		}
		return classify(err)
	}
	defer timer.Stop()
	defer conn.Close()
	// SFTP-only accounts (ForceCommand internal-sftp) accept the request but
	// don't run it: check for a shell that answers first.
	probe, err := conn.NewSession()
	if err != nil {
		return tag("unsupported", err)
	}
	out, _ := probe.Output("echo driveway-shell; pwd")
	probe.Close()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] != "driveway-shell" {
		return tag("unsupported", errors.New("the server has no shell for cp"))
	}
	// The shell must start where SFTP does (on some NAS accounts SFTP is
	// chrooted and the shell isn't): otherwise the same path names a
	// different file.
	sc, err := sftp.NewClient(conn)
	if err != nil {
		return tag("unsupported", err)
	}
	wd, err := sc.Getwd()
	sc.Close()
	if err != nil || strings.TrimRight(wd, "/") != strings.TrimRight(lines[1], "/") {
		return tag("unsupported", fmt.Errorf("the shell starts in %q but SFTP in %q", lines[1], wd))
	}
	sess, err := conn.NewSession()
	if err != nil {
		return tag("unsupported", err)
	}
	defer sess.Close()
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	// Paths are relative to the login's home folder, as they are for SFTP.
	if err := sess.Start(fmt.Sprintf(cpCommand, shellQuote(from), shellQuote(to))); err != nil {
		// sftp-only servers often refuse the exec request outright.
		return tag("unsupported", err)
	}
	if err := sess.Wait(); err != nil {
		var exit *ssh.ExitError
		msg := strings.TrimSpace(stderr.String())
		if errors.As(err, &exit) && (exit.ExitStatus() == 126 || exit.ExitStatus() == 127 ||
			strings.Contains(msg, "not allowed") || strings.Contains(msg, "This service allows sftp connections only")) {
			return tag("unsupported", fmt.Errorf("the server has no shell for cp: %s", msg))
		}
		// Don't leave half a copy behind (the name was free before cp ran):
		// over this connection if cp failed, over a new one if the
		// connection itself dropped. The tidying up is bounded too, a new
		// login included: closing the connection ends a stuck session or rm.
		var rmc *ssh.Client
		var stop *time.Timer
		if errors.As(err, &exit) {
			rmc, stop = conn, time.AfterFunc(tidyLimit, func() { conn.Close() })
		} else {
			quick := *cfg
			quick.Timeout = 3 * time.Second
			if rmc, stop, _ = dialWithin(addr, &quick, tidyLimit, new(atomic.Bool)); rmc != nil {
				defer rmc.Close()
			}
		}
		if rmc != nil {
			if rm, rerr := rmc.NewSession(); rerr == nil {
				_ = rm.Run("rm -f -- " + shellQuote(to))
				rm.Close()
			}
			stop.Stop()
		}
		if late.Load() {
			return tag("unsupported", errors.New("cp on the server took too long"))
		}
		if exit == nil {
			// No exit status: the connection or session ended under cp
			// (ssh's "remote command exited without exit status").
			return tag("network", fmt.Errorf("the connection dropped during cp: %w", err))
		}
		return classify(fmt.Errorf("cp exited %s: %s", strconv.Itoa(exit.ExitStatus()), msg))
	}
	return nil
}

// copyLimitTime bounds a copy on the server, from login to cp's end, inside
// the call's own limit; cpCommand is what runs (tests make it slow).
var (
	copyLimitTime = 12 * time.Second
	cpCommand     = "cp -p -- %s %s"
)

// dialWithin connects and logs in, and closes the connection once limit is
// up from the start, the TCP connection and the login included (ssh.Dial's
// Timeout covers only the TCP connection: a server that takes it and then
// says nothing would hold the call for good). late is set if the limit
// closed it. Stop the timer once done with the connection.
func dialWithin(addr string, cfg *ssh.ClientConfig, limit time.Duration, late *atomic.Bool) (*ssh.Client, *time.Timer, error) {
	end := time.Now().Add(limit)
	conn, err := dial("tcp", addr, min(cfg.Timeout, limit))
	if err != nil {
		return nil, nil, err
	}
	timer := time.AfterFunc(time.Until(end), func() {
		late.Store(true)
		conn.Close()
	})
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		timer.Stop()
		conn.Close()
		return nil, nil, err
	}
	return ssh.NewClient(cc, chans, reqs), timer, nil
}

// dial makes the TCP connection (a test makes it slow).
var dial = net.DialTimeout

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
