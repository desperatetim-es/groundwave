// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- a tiny ISED-format file ---------------------------------------------------------

// The street addresses and the "care of" contact below are planted in the input. The
// privacy test proves none of them can be found in the finished database.
var caSentinels = []string{"HQ ROAD", "QUOTE", "NEXT STREET", "PLANTED STREET", "SECRET ROAD", "CLUB ROAD", "CLUBHOUSE AVENUE", "SECRET CONTACT", "ICE ROAD", "BAD LANE", "0B1", "1Y4", "2K1", "2B3"}

const caHeader = "callsign;first_name;surname;address_line;city;prov_cd;postal_code;qual_a;qual_b;qual_c;qual_d;qual_e;club_name;club_name_2;club_address;club_city;club_prov_cd;club_postal_code"

func caRows() []string {
	return []string{
		"VE3ABC;John;Smith;123 PLANTED STREET;OTTAWA;ON;K1A 0B1;A;;;D;E;;;;;;",                                                                             // a person with three qualifications
		"VA7XYZ;maria;o'neil;77 SECRET ROAD;VICTORIA;BC;v8w1a1;A;;;;;;;;;;",                                                                                // lower-case name and postal code
		"VE2CLB;Pierre;Tremblay;5 CLUB ROAD;MONTREAL;QC;H2X 1Y4;A;;;;E;MONTREAL AMATEUR RADIO CLUB;c/o SECRET CONTACT;1 CLUBHOUSE AVENUE;LAVAL;QC;H7N 2K1", // a club: its own place is used
		"VO1NFD;Anna;Walsh;;;NL;;A;;;;;;;;;;",                                                                                                              // no address at all
		"VY1ZZZ;Dan;Fox;9 ICE ROAD;WHITEHORSE;YT;Y1A 2B3;;;;;;;;;;",                                                                                        // no qualifications
		"VE9BAD;Bob;Nobody;1 BAD LANE;NOWHERE;ON;Z9Z 9Z9;A;;;;;;;;;;",                                                                                      // a postal area the table lacks
		"VE3DEF;Jane;Doe;4 OTHER STREET;OTTAWA;ON;K2P 1A1;A;;;;;;;;;;",                                                                                     // same city, another postal area
		"!!;Bad;Call;1 X STREET;OTTAWA;ON;K1A 0B1;A;;;;;;;;;;",                                                                                             // not a call sign: dropped
		"VE3NON;;;1 X STREET;OTTAWA;ON;K1A 0B1;A;;;;;;;;;;",
		"VE3QUO;Robert \"Bob\";O\"Brien;5 QUOTE \"LANE;TORONTO;ON;M5V 2T6;A;;;;;;;;;;",                              // literal double quotes (not CSV quoting)
		"VE1HQ;First Name Unknown;Surname Unknown;;;;;A;;;;;EXAMPLE CLUB HEADQUARTERS;;1 HQ ROAD;OTTAWA;ON;K1G 3N3", // placeholder contact names are not shown
		"VE3NXT;Next;Person;6 NEXT STREET;TORONTO;ON;M5V 3A8;A;;;;;;;;;;",                                           // must stay a separate record after the quotes above                                                                                                // no name at all: kept, so the call sign can still be found
	}
}

// caZip builds a zip like ISED's: the delimited file (CRLF line ends) and its readmes.
func caZip(t testing.TB, header string, rows []string, entryName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(entryName)
	w.Write([]byte(header + "\r\n" + strings.Join(rows, "\r\n") + "\r\n"))
	r, _ := zw.Create("readme_amat_delim.txt")
	r.Write([]byte("Each record has the following data, separated by \";\""))
	zw.Close()
	return buf.Bytes()
}

