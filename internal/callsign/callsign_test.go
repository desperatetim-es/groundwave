// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- a tiny FCC-format file ------------------------------------------------------

// The personal-data sentinels below are planted in the input. The privacy test
// proves none of them can be found in the finished database.
const (
	phoneSentinel  = "PHONE-SENTINEL-5550123"
	faxSentinel    = "FAX-SENTINEL-5550124"
	emailSentinel  = "email-sentinel@example.invalid"
	streetSentinel = "STREET-SENTINEL 42 ELM LANE"
	poBoxSentinel  = "POBOX-SENTINEL-77"
	frnSentinel    = "FRN-SENTINEL-0001234567"
)

func hd(usi int, call, status, service, grant, expire string) string {
	f := make([]string, 59)
	f[0], f[1], f[4], f[5], f[6], f[7], f[8] = "HD", strconv.Itoa(usi), call, status, service, grant, expire
	return strings.Join(f, "|")
}

func en(usi int, call, etype, entity, first, mi, last, suffix, city, state, zip string) string {
	f := make([]string, 30)
	f[0], f[1], f[4], f[5], f[7], f[8], f[9], f[10], f[11] = "EN", strconv.Itoa(usi), call, etype, entity, first, mi, last, suffix
	f[12], f[13], f[14], f[15] = phoneSentinel, faxSentinel, emailSentinel, streetSentinel
	f[16], f[17], f[18], f[19], f[22] = city, state, zip, poBoxSentinel, frnSentinel
	return strings.Join(f, "|")
}

func am(usi int, call, class, trusteeCall, prev, trusteeName string) string {
	f := make([]string, 18)
	f[0], f[1], f[4], f[5], f[8], f[15], f[17] = "AM", strconv.Itoa(usi), call, class, trusteeCall, prev, trusteeName
	return strings.Join(f, "|")
}

var testNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// testFiles returns the three tables for a small, deliberately awkward data set.
func testFiles() (hds, ens, ams []string) {
	hds = []string{
		hd(1, "W1AW", "A", "HA", "01/02/2020", "01/02/2030"),    // a club
		hd(2, "KD9TST", "A", "HA", "10/11/2023", "11/23/2033"),  // a person, ZIP+4, previous call
		hd(3, "K9EXP", "E", "HA", "01/01/2010", "01/01/2026"),   // expired within the 2-year window: kept
		hd(4, "N0OLD", "E", "HA", "01/01/2000", "03/01/2019"),   // expired long ago: dropped
		hd(5, "K0CAN", "C", "HA", "01/01/2000", "03/01/2030"),   // cancelled: dropped
		hd(6, "KB1GMR", "A", "GM", "01/01/2000", "03/01/2030"),  // not an amateur service: dropped
		hd(7, "W2NOEN", "A", "HA", "01/01/2020", "03/01/2030"),  // no licensee record: dropped
		hd(8, "W3ZIP", "A", "HA", "01/01/2020", "03/01/2030"),   // a ZIP the Census table lacks
		hd(9, "W9DUP", "E", "HA", "01/01/2005", "06/01/2025"),   // an old holder of a re-issued call
		hd(10, "W9DUP", "A", "HV", "05/05/2026", "05/05/2036"),  // its current holder (vanity)
		hd(11, "N5MIX", "A", "HA", "01/01/2020", "03/01/2030"),  // already mixed case
		hd(12, "W4CS", "A", "HA", "01/01/2020", "03/01/2030"),   // licensee record of another type
		hd(13, "K5LATE", "E", "HA", "01/01/2000", "11/01/2024"), // expired 2024-11-01: before the cutoff (2024-11-12)
		hd(14, "KE7LYN", "A", "HA", "01/01/2020", "03/01/2030"), // name with vowel-less words
		hd(15, "N6ODD", "A", "HA", "01/01/2020", "xx/yy/zzzz"),  // a bad date
	}
	ens = []string{
		en(1, "W1AW", "L", "ARRL HQ OPERATORS CLUB", "", "", "", "", "NEWINGTON", "CT", "06111"),
		en(2, "KD9TST", "L", "MCDONALD, ALICE B JR", "ALICE", "B", "MCDONALD", "JR", "CARY", "il", "60013-1234"),
		en(3, "K9EXP", "L", "EXPIRED, ED", "ED", "", "EXPIRED", "", "ST. LOUIS", "MO", "63101"),
		en(4, "N0OLD", "L", "OLD, OTTO", "OTTO", "", "OLD", "", "OMAHA", "NE", "68102"),
		en(5, "K0CAN", "L", "CANCELLED, CAL", "CAL", "", "CANCELLED", "", "DENVER", "CO", "80202"),
		en(6, "KB1GMR", "L", "GMRS, GARY", "GARY", "", "GMRS", "", "BOSTON", "MA", "02108"),
		en(8, "W3ZIP", "L", "ZIPLESS, ZED", "ZED", "", "ZIPLESS", "", "NOWHERE", "PA", "99999"),
		en(9, "W9DUP", "L", "FIRST, FRED", "FRED", "", "FIRST", "", "PEORIA", "IL", "61602"),
		en(10, "W9DUP", "L", "SECOND, SALLY", "SALLY", "", "SECOND", "", "PEORIA", "IL", "61602"),
		en(11, "N5MIX", "L", "Already Mixed", "Already", "", "Mixed", "", "Austin", "TX", "78701"),
		en(12, "W4CS", "CS", "CONTACT, CARL", "CARL", "", "CONTACT", "", "ATLANTA", "GA", "30301"),
		en(13, "K5LATE", "L", "LATE, LUCY", "LUCY", "", "LATE", "", "TULSA", "OK", "74101"),
		en(14, "KE7LYN", "L", "LYNN, GLYNN", "GLYNN", "", "LYNN", "", "SEATTLE", "WA", "98101"),
		en(15, "N6ODD", "L", "ODD, OLLIE", "OLLIE", "", "ODD", "", "FRESNO", "CA", "93650"),
		"EN|short|line",
		"XX|not|an|en|record",
		"",
	}
	ams = []string{
		am(1, "W1AW", "", "K1ZZ", "", "SMITH, JOHN Q"),
		am(2, "KD9TST", "E", "", "N9OLD", ""),
		am(3, "K9EXP", "G", "", "", ""),
		am(8, "W3ZIP", "T", "", "", ""),
		am(10, "W9DUP", "G", "", "", ""),
		am(11, "N5MIX", "A", "", "", ""),
		"AM|bad",
	}
	return
}

