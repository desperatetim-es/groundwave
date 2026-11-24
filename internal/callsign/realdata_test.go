// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"archive/zip"
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealFCCFile builds a database from the real FCC download and reports what
// came out. It is skipped unless GROUNDWAVE_FCC_ZIP points at l_amat.zip, since
// that file is about 200 MB. Run: GROUNDWAVE_FCC_ZIP=path/to/l_amat.zip go test -run RealFCC -v
func TestRealFCCFile(t *testing.T) {
	zipPath := os.Getenv("GROUNDWAVE_FCC_ZIP")
	if zipPath == "" {
		t.Skip("set GROUNDWAVE_FCC_ZIP to the real l_amat.zip to run this")
	}

	// Which entity types do the kept licenses carry? (statistics only)
	types := map[string]int{}
	if zr, err := zip.OpenReader(zipPath); err == nil {
		for _, f := range zr.File {
			if f.Name != "EN.dat" {
				continue
			}
			rc, _ := f.Open()
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
			for sc.Scan() {
				if p := strings.SplitN(sc.Text(), "|", 7); len(p) > 5 && p[0] == "EN" {
					types[p[5]]++
				}
			}
			rc.Close()
		}
		zr.Close()
	}
	t.Logf("EN entity types: %v", types)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "callsigns.db")
	start := time.Now()
	lastPhase := ""
	st, err := Import(context.Background(), ImportOptions{ZipPath: zipPath, DBPath: dbPath, SourceModified: "test",
		Progress: func(p Progress) {
			if p.Phase != lastPhase {
				lastPhase = p.Phase
				t.Logf("[%5.1fs] %s", time.Since(start).Seconds(), p.Phase)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dbPath)
	t.Logf("built in %v: %d records %v, %d with a location, %d clubs; database file %.1f MB", time.Since(start).Round(time.Second), st.Records, st.ByStatus, st.WithLocation, st.Clubs, float64(fi.Size())/1e6)

	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"W1AW", "kd9", "w1aw/4", "60013", "smith cary", "st:IL cary", "call:K9 st:IL"} {
		t0 := time.Now()
		res, err := db.Search(q, 5)
		if err != nil {
			t.Errorf("%q: %v", q, err)
			continue
		}
		first := ""
		if len(res) > 0 {
			r := res[0]
			if r.Club || q == "W1AW" || q == "w1aw/4" { // only organisations are printed in full
				first = r.Call + " " + r.Name + ", " + r.City + " " + r.State + " " + r.Grid + " (" + r.StatusName + ", " + r.ClassName + ")"
			} else {
				first = r.Call + " (" + r.StatusName + ", " + r.ClassName + ", " + r.State + ", grid " + r.Grid + ")"
			}
		}
		t.Logf("search %-14q -> %d results in %v; first: %s", q, len(res), time.Since(t0).Round(time.Microsecond), first)
	}
}

// TestRealISEDFile builds a database from the real ISED download and reports what came out. It is
// skipped unless GROUNDWAVE_ISED_ZIP points at amateur_delim.zip. Run:
// GROUNDWAVE_ISED_ZIP=path/to/amateur_delim.zip go test -run RealISED -v ./internal/callsign
func TestRealISEDFile(t *testing.T) {
	zipPath := os.Getenv("GROUNDWAVE_ISED_ZIP")
	if zipPath == "" {
		t.Skip("set GROUNDWAVE_ISED_ZIP to the real amateur_delim.zip to run this")
	}
	// how many lines the data file has, to compare with the records kept
	lines := 0
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "amateur_delim.txt") {
			rc, _ := f.Open()
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
			for sc.Scan() {
				lines++
			}
			rc.Close()
		}
	}
	zr.Close()

	dbp := filepath.Join(t.TempDir(), "callsigns-ca.db")
	st, err := ImportCanada(context.Background(), ImportOptions{ZipPath: zipPath, DBPath: dbp, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("file lines (including the header): %d; records kept: %d; with location: %d; clubs: %d", lines, st.Records, st.WithLocation, st.Clubs)
	if lines-1-st.Records > 0 {
		t.Logf("%d data lines were not kept", lines-1-st.Records)
	}
}
