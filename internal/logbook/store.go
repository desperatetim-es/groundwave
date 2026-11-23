// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package logbook

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// ErrNotFound is returned when a contact id does not exist.
var ErrNotFound = errors.New("contact not found")

// Store is the log, kept in one SQLite file in the data folder.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS qso (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	call        TEXT NOT NULL,
	date        TEXT NOT NULL,
	time        TEXT NOT NULL,
	freq_mhz    REAL NOT NULL DEFAULT 0,
	band        TEXT NOT NULL DEFAULT '',
	mode        TEXT NOT NULL,
	submode     TEXT NOT NULL DEFAULT '',
	rst_sent    TEXT NOT NULL DEFAULT '',
	rst_rcvd    TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL DEFAULT '',
	qth         TEXT NOT NULL DEFAULT '',
	grid        TEXT NOT NULL DEFAULT '',
	grid_source TEXT NOT NULL DEFAULT '',
	my_grid     TEXT NOT NULL DEFAULT '',
	pota_ref    TEXT NOT NULL DEFAULT '',
	sota_ref    TEXT NOT NULL DEFAULT '',
	my_pota_ref TEXT NOT NULL DEFAULT '',
	my_sota_ref TEXT NOT NULL DEFAULT '',
	comment     TEXT NOT NULL DEFAULT '',
	extra       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS qso_when ON qso(date DESC, time DESC);
CREATE INDEX IF NOT EXISTS qso_call ON qso(call);
`

const columns = `id, call, date, time, freq_mhz, band, mode, submode, rst_sent, rst_rcvd, name, qth,
	grid, grid_source, my_grid, pota_ref, sota_ref, my_pota_ref, my_sota_ref, comment, extra`

// Open opens (creating if needed) the log at path. The log is written with full synchronous
// commits: it is the one file here that cannot be re-downloaded.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_pragma=journal_mode(DELETE)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the log.
func (s *Store) Close() error { return s.db.Close() }

type scanner interface{ Scan(...any) error }

func scan(r scanner) (QSO, error) {
	var q QSO
	var extra string
	if err := r.Scan(&q.ID, &q.Call, &q.Date, &q.Time, &q.FreqMHz, &q.Band, &q.Mode, &q.Submode, &q.RSTSent, &q.RSTRcvd,
		&q.Name, &q.QTH, &q.Grid, &q.GridSource, &q.MyGrid, &q.POTARef, &q.SOTARef, &q.MyPOTARef, &q.MySOTARef, &q.Comment, &extra); err != nil {
		return QSO{}, err
	}
	if extra != "" {
		_ = json.Unmarshal([]byte(extra), &q.Extra)
	}
	return q, nil
}

func extraJSON(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

func insert(x execer, q QSO) (int64, error) {
	res, err := x.Exec(`INSERT INTO qso (call, date, time, freq_mhz, band, mode, submode, rst_sent, rst_rcvd, name, qth,
		grid, grid_source, my_grid, pota_ref, sota_ref, my_pota_ref, my_sota_ref, comment, extra)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		q.Call, q.Date, q.Time, q.FreqMHz, q.Band, q.Mode, q.Submode, q.RSTSent, q.RSTRcvd, q.Name, q.QTH,
		q.Grid, q.GridSource, q.MyGrid, q.POTARef, q.SOTARef, q.MyPOTARef, q.MySOTARef, q.Comment, extraJSON(q.Extra))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Add validates and stores a contact and returns it with its id.
func (s *Store) Add(q QSO) (QSO, error) {
	if err := q.Normalize(); err != nil {
		return QSO{}, err
	}
	id, err := insert(s.db, q)
	if err != nil {
		return QSO{}, err
	}
	q.ID = id
	return q, nil
}

