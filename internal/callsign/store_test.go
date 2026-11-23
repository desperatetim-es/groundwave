// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fccZipBytes(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.zip")
	h, e, a := testFiles()
	writeZip(t, p, h, e, a)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestStore(t *testing.T, url string) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "callsigns")
	s, err := NewStore(StoreOptions{Dir: dir, URL: url, AnyHostForTests: true, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, dir
}

func waitDone(t *testing.T, s *Store) UpdateState {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Status().Update; !st.Running {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the update did not finish")
	return UpdateState{}
}

func serveZip(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Sun, 08 Nov 2026 14:08:58 GMT")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateDownloadsBuildsAndInstalls(t *testing.T) {
	srv := serveZip(t, fccZipBytes(t))
	s, dir := newTestStore(t, srv.URL)

	if st := s.Status(); st.Available || st.Update.Running {
		t.Fatalf("fresh store: %+v", st)
	}
	if _, err := s.Search("w1aw", 5); err != ErrNoDatabase {
		t.Errorf("search before any download: %v", err)
	}
	if err := s.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	up := waitDone(t, s)
	if up.Error != "" || up.Cancelled || up.Phase != "Done" || up.Fraction != 1 || up.FinishedAt == nil || up.StartedAt == nil {
		t.Fatalf("update = %+v", up)
	}
	st := s.Status()
	if !st.Available || st.Records != 9 || st.SourceModified != "Sun, 08 Nov 2026 14:08:58 GMT" || st.BuiltAt == nil || st.AgeDays == nil {
		t.Errorf("status = %+v", st)
	}
	if r, err := s.Search("kd9", 5); err != nil || len(r) != 1 || r[0].Call != "KD9TST" {
		t.Errorf("search after update: %v %v", r, err)
	}
	if r, err := s.Lookup("W1AW/4"); err != nil || len(r) != 1 {
		t.Errorf("lookup: %v %v", r, err)
	}
	// nothing is left behind except the database
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "callsigns.db" {
		t.Errorf("files left in the data folder: %v", names)
	}
}

func TestUpdatingWhileTheOldDatabaseIsOpenWorks(t *testing.T) {
	// On Windows a file cannot be replaced while open: the store must release it first.
	srv := serveZip(t, fccZipBytes(t))
	s, _ := newTestStore(t, srv.URL)
	for i := 0; i < 2; i++ {
		if err := s.StartUpdate(); err != nil {
			t.Fatal(err)
		}
		if up := waitDone(t, s); up.Error != "" {
			t.Fatalf("update %d failed: %+v", i+1, up)
		}
		if r, err := s.Lookup("W1AW"); err != nil || len(r) != 1 {
			t.Fatalf("after update %d: %v %v", i+1, r, err)
		}
	}
}

func TestOnlyOneUpdateAtATimeAndItCanBeCancelled(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "1000000")
		w.Write([]byte("PK")) // the start of a file that never finishes
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	s, dir := newTestStore(t, srv.URL)
	if err := s.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500 && hits.Load() == 0; i++ { // wait until the download is really under way
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatal("the download never started")
	}
	if err := s.StartUpdate(); err != ErrBusy {
		t.Errorf("second update while one is running: %v", err)
	}
	if st := s.Status().Update; !st.Running {
		t.Errorf("not reported as running: %+v", st)
	}
	s.CancelUpdate()
	up := s.Status().Update
	if up.Running || !up.Cancelled || up.Error != "" || up.Phase != "Cancelled" {
		t.Errorf("after cancel: %+v", up)
	}
	if hits.Load() != 1 {
		t.Errorf("the server was contacted %d times", hits.Load())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("a cancelled update left files behind: %v", entries)
	}
	// and a new update can be started afterwards
	if err := s.StartUpdate(); err != nil {
		t.Errorf("could not restart after a cancel: %v", err)
	}
}

func TestFailedUpdatesKeepTheOldDatabase(t *testing.T) {
	good := fccZipBytes(t)
	var mode atomic.Value
	mode.Store("good")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "good":
			w.Write(good)
		case "500":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "garbage":
			w.Write([]byte(strings.Repeat("this is not a zip file ", 50)))
		case "truncated":
			w.Header().Set("Content-Length", "100000")
			w.Write(good[:50])
			panic(http.ErrAbortHandler)
		case "huge":
			w.Header().Set("Content-Length", "99999999999")
		}
	}))
	defer srv.Close()
	s, dir := newTestStore(t, srv.URL)
	if err := s.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if !s.Status().Available {
		t.Fatal("the first update failed")
	}

	for _, c := range []struct{ mode, want string }{
		{"500", "500"},
		{"garbage", "not a valid zip"},
		{"truncated", ""}, // a read error: any message, but it must fail
		{"huge", "larger than expected"},
	} {
		mode.Store(c.mode)
		if err := s.StartUpdate(); err != nil {
			t.Fatal(err)
		}
		up := waitDone(t, s)
		if up.Error == "" || up.Phase != "Failed" || up.Cancelled || !strings.Contains(up.Error, c.want) {
			t.Errorf("%s: update = %+v", c.mode, up)
		}
		if r, err := s.Lookup("KD9TST"); err != nil || len(r) != 1 {
			t.Errorf("%s: the old database was lost: %v %v", c.mode, r, err)
		}
		if st := s.Status(); !st.Available || st.Records != 9 {
			t.Errorf("%s: status = %+v", c.mode, st)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("%s: files left behind: %v", c.mode, entries)
		}
	}
}

