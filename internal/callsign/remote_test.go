// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// publisher is a fake FCC server whose "last changed" date and behaviour the test controls. It records
// whether any of the file's body was ever sent for a check.
type publisher struct {
	mu           sync.Mutex
	modified     string
	refuseHead   bool
	status       int // when not 0, every request answers with this
	bodyForCheck int // bytes of the list sent to requests that were only checks
	srv          *httptest.Server
	zip          []byte
}

func newPublisher(t *testing.T, modified string) *publisher {
	p := &publisher{modified: modified, zip: fccZipBytes(t)}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.status != 0 {
			w.WriteHeader(p.status)
			return
		}
		if p.modified != "" {
			w.Header().Set("Last-Modified", p.modified)
		}
		switch {
		case r.Method == http.MethodHead && p.refuseHead:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.Header.Get("Range") != "":
			w.Header().Set("Content-Range", "bytes 0-0/100")
			w.WriteHeader(http.StatusPartialContent)
			w.Write(p.zip[:1])
			p.bodyForCheck++
		default:
			w.Write(p.zip) // a real download
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *publisher) set(modified string) { p.mu.Lock(); p.modified = modified; p.mu.Unlock() }

// builtStore downloads the publisher's list into a new Store, so there is a database to compare with.
func builtStore(t *testing.T, p *publisher) *Store {
	t.Helper()
	s, _ := newTestStore(t, p.srv.URL)
	if err := s.StartUpdate(); err != nil {
		t.Fatal(err)
	}
	if up := waitDone(t, s); up.Error != "" {
		t.Fatalf("the build failed: %+v", up)
	}
	return s
}

const built = "Sun, 08 Nov 2026 14:08:58 GMT"

func TestCheckRemoteComparesPublicationDates(t *testing.T) {
	for name, tc := range map[string]struct {
		later string
		newer bool
	}{
		"same date":        {built, false},
		"newer list":       {"Sun, 15 Nov 2026 14:08:58 GMT", true},
		"one second later": {"Sun, 08 Nov 2026 14:08:59 GMT", true},
		"older list":       {"Sun, 01 Nov 2026 14:08:58 GMT", false},
	} {
		p := newPublisher(t, built)
		s := builtStore(t, p)
		p.set(tc.later)
		st, err := s.CheckRemote(context.Background())
		if err != nil || st.Error != "" || st.Newer != tc.newer || st.Modified != tc.later {
			t.Errorf("%s: %+v err %v", name, st, err)
		}
		if got := s.Status().Remote; got == nil || got.Newer != tc.newer {
			t.Errorf("%s: Status().Remote = %+v", name, got)
		}
	}
}

func TestCheckRemoteNeverDownloadsTheList(t *testing.T) {
	p := newPublisher(t, built)
	s := builtStore(t, p)
	var served int
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Header.Get("Range") == "" {
			served++ // a full download
		}
		p.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer counting.Close()
	s.o.URL = counting.URL
	for i := 0; i < 3; i++ {
		if _, err := s.CheckRemote(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	p.refuseHead = true // and the fallback is a one-byte range request, not the file
	for i := 0; i < 3; i++ {
		s.CheckRemote(context.Background())
	}
	if served != 0 {
		t.Fatalf("a check downloaded the whole list %d times", served)
	}
}

func TestCheckRemoteFallsBackWhenHeadIsRefused(t *testing.T) {
	p := newPublisher(t, built)
	s := builtStore(t, p)
	p.refuseHead = true
	p.set("Sun, 15 Nov 2026 14:08:58 GMT")
	st, err := s.CheckRemote(context.Background())
	if err != nil || st.Error != "" || !st.Newer {
		t.Fatalf("with HEAD refused: %+v %v", st, err)
	}
	if p.bodyForCheck != 1 {
		t.Errorf("the fallback should send one ranged request, sent %d", p.bodyForCheck)
	}
}

func TestCheckRemoteProblemsAreReportedNotGuessed(t *testing.T) {
	p := newPublisher(t, built)
	s := builtStore(t, p)

	p.set("") // no Last-Modified header
	if st, err := s.CheckRemote(context.Background()); err != nil || st.Newer || !strings.Contains(st.Error, "does not say when") {
		t.Errorf("no date: %+v %v", st, err)
	}
	p.set("not a date")
	if st, _ := s.CheckRemote(context.Background()); st.Newer || !strings.Contains(st.Error, "could not read") {
		t.Errorf("unreadable date: %+v", st)
	}
	p.set("Sun, 15 Nov 2026 14:08:58 GMT")
	p.status = http.StatusInternalServerError
	if st, _ := s.CheckRemote(context.Background()); st.Newer || !strings.Contains(st.Error, "did not answer") {
		t.Errorf("server error: %+v", st)
	}
	p.srv.Close() // offline
	if st, _ := s.CheckRemote(context.Background()); st.Newer || !strings.Contains(st.Error, "Could not reach") {
		t.Errorf("offline: %+v", st)
	}
}

func TestCheckRemoteNeedsADatabaseAndAGoodAddress(t *testing.T) {
	// no database: a country that was never downloaded is never contacted
	p := newPublisher(t, built)
	empty, _ := newTestStore(t, p.srv.URL)
	if _, err := empty.CheckRemote(context.Background()); err != ErrNoDatabase {
		t.Fatalf("no database: %v", err)
	}
	// the same address restrictions as a download
	s := builtStore(t, p)
	dir := s.o.Dir
	s.Close()
	for _, u := range []string{"http://data.fcc.gov/x.zip", "https://example.org/x.zip", "https://fcc.gov.evil.example/x.zip"} {
		bad, err := NewStore(StoreOptions{Dir: dir, URL: u, Now: func() time.Time { return testNow }})
		if err != nil {
			t.Fatal(err)
		}
		st, err := bad.CheckRemote(context.Background())
		if err != nil || !strings.Contains(st.Error, "not an FCC https address") {
			t.Errorf("%s: %+v %v", u, st, err)
		}
		bad.Close()
	}
}

func TestDirectoryChecksOnlyDownloadedDatabases(t *testing.T) {
	us, ca := newPublisher(t, built), newPublisher(t, built)
	d, _ := newTestDirectory(t, us.srv.URL, ca.srv.URL)
	if got := d.CheckRemote(context.Background()); len(got) != 0 {
		t.Fatalf("nothing downloaded, yet %v was checked", got)
	}
	d.StartUpdate(IDUS)
	if up := waitSource(t, d, IDUS); up.Error != "" {
		t.Fatalf("%+v", up)
	}
	us.set("Sun, 15 Nov 2026 14:08:58 GMT")
	got := d.CheckRemote(context.Background())
	if len(got) != 1 || !got[IDUS].Newer {
		t.Fatalf("only the US database exists: %+v", got)
	}
	if st := d.Status(); st.Sources[0].Remote == nil || !st.Sources[0].Remote.Newer || st.Sources[1].Remote != nil {
		t.Fatalf("status: %+v / %+v", st.Sources[0].Remote, st.Sources[1].Remote)
	}
}
