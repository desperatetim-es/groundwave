// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"database/sql"
	"regexp"
	"strings"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxWords     = 6
)

const selectColumns = `l.call, l.name, l.club, l.class, l.status, l.granted, l.expires, l.prev_call,
	l.trustee_call, l.trustee_name, l.city, l.state, l.zip, l.lat, l.lon, l.grid`

func scanRecord(rows *sql.Rows, country string) (Record, error) {
	var r Record
	var club int
	var lat, lon sql.NullFloat64
	if err := rows.Scan(&r.Call, &r.Name, &club, &r.Class, &r.Status, &r.Granted, &r.Expires, &r.PrevCall,
		&r.TrusteeCall, &r.TrusteeName, &r.City, &r.State, &r.ZIP, &lat, &lon, &r.Grid); err != nil {
		return Record{}, err
	}
	r.Club = club != 0
	r.Country = country
	r.ClassName = classNameFor(country, r.Class)
	r.StatusName = statusNameFor(country, r.Status)
	if lat.Valid && lon.Valid {
		la, lo := lat.Float64, lon.Float64
		r.Lat, r.Lon = &la, &lo
	}
	return r, nil
}

// A call sign starts with a US prefix (A, K, N or W and an optional second letter) or a
// Canadian one (VA to VG, VO, VY, CF to CK, CY, CZ, XJ to XO); a partial one is that, the
// digit and up to three more letters (W, KD9, N0CAL, W1AW, VE3, VA3XYZ).
// zipRE matches a ZIP code: 12345, 12345-6789 or 123456789.
var zipRE = regexp.MustCompile(`^[0-9]{5}(-?[0-9]{4})?$`)

var callRE = regexp.MustCompile(`^(?:[AKNWaknw][A-Za-z]?|[Vv][A-Ga-gOoYy]|[Cc][F-Kf-kYyZz]|[Xx][J-Oj-o])[0-9][A-Za-z]{0,3}$`)

// postalRE matches a Canadian postal code's forward sortation area (K1A) or a full code (K1A 0B1 or K1A0B1).
var postalRE = regexp.MustCompile(`^[A-Za-z][0-9][A-Za-z](?: ?[0-9][A-Za-z][0-9])?$`)

// NormalizeCall upper-cases a call sign and strips portable markers and
// reciprocal prefixes: W1AW/4 and W1AW/P give W1AW; VE3/W1AW gives W1AW.
func NormalizeCall(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.Contains(s, "/") {
		return s
	}
	best := ""
	for _, part := range strings.Split(s, "/") {
		if callRE.MatchString(part) && strings.IndexAny(part, "0123456789") >= 0 && len(part) > len(best) {
			best = part
		}
	}
	if best == "" {
		return strings.Split(s, "/")[0]
	}
	return best
}

// word is one name or city word; col restricts it to that column ("name" or "city")
type word struct{ text, col string }

// QueryError is returned for a query the user can fix (as opposed to a database failure).
type QueryError struct{ Msg string }

func (e *QueryError) Error() string { return e.Msg }

type query struct {
	call  string // a call sign or the start of one
	zip   string // five digits
	state string // two letters
	words []word // name or city words, each matched as a prefix

	// cityFirst is set for the form "City, ST": the words are tried as a city first, and only
	// if nothing has that city are they matched as a name or city.
	cityFirst bool

	// cityExact, when set, ranks licenses whose whole city name equals it ahead of those that merely
	// contain the words (Portland before South Portland). It is normalised: lower case, no hyphens or dots.
	cityExact string
}

// parseQuery understands free text ("kd9", "smith cary", "60013", "jones il")
// and explicit filters (call:W9 zip:60013 st:IL).
func parseQuery(q string) (query, error) {
	var p query
	// "Cary, IL" or "Cary, Illinois": what follows the last comma is a state.
	// Anything else after a comma ("Smith, John") is just ordinary text.
	if i := strings.LastIndex(q, ","); i >= 0 {
		if code, ok := StateCode(q[i+1:]); ok {
			p.state, p.cityFirst = code, true
			q = q[:i]
		}
	}
	for _, tok := range strings.Fields(q) {
		low := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(low, "call:"):
			p.call = NormalizeCall(tok[5:])
		case strings.HasPrefix(low, "zip:"), strings.HasPrefix(low, "postal:"):
			// a US ZIP code, or a Canadian postal code (only its first three characters are kept)
			val := tok[strings.Index(tok, ":")+1:]
			if p.zip = cleanZIP(val); p.zip == "" {
				p.zip = cleanFSA(val)
			}
		case strings.HasPrefix(low, "st:"), strings.HasPrefix(low, "state:"):
			code, ok := StateCode(tok[strings.Index(tok, ":")+1:])
			if !ok {
				return p, &QueryError{"a state is two letters or a full name, for example st:IL or st:illinois"}
			}
			p.state = code
		case strings.HasPrefix(low, "name:"):
			p.addWord(tok[5:], "name")
		case strings.HasPrefix(low, "city:"):
			p.addWord(tok[5:], "city")
		case p.call == "" && callRE.MatchString(NormalizeCall(tok)):
			p.call = NormalizeCall(tok)
		case zipRE.MatchString(tok):
			p.zip = cleanZIP(tok)
		default:
			p.addWord(tok, "")
		}
	}
	if p.call == "" && p.zip == "" && p.state == "" && len(p.words) == 0 {
		return p, &QueryError{"type a call sign, a name, a city, or a ZIP code"}
	}
	return p, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// addWord adds name or city words. The text is split at every character that is not