// writeZip builds a zip of the three tables (plus an unrelated one), with CRLF line endings
// like the real files.
func writeZip(t testing.TB, path string, hds, ens, ams []string, omit ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	skip := map[string]bool{}
	for _, o := range omit {
		skip[o] = true
	}
	for name, lines := range map[string][]string{"HD.dat": hds, "EN.dat": ens, "AM.dat": ams, "CO.dat": {"CO|1|unrelated"}} {
		if skip[name] {
			continue
		}
		w, _ := zw.Create(name)
		w.Write([]byte(strings.Join(lines, "\r\n") + "\r\n"))
	}
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildTestDB imports the standard data set and opens the result.
func buildTestDB(t *testing.T) (*DB, string, ImportStats) {
	t.Helper()
	dir := t.TempDir()
	zp, dbp := filepath.Join(dir, "l_amat.zip"), filepath.Join(dir, "callsigns.db")
	h, e, a := testFiles()
	writeZip(t, zp, h, e, a)
	st, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp, SourceModified: "Sun, 08 Nov 2026 14:08:58 GMT", Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dbp, st
}

// ---- the import -------------------------------------------------------------------

func TestImportKeepsTheRightLicenses(t *testing.T) {
	db, _, st := buildTestDB(t)
	// kept: W1AW, KD9TST, K9EXP, W3ZIP, W9DUP (x2), N5MIX, KE7LYN, N6ODD = 9
	if st.Records != 9 || st.ByStatus["A"] != 7 || st.ByStatus["E"] != 2 || st.Clubs != 1 {
		t.Errorf("stats = %+v", st)
	}
	if st.WithLocation != 8 { // every kept license has a ZIP the Census table knows, except W3ZIP (99999)
		t.Errorf("with location = %d", st.WithLocation)
	}
	for _, gone := range []string{"N0OLD", "K0CAN", "KB1GMR", "W2NOEN", "W4CS", "K5LATE"} {
		if r, _ := db.Lookup(gone); len(r) != 0 {
			t.Errorf("%s should not be in the database: %+v", gone, r)
		}
	}
	info, err := db.Info()
	if err != nil || info.Records != 9 || info.SourceModified == "" || time.Since(info.BuiltAt) > time.Minute {
		t.Errorf("info = %+v %v", info, err)
	}
}

func TestPersonalDataThatIsNotNeededNeverReachesTheDatabase(t *testing.T) {
	_, dbp, _ := buildTestDB(t)
	raw, err := os.ReadFile(dbp)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{phoneSentinel, faxSentinel, emailSentinel, streetSentinel, poBoxSentinel, frnSentinel, "ELM LANE", "5550123"} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("the database file contains %q", s)
		}
	}
	// and the schema has no column that could hold them
	db, _, _ := buildTestDB(t)
	rows, err := db.db.Query(`SELECT name FROM pragma_table_info('license')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		rows.Scan(&col)
		for _, bad := range []string{"street", "address", "phone", "email", "fax", "po_box", "frn", "ssn"} {
			if strings.Contains(col, bad) {
				t.Errorf("column %q looks like it holds personal data", col)
			}
		}
	}
}

func TestRecordContents(t *testing.T) {
	db, _, _ := buildTestDB(t)

	r, _ := db.Lookup("KD9TST")
	if len(r) != 1 {
		t.Fatalf("KD9TST: %+v", r)
	}
	k := r[0]
	if k.Name != "Alice B McDonald JR" && k.Name != "Alice B McDonald Jr" {
		t.Errorf("name = %q", k.Name)
	}
	if k.City != "Cary" || k.State != "IL" || k.ZIP != "60013" || k.Class != "E" || k.ClassName != "Amateur Extra" ||
		k.Status != "A" || k.StatusName != "Active" || k.Granted != "2023-10-11" || k.Expires != "2033-11-23" || k.PrevCall != "N9OLD" || k.Club {
		t.Errorf("record = %+v", k)
	}
	if k.Grid != "EN52vf" || k.Lat == nil || k.Lon == nil || *k.Lat < 42.1 || *k.Lat > 42.4 || *k.Lon > -88.1 || *k.Lon < -88.4 {
		t.Errorf("location: grid %q lat %v lon %v", k.Grid, k.Lat, k.Lon)
	}

	r, _ = db.Lookup("W1AW")
	c := r[0]
	if !c.Club || c.Name != "ARRL HQ Operators Club" || c.TrusteeCall != "K1ZZ" || c.TrusteeName != "Smith, John Q" || c.Class != "" || c.ClassName != "" {
		t.Errorf("club = %+v", c)
	}
	if c.City != "Newington" || c.Grid == "" {
		t.Errorf("club place = %+v", c)
	}

	r, _ = db.Lookup("W3ZIP")
	if len(r) != 1 || r[0].Lat != nil || r[0].Lon != nil || r[0].Grid != "" || r[0].ZIP != "99999" {
		t.Errorf("a license in a ZIP with no location should still be found: %+v", r)
	}
	if r, _ := db.Lookup("N5MIX"); r[0].Name != "Already Mixed" || r[0].City != "Austin" {
		t.Errorf("mixed-case text must be left alone: %+v", r[0])
	}
	if r, _ := db.Lookup("KE7LYN"); r[0].Name != "Glynn Lynn" {
		t.Errorf("vowel-less real names must not become initials: %q", r[0].Name)
	}
	if r, _ := db.Lookup("K9EXP"); r[0].Status != "E" || r[0].StatusName != "Expired" || r[0].City != "St. Louis" {
		t.Errorf("expired record = %+v", r[0])
	}
	if r, _ := db.Lookup("N6ODD"); len(r) != 1 || r[0].Expires != "" {
		t.Errorf("a bad date should become empty, not fail the import: %+v", r)
	}
}

func TestReissuedCallsListTheCurrentHolderFirst(t *testing.T) {
	db, _, _ := buildTestDB(t)
	r, _ := db.Lookup("W9DUP")
	if len(r) != 2 || r[0].Status != "A" || r[0].Name != "Sally Second" || r[1].Status != "E" {
		t.Errorf("W9DUP = %+v", r)
	}
	if res, _ := db.Search("w9dup", 10); len(res) != 2 || res[0].Status != "A" {
		t.Errorf("search order: %+v", res)
	}
}

func TestExpiredWindowIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "l_amat.zip")
	h, e, a := testFiles()
	writeZip(t, zp, h, e, a)
	now := func() time.Time { return testNow }

	all, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: filepath.Join(dir, "all.db"), KeepExpiredYears: -1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if all.ByStatus["E"] != 4 { // K9EXP, N0OLD, W9DUP (old), K5LATE
		t.Errorf("keeping every expired license: %+v", all.ByStatus)
	}
	short, _ := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: filepath.Join(dir, "short.db"), KeepExpiredYears: 1, Now: now})
	if short.ByStatus["E"] != 1 { // only K9EXP (2026-01) is within a year; W9DUP's old license expired 2025-06
		t.Errorf("one-year window: %+v", short.ByStatus)
	}
	only, _ := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: filepath.Join(dir, "a.db"), KeepStatuses: "A", Now: now})
	if only.ByStatus["E"] != 0 || only.ByStatus["A"] != 7 {
		t.Errorf("active only: %+v", only.ByStatus)
	}
}

func TestImportErrorsAreClearAndLeaveTheOldDatabaseAlone(t *testing.T) {
	db, dbp, _ := buildTestDB(t)
	db.Close()
	before, _ := os.ReadFile(dbp)
	dir := t.TempDir()

	notZip := filepath.Join(dir, "bad.zip")
	os.WriteFile(notZip, []byte("this is not a zip file"), 0o644)
	if _, err := Import(context.Background(), ImportOptions{ZipPath: notZip, DBPath: dbp}); err == nil || !strings.Contains(err.Error(), "not a valid zip") {
		t.Errorf("not a zip: %v", err)
	}
	h, e, a := testFiles()
	missing := filepath.Join(dir, "missing.zip")
	writeZip(t, missing, h, e, a, "AM.dat")
	if _, err := Import(context.Background(), ImportOptions{ZipPath: missing, DBPath: dbp}); err == nil || !strings.Contains(err.Error(), "AM.dat") {
		t.Errorf("missing table: %v", err)
	}
	if _, err := Import(context.Background(), ImportOptions{ZipPath: filepath.Join(dir, "nope.zip"), DBPath: dbp}); err == nil {
		t.Error("a missing file was accepted")
	}
	// a cancelled build
	good := filepath.Join(dir, "good.zip")
	writeZip(t, good, h, e, a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Import(ctx, ImportOptions{ZipPath: good, DBPath: dbp}); err == nil {
		t.Error("a cancelled build reported success")
	}
	after, _ := os.ReadFile(dbp)
	if !bytes.Equal(before, after) {
		t.Error("a failed build changed the existing database")
	}
	if _, err := os.Stat(dbp + ".building"); err == nil {
		t.Error("a failed build left its temporary file behind")
	}
}

func TestImportReplacesTheOldDatabase(t *testing.T) {
	old, dbp, _ := buildTestDB(t)
	dir := t.TempDir()
	zp := filepath.Join(dir, "l_amat.zip")
	writeZip(t, zp, []string{hd(99, "W0NEW", "A", "HA", "01/01/2026", "01/01/2036")}, []string{en(99, "W0NEW", "L", "NEW, NORA", "NORA", "", "NEW", "", "CARY", "IL", "60013")}, nil)
	// AM.dat is present but empty
	closed := false
	if _, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp, Now: func() time.Time { return testNow },
		BeforeReplace: func() error { closed = true; return old.Close() }}); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Error("the hook was not called before the replacement")
	}
	db, err := Open(dbp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if r, _ := db.Lookup("W0NEW"); len(r) != 1 {
		t.Error("the new data is not there")
	}
	if r, _ := db.Lookup("W1AW"); len(r) != 0 {
		t.Error("the old data is still there")
	}
}

func TestProgressIsReportedInOrder(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "l_amat.zip")
	h, e, a := testFiles()
	writeZip(t, zp, h, e, a)
	var phases []string
	_, err := Import(context.Background(), ImportOptions{ZipPath: zp, DBPath: filepath.Join(dir, "x.db"), Now: func() time.Time { return testNow },
		Progress: func(p Progress) {
			if p.Fraction < 0 || p.Fraction > 1 {
				t.Errorf("fraction %v out of range", p.Fraction)
			}
			if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
				phases = append(phases, p.Phase)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Reading licenses", "Reading names and places", "Reading operator classes", "Building the search index", "Done"}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Errorf("phases = %v", phases)
	}
}

func TestOpenRefusesBadFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "none.db")); err != ErrNoDatabase {
		t.Errorf("missing file: %v", err)
	}
	junk := filepath.Join(dir, "junk.db")
	os.WriteFile(junk, []byte(strings.Repeat("not a database ", 100)), 0o644)
	if _, err := Open(junk); err == nil {
		t.Error("a junk file was opened")
	}
}

// ---- searching --------------------------------------------------------------------

func calls(rs []Record) string {
	var c []string
	for _, r := range rs {
		c = append(c, r.Call)
	}
	return strings.Join(c, ",")
}

func TestSearch(t *testing.T) {
	db, _, _ := buildTestDB(t)
	for _, c := range []struct{ q, want string }{
		{"W1AW", "W1AW"},
		{"w1aw", "W1AW"},
		{"w1aw/4", "W1AW"},
		{"W1AW/P", "W1AW"},
		{"VE3/W1AW", "W1AW"},
		{"kd9", "KD9TST"},
		{"call:K9", "K9EXP"},
		{"w9", "W9DUP,W9DUP"},
		{"alice", "KD9TST"},
		{"alice mcdonald", "KD9TST"},
		{"mcdon", "KD9TST"},
		{"mcdonald alice", "KD9TST"},
		{"cary", "KD9TST"},
		{"city:cary", "KD9TST"},
		{"name:cary", ""}, // "Cary" is a city here, not a name
		{"name:alice city:cary", "KD9TST"},
		{"60013", "KD9TST"},
		{"60013-1234", "KD9TST"},
		{"zip:60013", "KD9TST"},
		{"st:CT", "W1AW"},
		{"state:il", "KD9TST,W9DUP,W9DUP"},
		{"st:IL peoria", "W9DUP,W9DUP"},
		{"arrl", "W1AW"},
		{"operators club", "W1AW"},
		{"smith", "W1AW"}, // the club's trustee
		{"nosuchname", ""},
		{"w1aw alice", ""}, // both must match
		{"lynn", "KE7LYN"},
		{"st. louis", "K9EXP"},
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
	// a callsign prefix lists exact matches first
	if got, _ := db.Search("w1aw", 10); got[0].Call != "W1AW" {
		t.Error("exact match not first")
	}
	// results are never nil, so the JSON is [] not null
	if got, err := db.Search("nosuchname", 10); err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty result: %v %v", got, err)
	}
	// limits
	if got, _ := db.Search("st:IL", 1); len(got) != 1 {
		t.Errorf("limit 1 returned %d", len(got))
	}
	if got, _ := db.Search("st:IL", 100000); len(got) != 3 {
		t.Errorf("a huge limit: %d", len(got))
	}
}

func TestSearchRejectsBadQueriesAndIsSafeAgainstInjection(t *testing.T) {
	db, _, _ := buildTestDB(t)
	for _, q := range []string{"", "   ", "st:I", "st:ILL", "zip:abc"} {
		if _, err := db.Search(q, 10); err == nil {
			t.Errorf("%q accepted", q)
		}
	}
	hostile := []string{
		`"`, `""`, `*`, `NEAR(a b)`, `name:"`, `foo" OR "bar`, `a AND`, `{name}: x`, `-x`, `^x`, `x*y`,
		`'; DROP TABLE license; --`, `1; DELETE FROM license`, `name:'`, `call:'; DROP TABLE license`, "w1aw\x00", "(((", `%`, `_`,
		strings.Repeat("a ", 100), strings.Repeat("W1AW/", 50),
	}
	for _, q := range hostile {
		db.Search(q, 5) // may legitimately return an error or nothing: it must not panic or damage anything
	}
	if r, err := db.Lookup("W1AW"); err != nil || len(r) != 1 {
		t.Fatalf("the database was damaged by a hostile query: %v %v", r, err)
	}
	if _, err := db.Lookup(""); err == nil {
		t.Error("an empty lookup was accepted")
	}
	// the connection is read-only: even a direct write must fail
	if _, err := db.db.Exec(`DELETE FROM license`); err == nil {
		t.Error("the search connection can write")
	}
}

