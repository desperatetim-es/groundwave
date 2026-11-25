// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package settings keeps the user's preferences in one small JSON file in the data
// folder. Every choice that makes the program contact the internet on its own is
// off until the user turns it on.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Pages are the pages that can be hidden from the menu and the home page. The names are
// the routes the web UI uses.
var Pages = []string{"library", "antenna", "callsigns", "logbook", "clocks"}

// Launch modes for the callsign databases.
const (
	LaunchOff    = "off"    // do nothing at launch
	LaunchCheck  = "check"  // ask each downloaded database's publisher whether a newer list exists
	LaunchUpdate = "update" // check, and download and rebuild any database that is out of date
)

// Settings is everything the user can choose. The zero value is the default: nothing
// contacts the internet by itself and every page is shown.
type Settings struct {
	// CheckUpdatesOnStart asks GitHub whether a newer release exists when the program starts.
	CheckUpdatesOnStart bool `json:"check_updates_on_start"`
	// HiddenPages lists pages (see Pages) removed from the menu and the home page. They can still be
	// reached by a direct link, so hiding one never loses anything.
	HiddenPages []string `json:"hidden_pages"`
	// CallsignLaunch is what to do about the callsign databases at launch: LaunchOff, LaunchCheck or
	// LaunchUpdate. It only ever concerns databases that have already been downloaded.
	CallsignLaunch string `json:"callsign_launch"`
}

// ValidationError is a choice the user can fix.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Default returns the settings used before the user chooses anything.
func Default() Settings {
	return Settings{HiddenPages: []string{}, CallsignLaunch: LaunchOff}
}

// Validate checks every value and returns a cleaned copy (no duplicates, pages in menu order).
func (s Settings) Validate() (Settings, error) {
	switch s.CallsignLaunch {
	case "":
		s.CallsignLaunch = LaunchOff
	case LaunchOff, LaunchCheck, LaunchUpdate:
	default:
		return s, &ValidationError{fmt.Sprintf("callsign_launch must be %q, %q or %q", LaunchOff, LaunchCheck, LaunchUpdate)}
	}
	hidden := map[string]bool{}
	for _, p := range s.HiddenPages {
		known := false
		for _, k := range Pages {
			known = known || k == p
		}
		if !known {
			return s, &ValidationError{fmt.Sprintf("%q is not a page that can be hidden", p)}
		}
		hidden[p] = true
	}
	s.HiddenPages = []string{}
	for _, p := range Pages { // menu order, no duplicates
		if hidden[p] {
			s.HiddenPages = append(s.HiddenPages, p)
		}
	}
	return s, nil
}

// Store loads and saves the settings file. A Store opened with an empty path keeps the settings in
// memory only (used by tests).
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// Open reads the settings file. A missing file means the defaults. A file that cannot be
// read or is invalid also means the defaults (the damaged file is left in place until the
// user saves a choice), so a bad file can never stop the program from starting.
func Open(path string) *Store {
	s := &Store{path: path, cur: Default()}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f Settings
	if json.Unmarshal(b, &f) != nil {
		return s
	}
	if v, err := f.Validate(); err == nil {
		s.cur = v
	}
	return s
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cur
	c.HiddenPages = append([]string{}, c.HiddenPages...)
	return c
}

// Update applies a change to the current settings, validates the result and saves it. If the
// change is invalid or cannot be saved, nothing changes.
func (s *Store) Update(change func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	next.HiddenPages = append([]string{}, next.HiddenPages...)
	change(&next)
	next, err := next.Validate()
	if err != nil {
		return s.cur, err
	}
	if err := s.save(next); err != nil {
		return s.cur, errors.New("the settings could not be saved")
	}
	s.cur = next
	return next, nil
}

// save writes the file atomically: a crash can leave the old file or the new one, never half of one.
func (s *Store) save(v Settings) error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
