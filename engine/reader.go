// SPDX-License-Identifier: Apache-2.0

package engine

// Reading a file at any offset, for Files' reads and seeks.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/rclone/rclone/fs"
)

// Reader reads one file at any offset. Sequential reads reuse one stream;
// a jump elsewhere reopens it at the new offset.
type Reader struct {
	id   string
	mu   sync.Mutex
	obj  fs.Object
	rc   io.ReadCloser
	buf  *bufio.Reader // read-ahead over rc
	pos  int64
	size int64
}

// readAhead is how much each trip to the server asks for. Android hands a
// provider reads 128 KB at a time; on SMB and NFS each read is a round trip,
// so asking for more at once is what makes a long sequential read fast.
const readAhead = 4 << 20

// Open starts reading the file p.
func Open(id, p string) (*Reader, error) {
	f, c, done, err := begin(id)
	if err != nil {
		return nil, err
	}
	defer done()
	w, cancel := op(c)
	defer cancel()
	o, err := f.NewObject(w, clean(p))
	if err != nil {
		return nil, classify(err)
	}
	if o.Size() < 0 {
		// A file the service makes on demand (a Google Docs document) has no
		// size until it's made, and Android needs one: reads past "size -1"
		// would look like the end of an empty file.
		return nil, tag("other", errors.New("this file has no fixed size (the service makes it when it's downloaded); open it in the service's own app"))
	}
	return &Reader{id: id, obj: o, size: o.Size()}, nil
}

// Size of the file in bytes.
func (r *Reader) Size() int64 { return r.size }

// skipLimit is how far ahead a read may be before reopening beats reading
// and dropping the bytes in between.
const skipLimit = 512 << 10

// ReadAt returns up to n bytes from offset off; fewer only at the end of the file.
func (r *Reader) ReadAt(off int64, n int) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if off >= r.size || n <= 0 {
		return []byte{}, nil
	}
	if r.rc != nil && off > r.pos && off-r.pos <= skipLimit {
		if _, err := io.CopyN(io.Discard, r.buf, off-r.pos); err == nil {
			r.pos = off
		} else {
			r.closeStream()
		}
	}
	buf := make([]byte, n)
	got := 0
	// A dropped connection (the server restarted, Wi-Fi blipped) gets one
	// fresh connection, resuming where the read stopped.
	for attempt := 0; ; attempt++ {
		if r.rc == nil || off+int64(got) != r.pos {
			r.closeStream()
			rc, err := r.obj.Open(ctx(), &fs.SeekOption{Offset: off + int64(got)})
			if err != nil {
				if attempt == 0 && isNetwork(err) {
					if cerr := r.reconnect(); cerr != nil {
						return nil, cerr
					}
					continue
				}
				return nil, classify(err)
			}
			r.rc, r.pos = rc, off+int64(got)
			r.buf = bufio.NewReaderSize(rc, readAhead)
		}
		m, err := io.ReadFull(r.buf, buf[got:])
		got += m
		r.pos += int64(m)
		end := off+int64(got) >= r.size
		if err == nil || (end && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))) {
			return buf[:got], nil
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// The stream stopped before the file's end: a cut connection, not the end.
			err = tag("network", fmt.Errorf("the stream ended at %d of %d bytes", off+int64(got), r.size))
		}
		r.closeStream()
		if attempt > 0 || !isNetwork(err) {
			return nil, classify(err)
		}
		if cerr := r.reconnect(); cerr != nil {
			return nil, cerr
		}
	}
}

// reconnect drops the server's connections and finds the file again, so the
// next open uses a new connection. If the file was replaced meanwhile, the
// read fails rather than carry on in the other version.
func (r *Reader) reconnect() error {
	reset(r.id)
	// A file that can't be found again can't be checked: fail the read rather
	// than resume in what may be another version.
	f, err := remote(ctx(), r.id)
	if err != nil {
		return err
	}
	o, err := f.NewObject(ctx(), r.obj.Remote())
	if err != nil {
		return classify(err)
	}
	if o.Size() != r.size || !o.ModTime(ctx()).Equal(r.obj.ModTime(ctx())) {
		return tag("other", errors.New("the file changed on the server while it was being read"))
	}
	r.obj = o
	return nil
}

func isNetwork(err error) bool {
	return strings.HasPrefix(classify(err).Error(), "[network]")
}

func (r *Reader) closeStream() {
	if r.rc != nil {
		_ = r.rc.Close()
		r.rc = nil
		r.buf = nil
	}
}

// Close ends the read.
func (r *Reader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeStream()
}