func TestStoreRefusesNonFCCAddressesByDefault(t *testing.T) {
	for _, u := range []string{"http://data.fcc.gov/x.zip", "https://example.org/l_amat.zip", "https://fcc.gov.evil.example/x.zip", "https://evilfcc.gov/x.zip", "file:///etc/passwd", "ftp://data.fcc.gov/x.zip"} {
		s, err := NewStore(StoreOptions{Dir: t.TempDir(), URL: u})
		if err != nil {
			t.Fatal(err)
		}
		s.StartUpdate()
		up := waitDone(t, s)
		if up.Error == "" || !strings.Contains(up.Error, "not an FCC https address") {
			t.Errorf("%s: %+v", u, up)
		}
		s.Close()
	}
	for h, want := range map[string]bool{"data.fcc.gov": true, "FCC.gov": true, "www.fcc.gov": true, "fcc.gov.evil.example": false, "evilfcc.gov": false, "": false, "fcc.org": false} {
		if fccHost(h) != want {
			t.Errorf("fccHost(%q) = %v", h, !want)
		}
	}
	if !strings.HasPrefix(FCCURL, "https://data.fcc.gov/") {
		t.Errorf("FCCURL = %s", FCCURL)
	}
}

func TestStoreCleansUpAfterAnInterruptedRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "callsigns")
	os.MkdirAll(dir, 0o755)
	for _, n := range []string{"callsigns.db.building", "l_amat.zip.part", "l_amat.zip"} {
		os.WriteFile(filepath.Join(dir, n), bytes.Repeat([]byte("x"), 100), 0o644)
	}
	s, err := NewStore(StoreOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("leftovers not removed: %v", entries)
	}
	if st := s.Status(); st.Available {
		t.Error("a database appeared from nowhere")
	}
}

func TestStoreOpensAnExistingDatabase(t *testing.T) {
	_, dbp, _ := buildTestDB(t)
	dir := filepath.Dir(dbp)
	s, err := NewStore(StoreOptions{Dir: dir, Now: func() time.Time { return testNow.AddDate(0, 0, 10) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st := s.Status()
	if !st.Available || st.Records != 9 || st.AgeDays == nil {
		t.Fatalf("status = %+v", st)
	}
	if r, err := s.Search("w1aw", 5); err != nil || len(r) != 1 {
		t.Errorf("%v %v", r, err)
	}
}