// ---- helpers ----------------------------------------------------------------------

func TestNormalizeCall(t *testing.T) {
	for in, want := range map[string]string{
		"w1aw": "W1AW", " KD9TST ": "KD9TST", "W1AW/4": "W1AW", "W1AW/P": "W1AW", "W1AW/MM": "W1AW",
		"VE3/W1AW": "W1AW", "K9XYZ/QRP": "K9XYZ", "W1AW/4/P": "W1AW", "": "", "/": "",
	} {
		if got := NormalizeCall(in); got != want {
			t.Errorf("NormalizeCall(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrettyName(t *testing.T) {
	for in, want := range map[string]string{
		"MCDONALD, JOHN A":       "McDonald, John A",
		"O'BRIEN":                "O'Brien",
		"SMITH-JONES":            "Smith-Jones",
		"ARRL HQ OPERATORS CLUB": "ARRL HQ Operators Club",
		"ST. LOUIS":              "St. Louis",
		"WNY ARC":                "WNY ARC",
		"NY RADIO CLUB":          "NY Radio Club",
		"JOHN SMITH III":         "John Smith III",
		"LYNN, GLYNN":            "Lynn, Glynn",
		"Already Mixed":          "Already Mixed",
		"  extra   spaces  ":     "Extra Spaces", // all one case, so it is fixed
		"honolulu":               "Honolulu",
		"mcdonald":               "McDonald",
		"van der Berg":           "van der Berg", // a deliberate mix is left alone
		"CAry":                   "CAry",
		"":                       "",
		"VAN DER BERG":           "Van Der Berg",
		"CAMP PENDLETON ARS INC": "Camp Pendleton ARS Inc",
		"DR. JANE DOE":           "Dr. Jane Doe",
		"RAMSEY COUNTY RACES":    "Ramsey County RACES",
	} {
		if got := prettyName(in); got != want {
			t.Errorf("prettyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsoDateAndNames(t *testing.T) {
	for in, want := range map[string]string{"10/11/2023": "2023-10-11", "01/02/2030": "2030-01-02", "": "", "2023-10-11": "", "1/2/2023": "", "xx/yy/zzzz": "", "10-11-2023": ""} {
		if got := isoDate(in); got != want {
			t.Errorf("isoDate(%q) = %q, want %q", in, got, want)
		}
	}
	if ClassName("e") != "Amateur Extra" || ClassName("") != "" || ClassName("Q") != "" || StatusName("A") != "Active" || StatusName("Z") != "Z" {
		t.Error("class or status names wrong")
	}
	if personName("A", "B", "C", "JR", "X") != "A B C JR" && personName("A", "B", "C", "JR", "X") != "A B C Jr" {
		t.Errorf("personName = %q", personName("A", "B", "C", "JR", "X"))
	}
	if personName("", "", "", "", "SOME CLUB") != "Some Club" || personName("ONLY", "", "", "", "ONLY") != "Only" {
		t.Error("entity names wrong")
	}
}

func TestZIPLocation(t *testing.T) {
	if lat, lon, ok := ZIPLocation("60013"); !ok || lat < 42.1 || lat > 42.4 || lon > -88.1 || lon < -88.4 {
		t.Errorf("60013 = %v, %v, %v", lat, lon, ok)
	}
	if _, _, ok := ZIPLocation("60013-1234"); !ok {
		t.Error("ZIP+4 not understood")
	}
	if _, _, ok := ZIPLocation("600131234"); !ok {
		t.Error("nine-digit ZIP not understood")
	}
	for _, bad := range []string{"", "1234", "abcde", "99999", "0000x"} {
		if _, _, ok := ZIPLocation(bad); ok {
			t.Errorf("%q gave a location", bad)
		}
	}
	// Alaska, Hawaii and Puerto Rico are covered too
	for _, z := range []string{"99501", "96813", "00901"} {
		if _, _, ok := ZIPLocation(z); !ok {
			t.Errorf("%s has no location", z)
		}
	}
}
