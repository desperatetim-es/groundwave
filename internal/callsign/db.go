// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// SchemaVersion is bumped whenever the database layout changes, so an old file
// is rebuilt instead of misread.
const SchemaVersion = 1

// ErrNoDatabase is returned when no callsign database has been built yet.
var ErrNoDatabase = errors.New("no callsign database has been downloaded yet")

const createTables = `
CREATE TABLE license (
	usi          INTEGER PRIMARY KEY,
	call         TEXT NOT NULL,
	name         TEXT NOT NULL DEFAULT '',
	club         INTEGER NOT NULL DEFAULT 0,
	class        TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL,
	granted      TEXT NOT NULL DEFAULT '',
	expires      TEXT NOT NULL DEFAULT '',
	prev_call    TEXT NOT NULL DEFAULT '',
	trustee_call TEXT NOT NULL DEFAULT '',
	trustee_name TEXT NOT NULL DEFAULT '',
	city         TEXT NOT NULL DEFAULT '',
	state        TEXT NOT NULL DEFAULT '',
	zip          TEXT NOT NULL DEFAULT '',
	lat          REAL,
	lon          REAL,
	grid         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

const createIndexes = `
CREATE INDEX license_call  ON license(call);
CREATE INDEX license_zip   ON license(zip);
CREATE INDEX license_state ON license(state, city);
CREATE VIRTUAL TABLE license_fts USING fts5(
	name, city, trustee_name,
	content='license', content_rowid='usi',
	tokenize='unicode61 remove_diacritics 2'
);
INSERT INTO license_fts(license_fts) VALUES('rebuild');
`

// DB is a read-only handle on a built database.
type DB struct {
	db *sql.DB
}

// Open opens an existing database for searching.
func Open(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoDatabase
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	d := &DB{db: db}
	m, err := d.Meta()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("the callsign database cannot be read (%w); download it again", err)
	}
	if v, _ := strconv.Atoi(m["schema_version"]); v != SchemaVersion {
		db.Close()
		return nil, errors.New("the callsign database is from a different version of this program; download it again")
	}
	return d, nil
}

// Close releases the database.
func (d *DB) Close() error { return d.db.Close() }

// Meta returns the facts recorded when the database was built.
func (d *DB) Meta() (map[string]string, error) {
	rows, err := d.db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// Info summarises a database for the UI.
type Info struct {
	Records        int       `json:"records"`
	BuiltAt        time.Time `json:"built_at"`
	SourceModified string    `json:"source_modified,omitempty"` // when the FCC published the file
}

// Info returns the database's size and age.
func (d *DB) Info() (Info, error) {
	m, err := d.Meta()
	if err != nil {
		return Info{}, err
	}
	var in Info
	in.Records, _ = strconv.Atoi(m["records"])
	in.BuiltAt, _ = time.Parse(time.RFC3339, m["built_at"])
	in.SourceModified = m["source_modified"]
	return in, nil
}
