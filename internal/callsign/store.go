// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FCCURL is where the FCC publishes the complete amateur license file. It is a
// constant, never taken from a request, and the download follows redirects only
// within fcc.gov.
const FCCURL = "https://data.fcc.gov/download/pub/uls/complete/l_amat.zip"

var (
	ErrBusy     = errors.New("a database update is already running")
	ErrUpdating = errors.New("the callsign database is being replaced; try again in a moment")
)

// UpdateState describes a database update, running or finished.
type UpdateState struct {
	Running    bool       `json:"running"`
	Phase      string     `json:"phase,omitempty"`
	Fraction   float64    `json:"fraction"`
	BytesDone  int64      `json:"bytes_done,omitempty"`
	BytesTotal int64      `json:"bytes_total,omitempty"`
	Error      string     `json:"error,omitempty"`
	Cancelled  bool       `json:"cancelled,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Status is what the UI shows about the database.
type Status struct {
	Available      bool        `json:"available"`
	Records        int         `json:"records,omitempty"`
	BuiltAt        *time.Time  `json:"built_at,omitempty"`
	AgeDays        *int        `json:"age_days,omitempty"`
	SourceModified string      `json:"source_modified,omitempty"` // when the FCC published the file the database was built from
	Update         UpdateState `json:"update"`

	// Remote is what the last check of the publisher found (see CheckRemote); nil if none was made.
	Remote *RemoteState `json:"remote,omitempty"`

	// Sources is set only by a Directory: the state of each country's database.
	Sources []SourceStatus `json:"sources,omitempty"`
}

// StoreOptions configures a Store. Only Dir is required.
type StoreOptions struct {
	Dir    string  // holds the database and the temporary download
	Source *Source // default US
	URL    string  // default Source.URL; tests point it at a local server
	// AnyHostForTests lifts the https and issuing-authority host restrictions on the download. Tests only.
	AnyHostForTests bool
	Now             func() time.Time
}

// Store owns the callsign database: it opens it, searches it, and rebuilds it
// from the FCC's file on request.
type Store struct {
	o      StoreOptions
	dbPath string

	mu     sync.RWMutex
	db     *DB
	remote *RemoteState
	upd    UpdateState
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStore builds a Store and opens the existing database, if there is one.
func NewStore(o StoreOptions) (*Store, error) {
	if o.Source == nil {
		o.Source = US
	}
	if o.URL == "" {
		o.URL = o.Source.URL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{o: o, dbPath: filepath.Join(o.Dir, o.Source.DBFile)}
	// Leftovers of an update that was interrupted by quitting the program.
	for _, junk := range []string{s.dbPath + ".building", filepath.Join(o.Dir, o.Source.ZipName+".part"), filepath.Join(o.Dir, o.Source.ZipName)} {
		os.Remove(junk)
	}
	if db, err := Open(s.dbPath); err == nil {
		s.db = db
	}
	return s, nil
}

// Close stops any update and closes the database.
func (s *Store) Close() {
	s.CancelUpdate()
	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
	s.mu.Unlock()
}

// Status returns the database's size and age and the state of any update.
func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Status{Update: s.upd}
	if s.db == nil {
		return st
	}
	info, err := s.db.Info()
	if err != nil {
		return st
	}
	st.Available, st.Records, st.SourceModified = true, info.Records, info.SourceModified
	if s.remote != nil {
		r := *s.remote
		st.Remote = &r
	}
	if !info.BuiltAt.IsZero() {
		b := info.BuiltAt
		age := int(s.o.Now().Sub(b).Hours() / 24)
		st.BuiltAt, st.AgeDays = &b, &age
	}
	return st
}

func (s *Store) current() (*DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		if s.upd.Running {
			return nil, ErrUpdating
		}
		return nil, ErrNoDatabase
	}
	return s.db, nil
}

// Search finds licenses; see DB.Search.
func (s *Store) Search(q string, limit int) ([]Record, error) {
	db, err := s.current()
	if err != nil {
		return nil, err
	}
	return db.Search(q, limit)
}

// Lookup returns the licenses for one call sign; see DB.Lookup.
func (s *Store) Lookup(call string) ([]Record, error) {
	db, err := s.current()
	if err != nil {
		return nil, err
	}
	return db.Lookup(call)
}

// ---- updating -----------------------------------------------------------------------

// StartUpdate begins downloading and building the database in the background.
func (s *Store) StartUpdate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upd.Running {
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := s.o.Now()
	s.upd = UpdateState{Running: true, Phase: "Starting", StartedAt: &now}
	s.cancel, s.done = cancel, make(chan struct{})
	go s.runUpdate(ctx, s.done)
	return nil
}

// CancelUpdate stops a running update and waits for it to finish.
func (s *Store) CancelUpdate() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

func (s *Store) setPhase(phase string, frac float64) {
	s.mu.Lock()
	s.upd.Phase, s.upd.Fraction = phase, frac
	s.mu.Unlock()
}

func (s *Store) runUpdate(ctx context.Context, done chan struct{}) {
	defer close(done)
	zipPath := filepath.Join(s.o.Dir, s.o.Source.ZipName)
	defer os.Remove(zipPath)

	modified, err := s.download(ctx, zipPath)
	if err == nil {
		s.setPhase("Reading licenses", 0)
		_, err = s.o.Source.Import(ctx, ImportOptions{
			ZipPath: zipPath, DBPath: s.dbPath, SourceModified: modified, Now: s.o.Now,
			Progress: func(p Progress) { s.setPhase(p.Phase, p.Fraction) },
			BeforeReplace: func() error { // free the old file so Windows can replace it
				s.mu.Lock()
				defer s.mu.Unlock()
				if s.db != nil {
					s.db.Close()
					s.db = nil
				}
				return nil
			},
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// If the old database was released for replacement, open whatever is on disk now
	// (the new one, or the old one if the swap failed). If it was never released, the
	// existing open handle is still the right one: opening a second would leak it.
	if s.db == nil {
		if db, err := Open(s.dbPath); err == nil {
			s.db = db
		}
	}
	fin := s.o.Now()
	s.upd.Running, s.upd.FinishedAt, s.cancel = false, &fin, nil
	switch {
	case err == nil:
		s.upd.Phase, s.upd.Fraction, s.upd.Error, s.upd.Cancelled = "Done", 1, "", false
		s.remote = nil // an earlier "a newer list exists" no longer applies: the database was just rebuilt from it
	case errors.Is(err, context.Canceled):
		s.upd.Phase, s.upd.Cancelled, s.upd.Error = "Cancelled", true, ""
	default:
		s.upd.Phase, s.upd.Error, s.upd.Cancelled = "Failed", friendlyUpdateError(err, s.o.Source), false
	}
}

func friendlyUpdateError(err error, src *Source) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no space left") || strings.Contains(msg, "not enough space") || strings.Contains(msg, "There is not enough space"):
		return "There is not enough free disk space. The update needs " + src.Disk + " while it runs."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp") || strings.Contains(msg, "connection refused"):
		return "Could not reach " + src.Org + "'s server. You may be offline."
	}
	return msg
}

// downloadURL returns the address the list is fetched from, after checking that it is an https
// address of the authority that issues the list.
func (s *Store) downloadURL() (*url.URL, error) {
	u, err := url.Parse(s.o.URL)
	if err != nil {
		return nil, err
	}
	src := s.o.Source
	if !s.o.AnyHostForTests && (u.Scheme != "https" || !src.Host(u.Hostname())) {
		return nil, fmt.Errorf("the download address is not an %s https address", src.Org)
	}
	return u, nil
}

// httpClient returns a client that follows at most five redirects, all of them to https addresses of
// the issuing authority.
func (s *Store) httpClient(timeout time.Duration) *http.Client {
	src := s.o.Source
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !s.o.AnyHostForTests && (req.URL.Scheme != "https" || !src.Host(req.URL.Hostname())) {
				return fmt.Errorf("%s's server redirected somewhere that is not an %s address", src.Org, src.Org)
			}
			return nil
		},
	}
}

// download fetches the source's file into dest and returns when it was published.
func (s *Store) download(ctx context.Context, dest string) (modified string, err error) {
	s.setPhase("Downloading", 0)
	u, err := s.downloadURL()
	if err != nil {
		return "", err
	}
	src := s.o.Source
	client := s.httpClient(0) // large file: the context controls cancellation
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "groundwave")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s's server answered %s", src.Org, resp.Status)
	}
	total := resp.ContentLength
	if total > src.MaxBytes {
		return "", errors.New("the download is larger than expected, so it was refused")
	}
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(part)
		}
	}()
	buf := make([]byte, 256<<10)
	var done int64
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			done += int64(n)
			if done > src.MaxBytes {
				return "", errors.New("the download is larger than expected, so it was refused")
			}
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				s.mu.Lock()
				s.upd.BytesDone, s.upd.BytesTotal = done, total
				if total > 0 {
					s.upd.Fraction = float64(done) / float64(total)
				}
				s.mu.Unlock()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if total >= 0 && done != total {
		return "", fmt.Errorf("the download was cut short (%d of %d bytes); try again", done, total)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(part, dest); err != nil {
		return "", err
	}
	ok = true
	s.mu.Lock()
	s.upd.BytesDone, s.upd.BytesTotal, s.upd.Fraction = done, done, 1
	s.mu.Unlock()
	return resp.Header.Get("Last-Modified"), nil
}

func fccHost(h string) bool {
	h = strings.ToLower(h)
	return h == "fcc.gov" || strings.HasSuffix(h, ".fcc.gov")
}
