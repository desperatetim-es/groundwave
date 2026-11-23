// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStateCode(t *testing.T) {
	for in, want := range map[string]string{
		"IL": "IL", "il": "IL", " Il ": "IL", "il.": "IL", "Illinois": "IL", "ILLINOIS": "IL",
		"north carolina": "NC", "North  Carolina": "NC", "new york": "NY", "West Virginia": "WV", "virginia": "VA", "Washington": "WA",
		"DC": "DC", "d.c.": "DC", "District of Columbia": "DC", "washington dc": "DC", "Washington D.C.": "DC",
		"Puerto Rico": "PR", "pr": "PR", "Guam": "GU", "virgin islands": "VI", "AE": "AE", "american samoa": "AS", "california,": "CA",
	} {
		if got, ok := StateCode(in); !ok || got != want {
			t.Errorf("StateCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "x", "xx", "ill", "illinoi", "new", "north", "carolina", "cary", "il il", "smith", "john", "1", "i l"} {
		if got, ok := StateCode(bad); ok {
			t.Errorf("StateCode(%q) = %q, want no state", bad, got)
		}
	}
	// every name points at a real code, and all 50 states plus DC are covered
	seen := map[string]bool{}
	for name, code := range stateNames {
		if !stateCodes[code] {
			t.Errorf("%q maps to %q, which is not a known code", name, code)
		}
		seen[code] = true
	}
	for _, code := range []string{"AL", "AK", "AZ", "AR", "CA", "CO", "CT", "DE", "FL", "GA", "HI", "ID", "IL", "IN", "IA", "KS", "KY", "LA", "ME", "MD", "MA", "MI", "MN", "MS", "MO", "MT", "NE", "NV", "NH", "NJ", "NM", "NY", "NC", "ND", "OH", "OK", "OR", "PA", "RI", "SC", "SD", "TN", "TX", "UT", "VT", "VA", "WA", "WV", "WI", "WY", "DC", "PR"} {
		if !seen[code] {
			t.Errorf("no state name maps to %s", code)
		}
	}
}

// cityStateDB builds a small data set designed to trip up "City, ST" parsing: a person
// named Cary who lives elsewhere, hyphenated and abbreviated city names, two Washingtons.
func cityStateDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	zp, dbp := filepath.Join(dir, "l_amat.zip"), filepath.Join(dir, "callsigns.db")
	type row struct {
		call, name, city, state, zip string
	}
	rows := []row{
		{"W9CIT", "BROWN, BOB", "CARY", "IL", "60013"},
		{"W9NAM", "CLARK, CARY", "PEORIA", "IL", "61602"}, // a person named Cary in Illinois
		{"K4WS", "WALKER, WANDA", "WINSTON-SALEM", "NC", "27101"},
		{"K3DC", "DISTRICT, DORA", "WASHINGTON", "DC", "20001"},
		{"W7WAL", "WASHINGTON, WALT", "SEATTLE", "WA", "98101"}, // surname Washington, in the state of Washington
		{"K2NYC", "YORK, NEIL", "NEW YORK", "NY", "10001"},
		{"K0STP", "PAUL, PETE", "ST. PAUL", "MN", "55101"},
		// two cities that contain the word Portland: the exact city should come first even though its call sign sorts later
		{"K1APO", "ADAMS, AL", "SOUTH PORTLAND", "ME", "04106"},
		{"K1ZPO", "ZANE, ZED", "PORTLAND", "ME", "04101"},
		{"K5HON", "HULA, HAL", "honolulu", "HI", "96813"}, // the FCC has some all-lower-case cities
	}
	var hds, ens []string
	for i, r := range rows {
		usi := 100 + i
		hds = append(hds, hd(usi, r.call, "A", "HA", "01/01/2020", "01/01/2099"))
		ens = append(ens, en(usi, r.call, "L", r.name, "", "", "", "", r.city, r.state, r.zip))
	}
	writeZip(t, zp, hds, ens, nil)
	if _, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp, Now: func() time.Time { return testNow }}); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCityCommaStateSearches(t *testing.T) {
	db := cityStateDB(t)
	for _, c := range []struct{ q, want string }{
		// the exact city is ranked ahead of cities that merely contain the word
		{"portland, me", "K1ZPO,K1APO"},
		{"portland, maine", "K1ZPO,K1APO"},
		{"south portland, me", "K1APO"},
		{"honolulu, hawaii", "K5HON"},
		// the state as an abbreviation, in any case or spacing
		{"cary, il", "W9CIT"},
		{"Cary, IL", "W9CIT"},
		{"cary,il", "W9CIT"},
		{"cary ,il", "W9CIT"},
		{"  cary , IL  ", "W9CIT"},
		// the state as a full name
		{"cary, illinois", "W9CIT"},
		{"Cary, Illinois", "W9CIT"},
		{"CARY, ILLINOIS", "W9CIT"},
		// the city wins over a person with that first name
		{"cary", "W9CIT,W9NAM"},
		{"cary, il", "W9CIT"},
		// hyphenated, abbreviated and multi-word cities and states
		{"winston-salem, nc", "K4WS"},
		{"winston salem, north carolina", "K4WS"},
		{"Winston-Salem, North Carolina", "K4WS"},
		{"st. paul, mn", "K0STP"},
		{"st paul, minnesota", "K0STP"},
		{"new york, ny", "K2NYC"},
		{"new york, new york", "K2NYC"},
		// the District of Columbia in its several spellings
		{"washington, dc", "K3DC"},
		{"washington, d.c.", "K3DC"},
		{"washington, district of columbia", "K3DC"},
		// no such city in the state: fall back to the words as a name, so a surname still finds people
		{"washington, wa", "W7WAL"},
		{"clark, il", "W9NAM"},
		{"cary, wa", ""},
		// the wrong state finds nothing
		{"cary, nc", ""},
		{"peoria, mo", ""},
		// a state by itself, and explicit state filters
		{", il", "W9CIT,W9NAM"},
		{"st:illinois", "W9CIT,W9NAM"},
		{"state:Illinois", "W9CIT,W9NAM"},
		{"cary st:illinois", "W9CIT,W9NAM"},
		{"cary st:il", "W9CIT,W9NAM"},
		// text after a comma that is not a state is ordinary text, and never an error
		{"clark, cary", "W9NAM"},
		{"brown, bob", "W9CIT"},
		{"cary, xx", ""},
		{"cary,", "W9CIT,W9NAM"},
		{"smith, john", ""},
		// without a comma a trailing state is not recognised (it would be ambiguous with names)
		{"cary il", ""},
	} {
		got, err := db.Search(c.q, 10)
		if err != nil {
			t.Errorf("%q: %v", c.q, err)
			continue
		}
		if calls(got) != c.want {
			t.Errorf("Search(%q) = %q, want %q", c.q, calls(got), c.want)
		}
	}
	// an all-lower-case city from the FCC is displayed properly
	if r, _ := db.Lookup("K5HON"); len(r) != 1 || r[0].City != "Honolulu" {
		t.Errorf("city = %+v", r)
	}
	// "il" alone is a word, not a state
	if got, _ := db.Search("il", 10); len(got) != 0 {
		t.Errorf("a bare il matched %s", calls(got))
	}
	// a bad explicit state is an error the user can fix, not a database failure
	for _, bad := range []string{"st:ILL", "st:I", "state:illinoi", "cary st:xx"} {
		_, err := db.Search(bad, 10)
		var qe *QueryError
		if err == nil || !asQueryError(err, &qe) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// hostile text around a comma still cannot break the query
	for _, q := range []string{`cary", il`, `'; DROP TABLE license; --, il`, `", "`, `,,,`, `, , il`, `a,b,c,il`} {
		db.Search(q, 5)
	}
	if r, err := db.Lookup("W9CIT"); err != nil || len(r) != 1 {
		t.Fatalf("the database was damaged: %v %v", r, err)
	}
}

func asQueryError(err error, target **QueryError) bool {
	qe, ok := err.(*QueryError)
	if ok {
		*target = qe
	}
	return ok
}
