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
	"strconv"
	"strings"
	"time"

	"github.com/desperatetim-es/groundwave/internal/geo"
)

// Progress reports how far a build has got.
type Progress struct {
	Phase    string  `json:"phase"`
	Fraction float64 `json:"fraction"` // 0 to 1 within the phase
}

// ImportOptions describes one build.
type ImportOptions struct {
	ZipPath          string           // the FCC's l_amat.zip
	DBPath           string           // where to write the new database (replaced if present)
	SourceModified   string           // when the FCC published the file, recorded for display
	KeepStatuses     string           // license status codes to keep (default "AE")
	KeepExpiredYears int              // expired licenses are kept this long after they expire (default 2, the FCC renewal grace period; negative keeps all)
	Now              func() time.Time // for tests
	Progress         func(Progress)
	// BeforeReplace runs just before the finished database replaces the old file. On Windows a file
	// cannot be replaced while it is open, so a caller that has the old database open closes it here.
	BeforeReplace func() error
}

// ImportStats says what was built.
type ImportStats struct {
	Records      int            `json:"records"`
	WithLocation int            `json:"with_location"`
	Clubs        int            `json:"clubs"`
	ByStatus     map[string]int `json:"by_status"`
}

// FCC table columns (zero-based), from the FCC's public access database
// definitions. Only the fields listed here are ever read into the database.
const (
	hdUSI, hdCall, hdStatus, hdService, hdGrant, hdExpire = 1, 4, 5, 6, 7, 8

	enUSI, enEntityType, enName, enFirst, enMI, enLast, enSuffix = 1, 5, 7, 8, 9, 10, 11
	enCity, enState, enZIP                                       = 16, 17, 18
	// enStreet (15), enPOBox (19), phone (12), fax (13) and email (14) are
	// intentionally not used anywhere.

	amUSI, amClass, amTrusteeCall, amPrevCall, amTrusteeName = 1, 5, 8, 15, 17
)

