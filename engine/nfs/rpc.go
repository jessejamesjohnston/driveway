// SPDX-License-Identifier: Apache-2.0

package nfs

// NFS calls sent directly (REMOVE, RMDIR, RENAME), and closing the connection.

import (
	"fmt"
	"path"
	"strings"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// go-nfs-client's Remove, RmDir and Rename look up the folders involved and
// send the change straight after, past any stillOn check of ours: a lookup
// answered late would still lead to the change. So the backend looks the
// folders up itself and sends these changes with the client's Call. (They
// skip the client's cache of folder listings, which is safe: both ways of
// connecting give it no lifetime, so every listing is read afresh.)

// diropArgs are the arguments of REMOVE and RMDIR (RFC 1813, 3.3.12 and
// 3.3.13): a folder's handle and a name in it.
type diropArgs struct {
	rpc.Header
	What nfsc.Diropargs3
}

// renameArgs are RENAME's (RFC 1813, 3.3.14).
type renameArgs struct {
	rpc.Header
	From, To nfsc.Diropargs3
}

func (f *Fs) header(proc uint32) rpc.Header {
	return rpc.Header{Rpcvers: 2, Prog: nfsc.Nfs3Prog, Vers: nfsc.Nfs3Vers, Proc: proc, Cred: f.auth, Verf: rpc.AuthNull}
}

// send sends one request that changes something, unless the call it belongs
// to was given up on, and returns the server's status as an error.
func (f *Fs) send(t *nfsc.Target, args any) error {
	if err := f.stillOn(t); err != nil {
		return err
	}
	res, err := t.Call(args)
	if err != nil {
		return err
	}
	status, err := xdr.ReadUint32(res)
	if err != nil {
		return err
	}
	return nfsc.NFS3Error(status)
}

// dirOp removes the file or folder name from the folder with handle dir
// (proc is REMOVE or RMDIR).
func (f *Fs) dirOp(t *nfsc.Target, proc uint32, dir []byte, name string) error {
	return f.send(t, &diropArgs{Header: f.header(proc), What: nfsc.Diropargs3{FH: dir, Filename: name}})
}

// removeEntry removes the file or folder at p.
func (f *Fs) removeEntry(t *nfsc.Target, proc uint32, p string) error {
	_, dir, err := t.Lookup(parent(p))
	if err != nil {
		return err
	}
	return f.dirOp(t, proc, dir, path.Base(p))
}

// rename renames from to to.
func (f *Fs) rename(t *nfsc.Target, from, to string) error {
	_, src, err := t.Lookup(parent(from))
	if err != nil {
		return err
	}
	_, dst, err := t.Lookup(parent(to))
	if err != nil {
		return err
	}
	return f.send(t, &renameArgs{Header: f.header(nfsc.NFSProc3Rename),
		From: nfsc.Diropargs3{FH: src, Filename: path.Base(from)},
		To:   nfsc.Diropargs3{FH: dst, Filename: path.Base(to)}})
}

func (f *Fs) disconnect() {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	f.closeLocked()
}

func (f *Fs) closeLocked() {
	if f.target != nil {
		f.target.Close()
	}
	if f.mount != nil {
		f.mount.Close()
	}
	f.mount, f.target = nil, nil
}

func mountError(export string, err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "MNT3ERR_NOENT"):
		return fmt.Errorf("the server has no export %q", export)
	case strings.Contains(s, "MNT3ERR_PERM"), strings.Contains(s, "MNT3ERR_ACCES"), strings.Contains(s, "auth"):
		return fmt.Errorf("the server refused to mount %q. Check that the export allows this device's address and "+
			"connections from non-privileged ports (\"insecure\" in /etc/exports; \"Allow connections from "+
			"non-privileged ports\" on Synology): %w", export, err)
	case strings.HasSuffix(s, "rejectedStatus was not valid: 1"):
		// go-nfs-client's words for an RPC authentication error (MSG_DENIED,
		// AUTH_ERROR), at the mount or the first NFS request.
		return fmt.Errorf("the server refused to mount %q: it turned down this device's credentials. Check that the "+
			"export allows this device's address and connections from non-privileged ports (\"insecure\" in "+
			"/etc/exports; \"Allow connections from non-privileged ports\" on Synology), and that it accepts "+
			"AUTH_SYS security (\"sec=sys\"), not only Kerberos: %w", export, err)
	}
	return fmt.Errorf("can't mount %q: %w", export, err)
}