// Update replaces contact id with q. Fields that have no column (kept in Extra) are retained
// from the stored copy when q has none.
func (s *Store) Update(id int64, q QSO) (QSO, error) {
	if err := q.Normalize(); err != nil {
		return QSO{}, err
	}
	old, err := s.Get(id)
	if err != nil {
		return QSO{}, err
	}
	if q.Extra == nil {
		q.Extra = old.Extra
	}
	_, err = s.db.Exec(`UPDATE qso SET call=?, date=?, time=?, freq_mhz=?, band=?, mode=?, submode=?, rst_sent=?, rst_rcvd=?,
		name=?, qth=?, grid=?, grid_source=?, my_grid=?, pota_ref=?, sota_ref=?, my_pota_ref=?, my_sota_ref=?, comment=?, extra=? WHERE id=?`,
		q.Call, q.Date, q.Time, q.FreqMHz, q.Band, q.Mode, q.Submode, q.RSTSent, q.RSTRcvd, q.Name, q.QTH,
		q.Grid, q.GridSource, q.MyGrid, q.POTARef, q.SOTARef, q.MyPOTARef, q.MySOTARef, q.Comment, extraJSON(q.Extra), id)
	if err != nil {
		return QSO{}, err
	}
	q.ID = id
	return q, nil
}

// Delete removes a contact.
func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM qso WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one contact.
func (s *Store) Get(id int64) (QSO, error) {
	q, err := scan(s.db.QueryRow(`SELECT `+columns+` FROM qso WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return QSO{}, ErrNotFound
	}
	return q, err
}

// likeEscape makes text safe to use inside LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// List returns contacts newest first. A non-empty q matches a call sign prefix, or part of a
// name, place, note or reference. It also returns how many contacts matched in all.
func (s *Store) List(q string, limit, offset int) ([]QSO, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where, args := "", []any{}
	if q = strings.TrimSpace(q); q != "" {
		e := likeEscape(q)
		where = ` WHERE call LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\' OR qth LIKE ? ESCAPE '\' OR comment LIKE ? ESCAPE '\'
			OR pota_ref LIKE ? ESCAPE '\' OR sota_ref LIKE ? ESCAPE '\' OR grid LIKE ? ESCAPE '\'`
		args = append(args, e+"%", "%"+e+"%", "%"+e+"%", "%"+e+"%", e+"%", e+"%", e+"%")
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM qso`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT `+columns+` FROM qso`+where+` ORDER BY date DESC, time DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []QSO{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// All returns every contact, oldest first (the order an ADIF file is usually read in).
func (s *Store) All() ([]QSO, error) {
	rows, err := s.db.Query(`SELECT ` + columns + ` FROM qso ORDER BY date, time, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QSO
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ImportResult reports what an ADIF import did.
type ImportResult struct {
	Records    int      `json:"records"`    // records found in the file
	Added      int      `json:"added"`      // new contacts stored
	Duplicates int      `json:"duplicates"` // already in the log (same call, day, minute, band and mode)
	Skipped    int      `json:"skipped"`    // not stored because they were not valid contacts
	Problems   []string `json:"problems,omitempty"`
}

const maxProblems = 20

// ImportADIF reads ADIF text and adds the contacts that are not already in the log. The whole
// import is one transaction: either every new contact is stored or none is.
func (s *Store) ImportADIF(data []byte) (ImportResult, error) {
	var res ImportResult
	recs, err := ParseADIF(data)
	if err != nil {
		return res, fmt.Errorf("%w: this does not look like an ADIF file (%v)", ErrInvalid, err)
	}
	res.Records = len(recs)
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	for i, f := range recs {
		q := FromFields(f)
		if err := q.Normalize(); err != nil {
			res.Skipped++
			if len(res.Problems) < maxProblems {
				res.Problems = append(res.Problems, fmt.Sprintf("record %d (%s): %s", i+1, f["call"], strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")))
			}
			continue
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM qso WHERE call=? AND date=? AND substr(time,1,4)=? AND band=? AND mode=?`,
			q.Call, q.Date, q.Time[:4], q.Band, q.Mode).Scan(&n); err != nil {
			return res, err
		}
		if n > 0 {
			res.Duplicates++
			continue
		}
		if _, err := insert(tx, q); err != nil {
			return res, err
		}
		res.Added++
	}
	return res, tx.Commit()
}
