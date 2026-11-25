// SPDX-License-Identifier: Apache-2.0

// Package sddl stands in for github.com/cloudsoda/sddl (LGPL-3.0), which
// go-smb2 imports to parse Windows security descriptors. Driveway never reads
// or changes file permissions over SMB, so a descriptor is kept as the opaque
// bytes the server sent: enough for go-smb2 to compile, and to hand a
// descriptor back unchanged. Written from go-smb2's calls alone; none of the
// original's code is used.
package sddl

// SecurityDescriptor is a Windows security descriptor in its binary form.
type SecurityDescriptor struct {
	raw []byte
}

// FromBinary keeps a copy of a binary security descriptor.
func FromBinary(b []byte) (*SecurityDescriptor, error) {
	return &SecurityDescriptor{raw: append([]byte(nil), b...)}, nil
}

// Binary returns the descriptor's bytes.
func (sd *SecurityDescriptor) Binary() []byte { return sd.raw }