// a letter or digit (as the full-text index does), so Winston-Salem becomes winston
// and salem, and nothing unsafe ever reaches the index expression.
func (p *query) addWord(w, col string) {
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 && len(p.words) < maxWords {
			p.words = append(p.words, word{b.String(), col})
		}
		b.Reset()
	}
	for _, r := range w {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
}

// Search finds licenses matching free text. At most limit results (default 50,
// largest 200) come back, with exact call sign matches and active licenses first.
func (d *DB) Search(q string, limit int) ([]Record, error) {
	p, err := parseQuery(q)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if p.cityFirst && len(p.words) > 0 {
		// "Cary, IL" means the city Cary. If no license has such a city ("smith cary, IL"),
		// fall back to matching the words as a name or city.
		city := p
		city.words = make([]word, len(p.words))
		var texts []string
		for i, w := range p.words {
			city.words[i] = w
			if w.col == "" {
				city.words[i].col = "city"
				texts = append(texts, strings.ToLower(w.text))
			}
		}
		city.cityExact = strings.Join(texts, " ")
		if res, err := d.run(city, limit); err != nil || len(res) > 0 {
			return res, err
		}
	}
	return d.run(p, limit)
}

// run executes a parsed query.
func (d *DB) run(p query, limit int) ([]Record, error) {
	var where []string
	var args []any
	from := `license l`
	if len(p.words) > 0 {
		from = `license l JOIN license_fts f ON f.rowid = l.usi`
		parts := make([]string, len(p.words))
		for i, w := range p.words {
			parts[i] = `"` + w.text + `"*`
			if w.col != "" {
				parts[i] = w.col + " : " + parts[i] // FTS5 column filter
			}
		}
		where = append(where, `license_fts MATCH ?`)
		args = append(args, strings.Join(parts, " AND "))
	}
	if p.call != "" {
		where = append(where, `l.call >= ? AND l.call < ?`)
		args = append(args, p.call, p.call+"￿")
	}
	if p.zip != "" {
		where = append(where, `l.zip = ?`)
		args = append(args, p.zip)
	}
	if p.state != "" {
		where = append(where, `l.state = ?`)
		args = append(args, p.state)
	}
	// ORDER BY terms are built from the least to the most important, each prepended, with its
	// argument prepended to match, so the text and the arguments always stay in step.
	order := `(l.status = 'A' AND (l.expires = '' OR l.expires >= date('now'))) DESC, l.call, l.granted DESC`
	var orderArgs []any
	if p.cityExact != "" {
		// the city whose whole name is the words typed comes first (Portland before South Portland)
		order = `(REPLACE(REPLACE(LOWER(l.city), '-', ' '), '.', '') = ?) DESC, ` + order
		orderArgs = append([]any{p.cityExact}, orderArgs...)
	}
	if p.call != "" {
		order = `(l.call = ?) DESC, ` + order
		orderArgs = append([]any{p.call}, orderArgs...)
	}
	args = append(args, orderArgs...)
	sqlText := `SELECT ` + selectColumns + ` FROM ` + from + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows, d.country)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Lookup returns the licenses for one exact call sign, active first. The same
// call can appear more than once when an expired license was later reissued.
func (d *DB) Lookup(call string) ([]Record, error) {
	c := NormalizeCall(call)
	if c == "" {
		return nil, &QueryError{"enter a call sign"}
	}
	rows, err := d.db.Query(`SELECT `+selectColumns+` FROM license l WHERE l.call = ? ORDER BY (l.status = 'A' AND (l.expires = '' OR l.expires >= date('now'))) DESC, l.granted DESC`, c)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows, d.country)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