// Import builds a database from the FCC's amateur license file. It writes to
// o.DBPath + ".building" and renames it into place only when it has finished,
// so a failed or cancelled build never damages the existing database.
func Import(ctx context.Context, o ImportOptions) (ImportStats, error) {
	keep := o.KeepStatuses
	if keep == "" {
		keep = "AE"
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	years := o.KeepExpiredYears
	if years == 0 {
		years = 2
	}
	expiredCutoff := ""
	if years > 0 {
		expiredCutoff = now().AddDate(-years, 0, 0).Format("2006-01-02")
	}
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
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, need := range []string{"HD.dat", "EN.dat", "AM.dat"} {
		if files[need] == nil {
			return ImportStats{}, fmt.Errorf("the downloaded file does not contain %s: is it the FCC amateur license file (l_amat.zip)?", need)
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

	// Pass 1: license headers (call sign, status, dates). Everything else is
	// filled in by key in the next passes, so memory use stays small.
	report("Reading licenses", 0)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ImportStats{}, err
	}
	ins, err := tx.Prepare(`INSERT OR REPLACE INTO license (usi, call, status, granted, expires) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return ImportStats{}, err
	}
	err = readTable(ctx, files["HD.dat"], func(f []string, frac float64) error {
		if len(f) <= hdExpire || f[0] != "HD" {
			return nil
		}
		usi, err := strconv.ParseInt(f[hdUSI], 10, 64)
		if err != nil || (f[hdService] != "HA" && f[hdService] != "HV") || !strings.Contains(keep, f[hdStatus]) || f[hdStatus] == "" {
			return nil
		}
		call := strings.ToUpper(strings.TrimSpace(f[hdCall]))
		if call == "" {
			return nil
		}
		expires := isoDate(f[hdExpire])
		if f[hdStatus] == "E" && expiredCutoff != "" && expires != "" && expires < expiredCutoff {
			return nil // expired long ago: past the renewal grace period
		}
		_, err = ins.Exec(usi, call, f[hdStatus], isoDate(f[hdGrant]), expires)
		return err
	}, report, "Reading licenses")
	ins.Close()
	if err != nil {
		tx.Rollback()
		return ImportStats{}, err
	}
	if err := tx.Commit(); err != nil {
		return ImportStats{}, err
	}

	// Pass 2: names and home city, state and ZIP (never the street address).
	report("Reading names and places", 0)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return ImportStats{}, err
	}
	upd, err := tx.Prepare(`UPDATE license SET name = ?, city = ?, state = ?, zip = ?, lat = ?, lon = ?, grid = ? WHERE usi = ? AND name = ''`)
	if err != nil {
		return ImportStats{}, err
	}
	err = readTable(ctx, files["EN.dat"], func(f []string, frac float64) error {
		if len(f) <= enZIP || f[0] != "EN" {
			return nil
		}
		if t := f[enEntityType]; t != "L" && t != "CL" { // the licensee, not a contact or agent
			return nil
		}
		usi, err := strconv.ParseInt(f[enUSI], 10, 64)
		if err != nil {
			return nil
		}
		name := personName(f[enFirst], f[enMI], f[enLast], f[enSuffix], f[enName])
		if name == "" {
			return nil
		}
		zip := cleanZIP(f[enZIP])
		var lat, lon any
		grid := ""
		if la, lo, found := ZIPLocation(zip); found {
			lat, lon, grid = la, lo, geo.Grid(la, lo, 3)
		}
		_, err = upd.Exec(name, prettyName(f[enCity]), strings.ToUpper(strings.TrimSpace(f[enState])), zip, lat, lon, grid, usi)
		return err
	}, report, "Reading names and places")
	upd.Close()
	if err != nil {
		tx.Rollback()
		return ImportStats{}, err
	}
	if err := tx.Commit(); err != nil {
		return ImportStats{}, err
	}
	// A license with no licensee record cannot be shown, so drop it.
	if _, err := db.Exec(`DELETE FROM license WHERE name = ''`); err != nil {
		return ImportStats{}, err
	}

	// Pass 3: operator class, previous call and club trustee.
	report("Reading operator classes", 0)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return ImportStats{}, err
	}
	upd, err = tx.Prepare(`UPDATE license SET class = ?, prev_call = ?, trustee_call = ?, trustee_name = ?, club = ? WHERE usi = ?`)
	if err != nil {
		return ImportStats{}, err
	}
	err = readTable(ctx, files["AM.dat"], func(f []string, frac float64) error {
		if len(f) <= amTrusteeName || f[0] != "AM" {
			return nil
		}
		usi, err := strconv.ParseInt(f[amUSI], 10, 64)
		if err != nil {
			return nil
		}
		trusteeCall := strings.ToUpper(strings.TrimSpace(f[amTrusteeCall]))
		club := 0
		if trusteeCall != "" {
			club = 1
		}
		_, err = upd.Exec(strings.ToUpper(strings.TrimSpace(f[amClass])), strings.ToUpper(strings.TrimSpace(f[amPrevCall])),
			trusteeCall, prettyName(f[amTrusteeName]), club, usi)
		return err
	}, report, "Reading operator classes")
	upd.Close()
	if err != nil {
		tx.Rollback()
		return ImportStats{}, err
	}
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
	rows, err := db.Query(`SELECT status, COUNT(*), SUM(lat IS NOT NULL), SUM(club) FROM license GROUP BY status`)
	if err != nil {
		return ImportStats{}, err
	}
	for rows.Next() {
		var s string
		var n, loc, clubs int
		if err := rows.Scan(&s, &n, &loc, &clubs); err != nil {
			rows.Close()
			return ImportStats{}, err
		}
		st.ByStatus[s] = n
		st.Records += n
		st.WithLocation += loc
		st.Clubs += clubs
	}
	rows.Close()

	meta := map[string]string{
		"schema_version":  strconv.Itoa(SchemaVersion),
		"built_at":        time.Now().UTC().Format(time.RFC3339),
		"source_modified": o.SourceModified,
		"records":         strconv.Itoa(st.Records),
		"with_location":   strconv.Itoa(st.WithLocation),
		"source":          "FCC Universal Licensing System, amateur radio licenses (l_amat.zip)",
		"locations":       "approximate: the centre of each licensee's ZIP code (U.S. Census Bureau ZCTA)",
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

// readTable streams one pipe-delimited table from the zip, calling fn for each
// line. It checks for cancellation as it goes.
func readTable(ctx context.Context, zf *zip.File, fn func(fields []string, frac float64) error, report func(string, float64), phase string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	total := float64(zf.UncompressedSize64)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var read float64
	n := 0
	for sc.Scan() {
		line := sc.Text()
		read += float64(len(line) + 1)
		n++
		if n%20000 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if total > 0 {
				report(phase, min(1, read/total))
			}
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if err := fn(strings.Split(line, "|"), 0); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return ctx.Err()
}

// personName builds a display name from the FCC's name fields: "First M Last
// Suffix" for a person, or the entity (club or organisation) name otherwise.
func personName(first, mi, last, suffix, entity string) string {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	if first != "" && last != "" {
		parts := []string{first}
		if m := strings.TrimSpace(mi); m != "" {
			parts = append(parts, m)
		}
		parts = append(parts, last)
		if s := strings.TrimSpace(suffix); s != "" {
			parts = append(parts, s)
		}
		return prettyName(strings.Join(parts, " "))
	}
	return prettyName(entity)
}
