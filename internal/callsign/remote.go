// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// RemoteState is what a check of the publisher found: whether it has a newer list than the database
// that was built.
type RemoteState struct {
	CheckedAt time.Time `json:"checked_at"`
	Modified  string    `json:"modified,omitempty"` // when the publisher says its list was last changed
	Newer     bool      `json:"newer"`              // that is later than the list the database was built from
	Error     string    `json:"error,omitempty"`    // why the check could not tell
}

// CheckRemote asks the publisher when its list was last changed, without downloading it (a HEAD
// request, or a one-byte ranged GET if the server refuses HEAD), and compares that with the list the
// database was built from. Only a database that already exists is checked: a country that was never
// downloaded is never fetched on the user's behalf. The answer is remembered and shows in Status.
func (s *Store) CheckRemote(ctx context.Context) (RemoteState, error) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return RemoteState{}, ErrNoDatabase
	}
	st := RemoteState{CheckedAt: s.o.Now()}
	local, err := db.Info()
	if err != nil {
		return RemoteState{}, err
	}
	mod, err := s.remoteModified(ctx)
	switch {
	case err != nil:
		st.Error = err.Error()
	case mod == "":
		st.Error = s.o.Source.Org + " does not say when its list was last changed."
	default:
		st.Modified = mod
		remote, e1 := http.ParseTime(mod)
		built, e2 := http.ParseTime(local.SourceModified)
		switch {
		case e1 != nil:
			st.Error = s.o.Source.Org + " gave a date the program could not read."
		case e2 != nil:
			// The database does not record when its list was published (it should), so assume it is out of date only
			// if it is older than the remote list by the day it was built.
			st.Newer = remote.After(local.BuiltAt)
		default:
			st.Newer = remote.After(built)
		}
	}
	s.mu.Lock()
	s.remote = &st
	s.mu.Unlock()
	return st, nil
}

// remoteModified returns the Last-Modified header of the publisher's list.
func (s *Store) remoteModified(ctx context.Context) (string, error) {
	u, err := s.downloadURL()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := s.httpClient(0)
	try := func(method string, ranged bool) (string, int, error) {
		req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("User-Agent", "groundwave")
		if ranged {
			req.Header.Set("Range", "bytes=0-0")
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, err
		}
		resp.Body.Close() // only the headers are wanted; nothing of the file is read
		return resp.Header.Get("Last-Modified"), resp.StatusCode, nil
	}
	mod, code, err := try(http.MethodHead, false)
	if err == nil && code == http.StatusOK {
		return mod, nil
	}
	mod, code, err2 := try(http.MethodGet, true) // some servers refuse HEAD
	switch {
	case err2 != nil:
		if err != nil {
			return "", errors.New("Could not reach " + s.o.Source.Org + "'s server. You may be offline.")
		}
		return "", errors.New("Could not reach " + s.o.Source.Org + "'s server.")
	case code != http.StatusOK && code != http.StatusPartialContent:
		return "", errors.New(s.o.Source.Org + "'s server did not answer the check.")
	}
	return mod, nil
}