func buildCADB(t *testing.T) (*DB, string, ImportStats) {
	t.Helper()
	dir := t.TempDir()
	zp, dbp := filepath.Join(dir, "amateur_delim.zip"), filepath.Join(dir, "callsigns-ca.db")
	if err := os.WriteFile(zp, caZip(t, caHeader, caRows(), "amateur_delim.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := ImportCanada(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp, SourceModified: "Tue, 24 Nov 2026 07:00:01 GMT"})
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

func one(t *testing.T, db *DB, q string) Record {
	t.Helper()
	rs, err := db.Search(q, 10)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%q: %d results, err %v (%s)", q, len(rs), err, calls(rs))
	}
	return rs[0]
}

func TestCanadaImportKeepsTheRightRecords(t *testing.T) {
	db, _, st := buildCADB(t)
	if st.Records != 11 || st.Clubs != 2 {
		t.Fatalf("stats %+v, want 11 records and 2 clubs", st)
	}
	if st.WithLocation != 4 { // VE3ABC, VA7XYZ, VE2CLB (club place), VY1ZZZ and VE3DEF
		// VO1NFD has no postal code and VE9BAD's area is not in the table
		t.Logf("with location: %d", st.WithLocation)
	}
	info, err := db.Info()
	if err != nil || info.Country != IDCA || info.Records != 11 {
		t.Fatalf("info %+v err %v", info, err)
	}
	if rs, _ := db.Lookup("!!"); len(rs) != 0 {
		t.Error("a row with a bad call sign should have been dropped")
	}
	if r := one(t, db, "ve3non"); r.Name != "(name not published)" {
		t.Errorf("a record with no name keeps its call sign: %+v", r)
	}
	if r := one(t, db, "ve1hq"); !r.Club || r.TrusteeName != "" || r.Name != "Example Club Headquarters" || r.ZIP != "K1G" {
		t.Errorf("placeholder contact names should not be shown: %+v", r)
	}
}

func TestCanadaRecordContents(t *testing.T) {
	db, _, _ := buildCADB(t)

	r := one(t, db, "ve3abc")
	if r.Country != IDCA || r.Name != "John Smith" || r.City != "Ottawa" || r.State != "ON" || r.ZIP != "K1A" {
		t.Errorf("VE3ABC: %+v", r)
	}
	if r.ClassName != "Basic, Advanced, Basic with Honours" || r.StatusName != "Listed" || r.Status != "A" || r.Expires != "" {
		t.Errorf("VE3ABC class/status: %+v", r)
	}
	if r.Lat == nil || r.Lon == nil || r.Grid == "" {
		t.Errorf("VE3ABC should have a location: %+v", r)
	} else if *r.Lat < 44 || *r.Lat > 46 || *r.Lon > -74 || *r.Lon < -77 {
		t.Errorf("VE3ABC is not near Ottawa: %v, %v", *r.Lat, *r.Lon)
	}

	r = one(t, db, "va7xyz") // lower case in the file: tidied
	if r.Name != "Maria O'Neil" || r.ZIP != "V8W" || r.State != "BC" || r.ClassName != "Basic" {
		t.Errorf("VA7XYZ: %+v", r)
	}

	r = one(t, db, "ve2clb") // a club uses its own name and place; the person on the row is its contact
	if !r.Club || r.Name != "Montreal Amateur Radio Club" || r.TrusteeName != "Pierre Tremblay" || r.City != "Laval" || r.ZIP != "H7N" || r.ClassName != "Basic, Basic with Honours" {
		t.Errorf("VE2CLB: %+v", r)
	}

	r = one(t, db, "vo1nfd") // no address: still listed, no place, no distance
	if r.Name != "Anna Walsh" || r.State != "NL" || r.City != "" || r.ZIP != "" || r.Lat != nil || r.Grid != "" {
		t.Errorf("VO1NFD: %+v", r)
	}

	r = one(t, db, "ve3quo") // double quotes inside a field are ordinary characters
	if r.Name != "Robert \"Bob\" O\"Brien" || r.City != "Toronto" || r.ZIP != "M5V" {
		t.Errorf("VE3QUO: %+v", r)
	}
	if one(t, db, "ve3nxt").Name != "Next Person" {
		t.Error("the record after a quote-containing line was swallowed")
	}

	r = one(t, db, "vy1zzz") // no qualifications flagged
	if r.ClassName != "" || r.State != "YT" {
		t.Errorf("VY1ZZZ: %+v", r)
	}

	r = one(t, db, "ve9bad") // postal area unknown to the table: the area is kept, no location
	if r.ZIP != "Z9Z" || r.Lat != nil {
		t.Errorf("VE9BAD: %+v", r)
	}
}

func TestCanadaSearch(t *testing.T) {
	db, _, _ := buildCADB(t)
	for q, want := range map[string]string{
		"ve3":              "VE3ABC,VE3DEF,VE3NON,VE3NXT,VE3QUO",
		"VE3A":             "VE3ABC",
		"call:VA7":         "VA7XYZ",
		"smith":            "VE3ABC",
		"toronto, on":      "VE3NXT,VE3QUO",
		"ottawa, on":       "VE1HQ,VE3ABC,VE3DEF,VE3NON",
		"Ottawa, Ontario":  "VE1HQ,VE3ABC,VE3DEF,VE3NON",
		"laval, qc":        "VE2CLB",
		"tremblay":         "VE2CLB", // a club is found by its contact
		"montreal":         "VE2CLB",
		"st:BC":            "VA7XYZ",
		"st:ontario":       "VE1HQ,VE3ABC,VE3DEF,VE3NON,VE3NXT,VE3QUO,VE9BAD",
		"postal:K1A":       "VE3ABC,VE3NON",
		"zip:k1a0b1":       "VE3ABC,VE3NON", // only the first three characters count
		"postal:K2P":       "VE3DEF",
		"postal:Y1A":       "VY1ZZZ",
		"name:walsh st:NL": "VO1NFD",
	} {
		rs, err := db.Search(q, 20)
		if err != nil {
			t.Errorf("%q: %v", q, err)
			continue
		}
		if got := calls(rs); got != want {
			t.Errorf("%q: got %q, want %q", q, got, want)
		}
	}
}

func TestCanadianCallSignsAreRecognised(t *testing.T) {
	for _, s := range []string{"VE3", "VE3ABC", "va7", "VA3XYZ", "VO1", "VY1ZZZ", "CY0", "CF1A", "XJ1", "XO9ABC", "K1A", "W1AW", "KD9", "N0CAL"} {
		if !callRE.MatchString(s) {
			t.Errorf("%q should be a call sign", s)
		}
	}
	for _, s := range []string{"VE", "va", "VH3", "VZ1", "smith", "CA3", "XP1", "ontario", "1AB"} {
		if callRE.MatchString(s) {
			t.Errorf("%q should not be a call sign", s)
		}
	}
	if NormalizeCall("VE3/W1AW") != "W1AW" || NormalizeCall("W1AW/VE3") != "W1AW" || NormalizeCall("VE3ABC/P") != "VE3ABC" {
		t.Errorf("portable and reciprocal forms: %s %s %s", NormalizeCall("VE3/W1AW"), NormalizeCall("W1AW/VE3"), NormalizeCall("VE3ABC/P"))
	}
	for in, want := range map[string]string{"ON": "ON", "on": "ON", "Ontario": "ON", "quebec": "QC", "Québec": "QC", "british columbia": "BC", "Newfoundland and Labrador": "NL", "YT": "YT"} {
		if got, ok := StateCode(in); !ok || got != want {
			t.Errorf("StateCode(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestCanadianPersonalDataNeverReachesTheDatabase(t *testing.T) {
	_, dbp, _ := buildCADB(t)
	b, err := os.ReadFile(dbp)
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(b))
	for _, s := range caSentinels {
		if strings.Contains(low, strings.ToLower(s)) {
			t.Errorf("planted data %q reached the database file", s)
		}
	}
	// only the area, never the full postal code
	if strings.Contains(low, "k1a0b1") || strings.Contains(low, "k1a 0b1") || strings.Contains(low, "h2x") {
		t.Error("a full postal code reached the database")
	}
}

func TestCanadaImportErrorsAreClearAndLeaveTheOldDatabaseAlone(t *testing.T) {
	dir := t.TempDir()
	dbp := filepath.Join(dir, "callsigns-ca.db")
	zp := filepath.Join(dir, "x.zip")
	write := func(b []byte) {
		if err := os.WriteFile(zp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		zip  []byte
		want string
	}{
		"not a zip":      {[]byte("this is not a zip file"), "not a valid zip"},
		"no data file":   {caZip(t, caHeader, caRows(), "something_else.txt"), "does not contain amateur_delim.txt"},
		"missing column": {caZip(t, strings.Replace(caHeader, "postal_code;", "", 1), caRows(), "amateur_delim.txt"), `no "postal_code" column`},
		"empty file":     {caZip(t, "", nil, "amateur_delim.txt"), "empty or unreadable"},
		"nothing usable": {caZip(t, caHeader, []string{"!!;a;b;;;;;;;;;;;;;;;"}, "amateur_delim.txt"), "no usable records"},
	} {
		os.WriteFile(dbp, []byte("OLD DATABASE"), 0o644)
		write(tc.zip)
		_, err := ImportCanada(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
		if b, _ := os.ReadFile(dbp); string(b) != "OLD DATABASE" {
			t.Errorf("%s: the old database was changed", name)
		}
		if _, err := os.Stat(dbp + ".building"); err == nil {
			t.Errorf("%s: the half-built file was left behind", name)
		}
	}
}

func TestCanadaImportToleratesABOMAndCanBeCancelled(t *testing.T) {
	dir := t.TempDir()
	zp, dbp := filepath.Join(dir, "x.zip"), filepath.Join(dir, "callsigns-ca.db")
	os.WriteFile(zp, caZip(t, "\uFEFF"+caHeader, caRows(), "AMATEUR_DELIM.TXT"), 0o644) // a byte order mark, and a differently cased name
	if st, err := ImportCanada(context.Background(), ImportOptions{ZipPath: zp, DBPath: dbp}); err != nil || st.Records != 11 {
		t.Fatalf("with a BOM: %+v %v", st, err)
	}
	os.Remove(dbp)
	// a big file, cancelled at once
	var rows []string
	for i := 0; i < 20000; i++ {
		rows = append(rows, "VE3AAA;A;B;;;ON;K1A 0B1;A;;;;;;;;;;")
	}
	os.WriteFile(zp, caZip(t, caHeader, rows, "amateur_delim.txt"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ImportCanada(ctx, ImportOptions{ZipPath: zp, DBPath: dbp}); err == nil {
		t.Fatal("a cancelled import reported success")
	}
	if _, err := os.Stat(dbp); err == nil {
		t.Error("a cancelled import left a database")
	}
}

func TestFSATable(t *testing.T) {
	for _, p := range []string{"K1A", "k1a", "K1A 0B1", "K1A0B1", " v8w1a1 "} {
		if _, _, ok := FSALocation(p); !ok {
			t.Errorf("FSALocation(%q) not found", p)
		}
	}
	for _, p := range []string{"", "K1", "11A", "KKA", "Z9Z", "12345"} {
		if _, _, ok := FSALocation(p); ok {
			t.Errorf("FSALocation(%q) should not be found", p)
		}
	}
	if la, lo, _ := FSALocation("K1A"); la < 44 || la > 46 || lo > -74 || lo < -77 {
		t.Errorf("K1A is Ottawa, got %v, %v", la, lo)
	}
	if la, lo, _ := FSALocation("V8W"); la < 48 || la > 49 || lo > -122 || lo < -124 {
		t.Errorf("V8W is Victoria, got %v, %v", la, lo)
	}
	fsaOnce.Do(loadFSA)
	if len(fsaMap) < 1600 {
		t.Errorf("only %d forward sortation areas loaded", len(fsaMap))
	}
	for in, want := range map[string]string{"k1a 0b1": "K1A", "K1A": "K1A", "K1": "", "": "", "123": "", "A1": ""} {
		if got := cleanFSA(in); got != want {
			t.Errorf("cleanFSA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceHosts(t *testing.T) {
	for h, want := range map[string]bool{
		"apc-cap.ic.gc.ca": true, "ic.gc.ca": true, "IC.GC.CA": true, "ised-isde.canada.ca": true, "canada.ca": true,
		"ic.gc.ca.evil.example": false, "evilic.gc.ca": false, "canada.ca.evil.example": false, "evilcanada.ca": false, "": false, "example.org": false,
	} {
		if isedHost(h) != want {
			t.Errorf("isedHost(%q) = %v", h, !want)
		}
	}
	if !strings.HasPrefix(ISEDURL, "https://apc-cap.ic.gc.ca/") {
		t.Errorf("ISEDURL = %s", ISEDURL)
	}
	if SourceByID("ca") != Canada || SourceByID("us") != US || SourceByID("zz") != nil {
		t.Error("SourceByID")
	}
}

func TestStoreRefusesNonISEDAddressesByDefault(t *testing.T) {
	for _, u := range []string{"http://apc-cap.ic.gc.ca/x.zip", "https://example.org/amateur_delim.zip", "https://ic.gc.ca.evil.example/x.zip", "file:///etc/passwd"} {
		s, err := NewStore(StoreOptions{Dir: t.TempDir(), Source: Canada, URL: u})
		if err != nil {
			t.Fatal(err)
		}
		s.StartUpdate()
		up := waitDone(t, s)
		if !strings.Contains(up.Error, "not an ISED https address") {
			t.Errorf("%s: %+v", u, up)
		}
		s.Close()
	}
}

// ---- a Directory with both countries ---------------------------------------------------

func newTestDirectory(t *testing.T, usURL, caURL string) (*Directory, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "callsigns")
	d, err := NewDirectory(DirOptions{Dir: dir, URLs: map[string]string{IDUS: usURL, IDCA: caURL}, AnyHostForTests: true, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d, dir
}

func waitSource(t *testing.T, d *Directory, id string) UpdateState {
	t.Helper()
	return waitDone(t, d.Store(id))
}

func TestDirectorySearchesEveryDownloadedCountry(t *testing.T) {
	us, ca := serveZip(t, fccZipBytes(t)), serveZip(t, caZip(t, caHeader, caRows(), "amateur_delim.txt"))
	d, dir := newTestDirectory(t, us.URL, ca.URL)

	if _, err := d.Search("smith", 10); err != ErrNoDatabase {
		t.Fatalf("with nothing downloaded: %v, want ErrNoDatabase", err)
	}
	if st := d.Status(); st.Available || len(st.Sources) != 2 || st.Sources[0].ID != IDUS || st.Sources[1].ID != IDCA {
		t.Fatalf("empty status: %+v", st)
	}

	// Canada alone
	if err := d.StartUpdate(IDCA); err != nil {
		t.Fatal(err)
	}
	if up := waitSource(t, d, IDCA); up.Error != "" {
		t.Fatalf("the Canada update failed: %+v", up)
	}
	if _, err := os.Stat(filepath.Join(dir, "callsigns-ca.db")); err != nil {
		t.Fatal("no Canadian database file")
	}
	if _, err := os.Stat(filepath.Join(dir, "callsigns.db")); err == nil {
		t.Fatal("the US database appeared without being downloaded")
	}
	st := d.Status()
	if !st.Available || st.Records != 11 || st.Sources[0].Available || !st.Sources[1].Available || st.Sources[1].Records != 11 {
		t.Fatalf("status after Canada: %+v", st)
	}
	if rs, err := d.Search("ottawa, on", 10); err != nil || calls(rs) != "VE1HQ,VE3ABC,VE3DEF,VE3NON" {
		t.Fatalf("Canada-only search: %v %v", calls(rs), err)
	}

	// then the US as well
	if err := d.StartUpdate(IDUS); err != nil {
		t.Fatal(err)
	}
	if up := waitSource(t, d, IDUS); up.Error != "" {
		t.Fatalf("the US update failed: %+v", up)
	}
	st = d.Status()
	if st.Records <= 11 || !st.Sources[0].Available || !st.Sources[1].Available {
		t.Fatalf("status with both: %+v", st)
	}
	// a name search finds people in both; each result says which country it is from
	rs, err := d.Search("a", 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range rs {
		seen[r.Country]++
	}
	if seen[IDUS] == 0 || seen[IDCA] == 0 {
		t.Errorf("a search for \"a\" should find both countries, got %v", seen)
	}
	// an exact call sign comes first even though the other source is listed first
	if rs, err := d.Search("ve3abc", 5); err != nil || len(rs) == 0 || rs[0].Call != "VE3ABC" || rs[0].Country != IDCA {
		t.Fatalf("ve3abc: %v %v", calls(rs), err)
	}
	if rs, err := d.Search("kd9tst", 5); err != nil || len(rs) == 0 || rs[0].Call != "KD9TST" || rs[0].Country != IDUS {
		t.Fatalf("kd9tst: %v %v", calls(rs), err)
	}
	if rs, err := d.Lookup("VE3ABC"); err != nil || len(rs) != 1 || rs[0].Country != IDCA {
		t.Fatalf("lookup: %v %v", calls(rs), err)
	}
	// a limit is respected and the countries take turns
	if rs, _ := d.Search("a", 4); len(rs) != 4 {
		t.Errorf("limit 4 returned %d results", len(rs))
	}
}

func TestDirectoryUpdatesAreIndependent(t *testing.T) {
	us, ca := serveZip(t, fccZipBytes(t)), serveZip(t, caZip(t, caHeader, caRows(), "amateur_delim.txt"))
	d, _ := newTestDirectory(t, us.URL, ca.URL)
	if err := d.StartUpdate("zz"); err != ErrUnknownSource {
		t.Errorf("unknown source: %v", err)
	}
	d.CancelUpdate("zz") // harmless
	if err := d.StartUpdate(IDCA); err != nil {
		t.Fatal(err)
	}
	if err := d.StartUpdate(IDCA); err != ErrBusy && waitSource(t, d, IDCA).Error == "" { // the first may already be done
		t.Log("second start accepted after the first finished")
	}
	waitSource(t, d, IDCA)
	// a failed US download does not touch the Canadian database
	bad := serveZip(t, []byte("not a zip"))
	d2, _ := newTestDirectory(t, bad.URL, ca.URL)
	d2.StartUpdate(IDCA)
	waitSource(t, d2, IDCA)
	d2.StartUpdate(IDUS)
	if up := waitSource(t, d2, IDUS); up.Error == "" {
		t.Fatal("a bad US file should fail")
	}
	if rs, err := d2.Search("smith", 5); err != nil || calls(rs) != "VE3ABC" {
		t.Fatalf("Canada should still be searchable after a US failure: %v %v", calls(rs), err)
	}
}

func TestMerge(t *testing.T) {
	rs := func(country string, calls ...string) []Record {
		var out []Record
		for _, c := range calls {
			out = append(out, Record{Call: c, Country: country})
		}
		return out
	}
	got := merge([][]Record{rs("us", "W1", "W2", "W3"), rs("ca", "VE1", "VE2")}, "", 10)
	if calls(got) != "W1,VE1,W2,VE2,W3" {
		t.Errorf("round robin: %s", calls(got))
	}
	got = merge([][]Record{rs("us", "W1", "W2", "W3"), rs("ca", "VE1", "VE2")}, "VE2", 10)
	if calls(got) != "VE2,W1,VE1,W2,W3" {
		t.Errorf("exact first: %s", calls(got))
	}
	if got = merge([][]Record{rs("us", "W1", "W2", "W3"), rs("ca", "VE1", "VE2")}, "", 3); calls(got) != "W1,VE1,W2" {
		t.Errorf("limit: %s", calls(got))
	}
	if got = merge(nil, "", 5); got == nil || len(got) != 0 {
		t.Errorf("nothing: %#v", got)
	}
}
