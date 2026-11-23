// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package library

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pdfBytes = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAddListPersistDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	d, err := s.Add(bytes.NewReader(pdfBytes), `C:\Users\me\FT-60R Manual.pdf`, "", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if d.Ext != "pdf" || d.Type != "application/pdf" || d.Title != "FT-60R Manual" || d.Size != int64(len(pdfBytes)) {
		t.Fatalf("unexpected doc: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "library", d.ID+".pdf")); err != nil {
		t.Fatal("stored file missing:", err)
	}

	// survives a restart
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.List(""); len(got) != 1 || got[0].ID != d.ID {
		t.Fatalf("not persisted: %+v", got)
	}
	if len(s2.List("ft-60r")) != 1 || len(s2.List("nonsense")) != 0 {
		t.Fatal("search broken")
	}

	if err := s2.Delete(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "library", d.ID+".pdf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file not removed")
	}
	if err := s2.Delete(d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound, got", err)
	}
}

func TestDuplicateRejected(t *testing.T) {
	s := newStore(t)
	first, _ := s.Add(bytes.NewReader(pdfBytes), "a.pdf", "", 1<<20)
	dup, err := s.Add(bytes.NewReader(pdfBytes), "renamed.pdf", "", 1<<20)
	if !errors.Is(err, ErrDuplicate) || dup.ID != first.ID {
		t.Fatalf("want duplicate of first, got %v %+v", err, dup)
	}
	if len(s.List("")) != 1 {
		t.Fatal("duplicate was stored")
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, "library"))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestTypeDecidedByContentNotName(t *testing.T) {
	s := newStore(t)
	// an executable-looking blob named .pdf is rejected
	if _, err := s.Add(bytes.NewReader([]byte("MZ\x90\x00\x03\x00\x00\x00")), "totally.pdf", "", 1<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatal("binary should be unsupported, got", err)
	}
	// text named .exe is accepted as text and stored as .txt
	d, err := s.Add(strings.NewReader("# notes\nhello"), "evil.exe", "", 1<<20)
	if err != nil || d.Ext != "txt" {
		t.Fatalf("got %v %+v", err, d)
	}
	if _, err := s.Add(bytes.NewReader(nil), "empty.txt", "", 1<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatal("empty file should be unsupported")
	}
}

func TestSizeLimit(t *testing.T) {
	s := newStore(t)
	big := append(append([]byte{}, pdfBytes...), bytes.Repeat([]byte("x"), 2000)...)
	if _, err := s.Add(bytes.NewReader(big), "big.pdf", "", 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatal("want ErrTooLarge, got", err)
	}
	if len(s.List("")) != 0 {
		t.Fatal("oversize file stored")
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, "library"))
	if len(entries) != 0 {
		t.Fatalf("leftover files: %v", entries)
	}
	// exactly at the limit is fine
	if _, err := s.Add(bytes.NewReader(big), "ok.pdf", "", int64(len(big))); err != nil {
		t.Fatal("at-limit file rejected:", err)
	}
}

func TestUpdate(t *testing.T) {
	s := newStore(t)
	d, _ := s.Add(bytes.NewReader(pdfBytes), "x.pdf", "", 1<<20)
	title, tags := "  New title ", []string{" Yaesu ", "yaesu", "", "HT"}
	got, err := s.Update(d.ID, &title, &tags)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New title" || strings.Join(got.Tags, ",") != "yaesu,ht" {
		t.Fatalf("unexpected: %+v", got)
	}
	empty := "   "
	if _, err := s.Update(d.ID, &empty, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("blank title accepted")
	}
	if _, err := s.Update("nope", &title, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound")
	}
}

func TestFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/doc.pdf", func(w http.ResponseWriter, r *http.Request) { w.Write(pdfBytes) })
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>hi</html>"))
	})
	mux.HandleFunc("/missing", http.NotFound)
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/doc.pdf", http.StatusFound) })
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := newStore(t)
	f := &Fetcher{AllowPrivate: true, MaxBytes: 1 << 20}
	ctx := context.Background()

	d, err := f.Fetch(ctx, s, ts.URL+"/doc.pdf")
	if err != nil || d.SourceURL != ts.URL+"/doc.pdf" || d.Title != "doc" {
		t.Fatalf("fetch failed: %v %+v", err, d)
	}
	if _, err := f.Fetch(ctx, s, ts.URL+"/redir"); !errors.Is(err, ErrDuplicate) {
		t.Fatal("redirect to the same file should be a duplicate, got", err)
	}
	if _, err := f.Fetch(ctx, s, ts.URL+"/page"); !errors.Is(err, ErrWebPage) {
		t.Fatal("html page should be refused, got", err)
	}
	if _, err := f.Fetch(ctx, s, ts.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal("404 should be reported, got", err)
	}
	for _, bad := range []string{"ftp://example.org/x.pdf", "file:///etc/passwd", "not a url", "http://user:pw@example.org/x.pdf", ""} {
		if _, err := f.Fetch(ctx, s, bad); err == nil {
			t.Fatalf("%q should be refused", bad)
		}
	}
}

func TestFetchBlocksLocalAddressesByDefault(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(pdfBytes) }))
	defer ts.Close()
	s := newStore(t)
	f := &Fetcher{MaxBytes: 1 << 20} // AllowPrivate false, as in the app
	_, err := f.Fetch(context.Background(), s, ts.URL+"/x.pdf")
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatal("loopback target must be refused, got", err)
	}
	if len(s.List("")) != 0 {
		t.Fatal("something was stored")
	}
}

func TestFetchSizeLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(append(append([]byte{}, pdfBytes...), bytes.Repeat([]byte("x"), 5000)...))
	}))
	defer ts.Close()
	s := newStore(t)
	f := &Fetcher{AllowPrivate: true, MaxBytes: 1000}
	if _, err := f.Fetch(context.Background(), s, ts.URL+"/big.pdf"); !errors.Is(err, ErrTooLarge) {
		t.Fatal("want ErrTooLarge, got", err)
	}
}
