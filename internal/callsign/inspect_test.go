// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"os"
	"testing"
)

// TestInspectBuiltDatabase prints facts about a real database (GROUNDWAVE_CS_DB). Skipped otherwise.
func TestInspectBuiltDatabase(t *testing.T) {
	p := os.Getenv("GROUNDWAVE_CS_DB")
	if p == "" {
		t.Skip("set GROUNDWAVE_CS_DB to a built callsigns.db")
	}
	db, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := func(label, sql string) {
		var n int
		if err := db.db.QueryRow(sql).Scan(&n); err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		t.Logf("%-62s %d", label, n)
	}
	q("total", `SELECT COUNT(*) FROM license`)
	q("status E", `SELECT COUNT(*) FROM license WHERE status='E'`)
	q("status A with an expiry date already in the past (grace period)", `SELECT COUNT(*) FROM license WHERE status='A' AND expires != '' AND expires < date('now')`)
	q("  ...of those, expired within the last 2 years", `SELECT COUNT(*) FROM license WHERE status='A' AND expires != '' AND expires < date('now') AND expires >= date('now','-2 years')`)
	q("  ...expired more than 2 years ago but still status A", `SELECT COUNT(*) FROM license WHERE status='A' AND expires != '' AND expires < date('now','-2 years')`)
	q("status A with no expiry date", `SELECT COUNT(*) FROM license WHERE status='A' AND expires = ''`)
	q("clubs", `SELECT COUNT(*) FROM license WHERE club = 1`)
	q("with a class", `SELECT COUNT(*) FROM license WHERE class != ''`)
	q("with a location", `SELECT COUNT(*) FROM license WHERE lat IS NOT NULL`)
	q("calls appearing more than once", `SELECT COUNT(*) FROM (SELECT call FROM license GROUP BY call HAVING COUNT(*) > 1)`)
	rows, _ := db.db.Query(`SELECT class, COUNT(*) FROM license GROUP BY class ORDER BY 2 DESC`)
	for rows.Next() {
		var c string
		var n int
		rows.Scan(&c, &n)
		t.Logf("class %q: %d", c, n)
	}
	rows.Close()
}
