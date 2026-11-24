// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"archive/zip"
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kd9psm/groundwave/internal/geo"
)

// ISED's columns, by header name. ONLY the columns named here are ever read into the
// database. address_line and club_address (street addresses) are deliberately never
// used, and neither is club_name_2, which can name a person ("c/o ...").
const (
	caCall       = "callsign"
	caFirst      = "first_name"
	caSurname    = "surname"
	caCity       = "city"
	caProv       = "prov_cd"
	caPostal     = "postal_code"
	caClub       = "club_name"
	caClubCity   = "club_city"
	caClubProv   = "club_prov_cd"
	caClubPostal = "club_postal_code"
)

// caQuals are the qualification flag columns, in the order their letters are stored.
var caQuals = []struct{ col, letter string }{
	{"qual_a", "A"}, {"qual_b", "B"}, {"qual_c", "C"}, {"qual_d", "D"}, {"qual_e", "E"},
}

// realName returns "" for the placeholders ISED uses where it holds no name.
func realName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "first name unknown", "surname unknown", "unknown":
		return ""
	}
	return s
}

var caCallRE = regexp.MustCompile(`^[A-Z0-9]{3,8}$`)

// countReader counts the bytes read, to report progress through a zip entry.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// ImportCanada builds a database from ISED Canada's amateur call sign list (the zip that
// holds amateur_delim.txt). Like Import, it writes o.DBPath + ".building" and renames it
// into place only when finished, so a failed or cancelled build never damages the old database.
//
// Each record keeps the call sign, name, qualifications, city, province and the forward
// sortation area (the first three characters of the postal code). For a club the club's
// name and place are used and the person named on the row is kept as the contact.
func ImportCanada(ctx context.Context, o ImportOptions) (ImportStats, error) {
	report := func(phase string, frac float64) {
		if o.Progress != nil {
			o.Progress(Progress{Phase: phase, Fraction: frac})
		}
	}
	zr, err := zip.OpenReader(o.ZipPath)
	if err != nil {
		return ImportStats{}, fmt.Errorf("the downloaded file is not a valid zip archive: %w", err)
	}
	defer zr.Close()
	var entry *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), "amateur_delim.txt") {
			entry = f
			break
		}
	}
	if entry == nil {
		return ImportStats{}, errors.New("the downloaded file does not contain amateur_delim.txt: is it the ISED amateur call sign list?")
	}
	rc, err := entry.Open()
	if err != nil {
		return ImportStats{}, err
	}
	defer rc.Close()
	cr := &countReader{r: rc}
	// The file is plain text with ";" between fields. It is NOT quoted CSV: some names and addresses
	// contain a literal double quote (Robert "Bob"), which a CSV reader would take as the start of a
	// quoted field and swallow the following lines into it. So the lines are split by hand.
	sc := bufio.NewScanner(cr)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) == "" {
		return ImportStats{}, errors.New("the ISED file is empty or unreadable")
	}
	header := strings.Split(strings.TrimRight(sc.Text(), "\r"), ";")
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	need := []string{caCall, caFirst, caSurname, caCity, caProv, caPostal, caClub, caClubCity, caClubProv, caClubPostal}
	for _, q := range caQuals {
		need = append(need, q.col)
	}
	for _, n := range need {
		if _, ok := idx[n]; !ok {
			return ImportStats{}, fmt.Errorf("the ISED file has no %q column: its layout may have changed", n)
		}
	}

	tmp := o.DBPath + ".building"
	os.Remove(tmp)
	db, err := sql.Open("sqlite", tmp+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)&_pragma=temp_store(MEMORY)")
	if err != nil {
		return ImportStats{}, err
	}
	db.SetMaxOpenConns(1)
	ok := false
	defer func() {
		db.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err := db.Exec(createTables); err != nil {
		return ImportStats{}, err
	}

	report("Reading licenses", 0)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ImportStats{}, err
	}
	ins, err := tx.Prepare(`INSERT INTO license (call, name, club, class, status, trustee_name, city, state, zip, lat, lon, grid)
		VALUES (?, ?, ?, ?, 'A', ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return ImportStats{}, err
	}
	total := float64(entry.UncompressedSize64)
	n := 0
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		rec := strings.Split(line, ";")
		n++
		if n%5000 == 0 {
			if err := ctx.Err(); err != nil {
				tx.Rollback()
				return ImportStats{}, err
			}
			if total > 0 {
				report("Reading licenses", min(1, float64(cr.n)/total))
			}
		}
		get := func(col string) string {
			if i := idx[col]; i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		call := strings.ToUpper(get(caCall))
		if !caCallRE.MatchString(call) {
			continue
		}
		person := prettyName(strings.TrimSpace(realName(get(caFirst)) + " " + realName(get(caSurname))))
		name, trustee, city, prov, postal, club := person, "", get(caCity), get(caProv), get(caPostal), 0
		if c := get(caClub); c != "" { // a club: its name and place; the person on the row is its contact
			name, trustee, club = prettyName(c), person, 1
			city, prov, postal = get(caClubCity), get(caClubProv), get(caClubPostal)
		}
		if name == "" { // keep the call sign findable even when ISED holds no usable name
			name = "(name not published)"
		}
		var class string
		for _, q := range caQuals {
			if get(q.col) != "" {
				class += q.letter
			}
		}
		prov = strings.ToUpper(prov)
		if !provinceCodes[prov] {
			prov = ""
		}
		fsa := cleanFSA(postal)
		var lat, lon any
		grid := ""
		if la, lo, found := FSALocation(fsa); found {
			lat, lon, grid = la, lo, geo.Grid(la, lo, 3)
		}
		if _, err := ins.Exec(call, name, club, class, prettyName(trustee), prettyName(city), prov, fsa, lat, lon, grid); err != nil {
			tx.Rollback()
			return ImportStats{}, err
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		tx.Rollback()
		return ImportStats{}, fmt.Errorf("the ISED file could not be read: %w", err)
	}
	ins.Close()
	if err := tx.Commit(); err != nil {
		return ImportStats{}, err
	}

	report("Building the search index", 0)
	if err := ctx.Err(); err != nil {
		return ImportStats{}, err
	}
	if _, err := db.ExecContext(ctx, createIndexes); err != nil {
		return ImportStats{}, err
	}
	var st ImportStats
	st.ByStatus = map[string]int{}
	var records, withLoc, clubs int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(lat IS NOT NULL), 0), COALESCE(SUM(club), 0) FROM license`).Scan(&records, &withLoc, &clubs); err != nil {
		return ImportStats{}, err
	}
	st.Records, st.WithLocation, st.Clubs = records, withLoc, clubs
	st.ByStatus["A"] = records
	if records == 0 {
		return ImportStats{}, errors.New("the ISED file held no usable records")
	}

	meta := map[string]string{
		"schema_version":  strconv.Itoa(SchemaVersion),
		"country":         IDCA,
		"built_at":        time.Now().UTC().Format(time.RFC3339),
		"source_modified": o.SourceModified,
		"records":         strconv.Itoa(records),
		"with_location":   strconv.Itoa(withLoc),
		"source":          "ISED Canada, amateur call sign list (amateur_delim.zip)",
		"locations":       "approximate: the centre of each postal code's forward sortation area (GeoNames postal codes, CC BY 4.0)",
	}
	for k, v := range meta {
		if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
			return ImportStats{}, err
		}
	}
	if err := db.Close(); err != nil {
		return ImportStats{}, err
	}
	if o.BeforeReplace != nil {
		if err := o.BeforeReplace(); err != nil {
			return ImportStats{}, err
		}
	}
	if err := os.Rename(tmp, o.DBPath); err != nil {
		return ImportStats{}, fmt.Errorf("could not put the new database in place: %w", err)
	}
	ok = true
	report("Done", 1)
	return st, nil
}
