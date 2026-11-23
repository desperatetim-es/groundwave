// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStanding(t *testing.T) {
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		status, expires, wantCode string
	}{
		{"A", "2030-01-02", StandingActive},
		{"A", "2026-11-12", StandingActive},  // expires today: still valid today
		{"A", "2026-11-11", StandingGrace},   // expired yesterday
		{"A", "2025-01-01", StandingGrace},   // within two years
		{"A", "2024-11-12", StandingGrace},   // exactly two years ago: the last day of grace
		{"A", "2024-11-11", StandingExpired}, // the day after the grace period ends
		{"A", "2010-01-01", StandingExpired}, // stale "Active" record
		{"A", "", StandingActive},            // no date recorded: take the FCC at its word
		{"E", "2026-01-01", StandingGrace},
		{"E", "2020-01-01", StandingExpired},
		{"E", "", StandingExpired},
		{"C", "2030-01-01", StandingOther},
		{"T", "", StandingOther},
		{"X", "2030-01-01", StandingOther},
	} {
		code, label := Standing(c.status, c.expires, now)
		if code != c.wantCode || label == "" {
			t.Errorf("Standing(%q, %q) = %q %q, want %q", c.status, c.expires, code, label, c.wantCode)
		}
	}
	if _, label := Standing("A", "2026-06-01", now); label != "Expired (in the renewal grace period)" {
		t.Errorf("grace label = %q", label)
	}
	if _, label := Standing("C", "", now); label != "Cancelled" {
		t.Errorf("cancelled label = %q", label)
	}
}

func TestLicensedStationsAreRankedAheadOfExpiredOnes(t *testing.T) {
	// Two holders of one call: an old license the FCC still calls Active although it lapsed in
	// 2020, and a current one. The current holder must come first however the rows happen to be ordered.
	dir := t.TempDir()
	zp, dbp := filepath.Join(dir, "l_amat.zip"), filepath.Join(dir, "callsigns.db")
	writeZip(t, zp,
		[]string{hd(20, "W9LA", "A", "HA", "01/01/2000", "01/01/2020"), hd(21, "W9LA", "A", "HV", "01/01/2024", "01/01/2099")},
		[]string{en(20, "W9LA", "L", "LAPSED, LARRY", "LARRY", "", "LAPSED", "", "PEORIA", "IL", "61602"), en(21, "W9LA", "L", "CURRENT, CAROL", "CAROL", "", "CURRENT", "", "PEORIA", "IL", "61602")},
		nil)
	if _, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp, Now: func() time.Time { return testNow }}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for name, f := range map[string]func() ([]Record, error){
		"Lookup": func() ([]Record, error) { return db.Lookup("W9LA") },
		"Search": func() ([]Record, error) { return db.Search("w9la", 10) },
	} {
		res, err := f()
		if err != nil || len(res) != 2 || res[0].Name != "Carol Current" {
			t.Errorf("%s: the currently licensed holder should come first: %+v %v", name, res, err)
		}
	}
}
