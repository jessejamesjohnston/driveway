// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A server that promises a whole file and sends half: the read must fail
// (after one reconnect) rather than hand back the half as if it were all.
func TestTruncatedStream(t *testing.T) {
	const size = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="f.bin">f.bin</a>`)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		if r.Method == http.MethodHead {
			return
		}
		// Promise the whole file, send half, hang up: an unexpected EOF.
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		// Honor "Range: bytes=N-" as real servers do, and cut every answer in half.
		start := 0
		if rg := r.Header.Get("Range"); rg != "" {
			fmt.Sscanf(rg, "bytes=%d-", &start)
		}
		left := size - start
		status := "200 OK"
		if start > 0 {
			status = fmt.Sprintf("206 Partial Content\r\nContent-Range: bytes %d-%d/%d", start, size-1, size)
		}
		fmt.Fprintf(buf, "HTTP/1.1 %s\r\nContent-Length: %d\r\nContent-Type: application/octet-stream\r\n\r\n", status, left)
		_, _ = buf.Write(make([]byte, left/2))
		_ = buf.Flush()
		conn.Close()
	}))
	defer srv.Close()
	if err := SetRemote("t-http", "http", `{"url":"`+srv.URL+`/"}`); err != nil {
		t.Fatal(err)
	}
	r, err := Open("t-http", "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := r.ReadAt(0, size)
	if err == nil {
		t.Fatalf("a cut stream returned %d bytes as success", len(got))
	}
	if !strings.HasPrefix(err.Error(), "[network]") {
		t.Fatalf("want a network error, got %v", err)
	}
	t.Logf("cut stream: %v", err)
	RemoveRemote("t-http")
}

// A file replaced on the server while a read is cut off: the read must fail,
// not resume in the new version (which would hand back half of each).
func TestReplacedMidRead(t *testing.T) {
	const size = 1 << 20
	var replaced atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="f.bin">f.bin</a>`)
			return
		}
		n := size
		if replaced.Load() {
			n = size + 10 // the new version
		}
		w.Header().Set("Content-Length", fmt.Sprint(n))
		if r.Method == http.MethodHead {
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", n)
		_, _ = buf.Write(make([]byte, n/2))
		_ = buf.Flush()
		conn.Close()
		replaced.Store(true)
	}))
	defer srv.Close()
	if err := SetRemote("t-http2", "http", `{"url":"`+srv.URL+`/"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-http2")
	r, err := Open("t-http2", "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.ReadAt(0, size)
	if err == nil || !strings.Contains(err.Error(), "changed on the server") {
		t.Fatalf("want a changed-file error, got %v", err)
	}
}

// A file with no size (the service makes it on download, like a Google Docs
// document) can't be opened as an empty one.
func TestUnknownSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="doc.docx">doc.docx</a>`)
			return
		}
		w.Header().Set("Transfer-Encoding", "chunked") // no Content-Length
		if r.Method != http.MethodHead {
			fmt.Fprint(w, "contents")
		}
	}))
	defer srv.Close()
	if err := SetRemote("t-http3", "http", `{"url":"`+srv.URL+`/"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-http3")
	r, err := Open("t-http3", "doc.docx")
	if err == nil {
		r.Close()
		t.Fatalf("opened a file of size %d", r.Size())
	}
	if !strings.Contains(err.Error(), "no fixed size") {
		t.Fatalf("got %v", err)
	}
}

// A cut read whose file can't be looked up again (the check fails) must not
// resume: the next open could read a version that replaced it meanwhile.
func TestReconnectUnchecked(t *testing.T) {
	const size = 1 << 20
	var cut atomic.Bool
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<a href="f.bin">f.bin</a>`)
			return
		}
		if r.Method == http.MethodHead {
			if cut.Load() {
				http.Error(w, "busy", http.StatusInternalServerError) // the check fails
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(size))
			return
		}
		gets.Add(1)
		if !cut.Load() {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", size)
			_, _ = buf.Write(make([]byte, size/2))
			_ = buf.Flush()
			conn.Close()
			cut.Store(true)
			return
		}
		// The replacement, served whole to any range.
		start := 0
		if rg := r.Header.Get("Range"); rg != "" {
			fmt.Sscanf(rg, "bytes=%d-", &start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, size-1, size))
			w.Header().Set("Content-Length", fmt.Sprint(size-start))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write([]byte(strings.Repeat("x", size-start)))
	}))
	defer srv.Close()
	if err := SetRemote("t-http3", "http", `{"url":"`+srv.URL+`/"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-http3")
	r, err := Open("t-http3", "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := r.ReadAt(0, size)
	if err == nil {
		t.Fatalf("read resumed without checking the file (%d bytes, %d downloads)", len(got), gets.Load())
	}
	if gets.Load() != 1 {
		t.Fatalf("the replacement was downloaded (%d downloads): %v", gets.Load(), err)
	}
	t.Logf("unchecked reconnect: %v", err)
}
