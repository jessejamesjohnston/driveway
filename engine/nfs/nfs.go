// SPDX-License-Identifier: Apache-2.0

// Package nfs is an rclone backend for NFSv3 servers, built on
// github.com/willscott/go-nfs-client. rclone has an NFS server but no NFS
// client; this fills that gap for Driveway.
//
// Android apps can't connect from privileged ports (below 1024), and Linux and
// Synology NFS servers refuse other ports unless the export allows them
// ("insecure" in /etc/exports, "Allow connections from non-privileged ports"
// on Synology). Mount errors say so.
package nfs

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	nfsc "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "nfs",
		Description: "NFS (version 3)",
		NewFs:       NewFs,
		Options: []fs.Option{{
			Name:     "host",
			Help:     "NFS server to connect to.\n\nE.g. \"nas.local\" or \"192.168.1.10\".",
			Required: true,
		}, {
			Name:     "export",
			Help:     "Exported directory to mount.\n\nE.g. \"/volume1/share\".",
			Required: true,
		}, {
			Name:     "port",
			Help:     "Port for both NFS and MOUNT.\n\nLeave 0 to ask the server's portmapper (port 111), which is right for most servers.",
			Default:  0,
			Advanced: true,
		}, {
			Name:     "uid",
			Help:     "User ID to act as (NFS AUTH_SYS).",
			Default:  1000,
			Advanced: true,
		}, {
			Name:     "gid",
			Help:     "Group ID to act as (NFS AUTH_SYS).",
			Default:  1000,
			Advanced: true,
		}},
	})
}

// Options are the backend's configuration.
type Options struct {
	Host   string `config:"host"`
	Export string `config:"export"`
	Port   int    `config:"port"`
	UID    uint32 `config:"uid"`
	GID    uint32 `config:"gid"`
}

// Fs is a directory tree on an NFS export.
type Fs struct {
	name     string
	root     string
	opt      Options
	features *fs.Features
	auth     rpc.Auth // the credentials every request carries (AUTH_SYS)

	// mu lets one RPC conversation run at a time, and guards the fields
	// below. A one-slot channel rather than a mutex: a caller waiting behind
	// a slow call (a big folder's deletion) gives up at its own deadline
	// instead of waiting the slow call out.
	mu     chan struct{}
	mount  *nfsc.Mount
	target *nfsc.Target

	// connMu guards mount and target against bounded, which sets a stuck
	// connection aside while the goroutine using it still runs.
	connMu sync.Mutex
	epoch  int // bumped each time a stuck call is given up on
}

// Object is a file on the export.
type Object struct {
	fs      *Fs
	remote  string
	size    int64
	modTime time.Time
}

// NewFs connects to the server and mounts the export.
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	opt := new(Options)
	if err := configstruct.Set(m, opt); err != nil {
		return nil, err
	}
	// The host bare: an IPv6 address gets its brackets where it is dialed.
	if h := opt.Host; len(h) > 2 && h[0] == '[' && h[len(h)-1] == ']' {
		opt.Host = h[1 : len(h)-1]
	}
	f := &Fs{name: name, root: strings.Trim(root, "/"), opt: *opt, mu: make(chan struct{}, 1),
		auth: rpc.NewAuthUnix("driveway", opt.UID, opt.GID).Auth()}
	f.features = (&fs.Features{CanHaveEmptyDirectories: true}).Fill(ctx, f)
	limit, err := f.lock(ctx, callLimit)
	if err != nil {
		return nil, err
	}
	epoch := f.currentEpoch()
	err = f.boundedWithin(limit, func() error {
		_, err := f.connect(epoch)
		return err
	})
	f.unlock()
	if err != nil {
		return nil, err
	}
	// rclone convention: a root that names a file means its directory.
	if f.root != "" {
		if fi, err := f.getattr(ctx, f.root); err == nil && !fi.IsDir() {
			f.root = parent(f.root)
			return f, fs.ErrorIsFile
		}
	}
	return f, nil
}
