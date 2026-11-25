// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package callsign is the offline US amateur callsign database. It is built
// from the FCC's public license file into a local SQLite database, keeping only
// what a lookup needs: call sign, name, class, status, dates and the home
// city, state and ZIP. Street addresses, PO boxes, phone numbers and email
// addresses are deliberately never read into the database.
package callsign

import (
	"strings"
	"unicode"
)

// Record is one license.
type Record struct {
	Country     string   `json:"country,omitempty"` // the source's ID: "us" or "ca"
	Call        string   `json:"call"`
	Name        string   `json:"name"`
	Club        bool     `json:"club,omitempty"` // a club or other organisation, not a person
	Class       string   `json:"class,omitempty"`
	ClassName   string   `json:"class_name,omitempty"`
	Status      string   `json:"status"`
	StatusName  string   `json:"status_name"`
	Granted     string   `json:"granted,omitempty"` // ISO dates
	Expires     string   `json:"expires,omitempty"`
	PrevCall    string   `json:"prev_call,omitempty"`
	TrusteeCall string   `json:"trustee_call,omitempty"`
	TrusteeName string   `json:"trustee_name,omitempty"`
	City        string   `json:"city,omitempty"`
	State       string   `json:"state,omitempty"`
	ZIP         string   `json:"zip,omitempty"`
	Lat         *float64 `json:"lat,omitempty"` // the centre of the ZIP code: approximate
	Lon         *float64 `json:"lon,omitempty"`
	Grid        string   `json:"grid,omitempty"` // 6-character Maidenhead square of that point: approximate
}

var classNames = map[string]string{
	"T": "Technician", "G": "General", "E": "Amateur Extra",
	"A": "Advanced", "N": "Novice", "P": "Technician Plus",
}

// ClassName spells out an FCC operator class code.
func ClassName(code string) string { return classNames[strings.ToUpper(strings.TrimSpace(code))] }

// caQualNames spell out ISED's qualification flags (stored as the letters A to E).
var caQualNames = map[rune]string{
	'A': "Basic", 'B': "5 WPM", 'C': "12 WPM", 'D': "Advanced", 'E': "Basic with Honours",
}

// classNameFor spells out a record's class for the country it comes from. For Canada the
// stored value is the set of qualification letters the holder has (ADE: Basic, Advanced
// and Basic with Honours).
func classNameFor(country, code string) string {
	if country != IDCA {
		return ClassName(code)
	}
	var parts []string
	for _, r := range code {
		if n, ok := caQualNames[r]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}

// statusNameFor labels a record's status. ISED publishes no status or expiry, so a
// Canadian record is simply "Listed".
func statusNameFor(country, code string) string {
	if country == IDCA {
		return "Listed"
	}
	return StatusName(code)
}

var statusNames = map[string]string{
	"A": "Active", "E": "Expired", "C": "Cancelled", "T": "Terminated", "X": "Termination pending",
	"L": "Pending legal status", "P": "Parent station cancelled",
}

// StatusName spells out an FCC license status code.
func StatusName(code string) string {
	if n, ok := statusNames[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return n
	}
	return code
}

// isoDate converts the FCC's MM/DD/YYYY to YYYY-MM-DD ("" if it is not a date).
func isoDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 10 || s[2] != '/' || s[5] != '/' {
		return ""
	}
	for _, i := range []int{0, 1, 3, 4, 6, 7, 8, 9} {
		if s[i] < '0' || s[i] > '9' {
			return ""
		}
	}
	return s[6:10] + "-" + s[0:2] + "-" + s[3:5]
}

// prettyName turns the FCC's usual UPPER CASE into readable capitalisation
// (MCDONALD, JOHN A -> McDonald, John A). Text that already has lower-case
// letters is left alone.
func prettyName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	// Leave text that already has a deliberate mix of upper and lower case alone; fix text that is all
	// one case (the FCC has both: JOHN SMITH and honolulu).
	if s == "" || (strings.IndexFunc(s, unicode.IsLower) >= 0 && strings.IndexFunc(s, unicode.IsUpper) >= 0) {
		return s
	}
	var b strings.Builder
	start := true // at the start of a word
	for _, r := range strings.ToLower(s) {
		switch {
		case start && unicode.IsLetter(r):
			b.WriteRune(unicode.ToUpper(r))
			start = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			start = false
		default:
			b.WriteRune(r)
			start = r == ' ' || r == '-' || r == '.' || r == '/' || r == '(' || r == '\''
		}
	}
	out := b.String()
	// Acronyms (club names are full of them), roman-numeral suffixes, Mc prefixes.
	words := strings.Split(out, " ")
	for i, w := range words {
		bare := strings.ToUpper(strings.Trim(w, ".,()/'-"))
		switch {
		case acronyms[bare]:
			words[i] = strings.ToUpper(w)
		case bare == "II" || bare == "III" || bare == "IV":
			words[i] = strings.ToUpper(w)
		case isInitials(bare):
			words[i] = strings.ToUpper(w) // no vowels: almost certainly initials, such as RMC or WNY
		case len(w) > 2 && strings.HasPrefix(w, "Mc"):
			words[i] = "Mc" + strings.ToUpper(w[2:3]) + w[3:]
		}
	}
	return strings.Join(words, " ")
}

// isInitials reports whether an upper-case word with no vowels is initials (RMC, WNY)
// rather than a real word. Y counts as a vowel in longer words, so names such as Lynn
// and Glynn survive, but short Y-only words (NY, WNY, NYC) are initials.
func isInitials(w string) bool {
	if len(w) < 2 || len(w) > 5 || strings.ContainsAny(w, "AEIOU0123456789") || titleWords[w] {
		return false
	}
	if strings.Contains(w, "Y") {
		return len(w) <= 3 && !yWords[w]
	}
	return true
}

// yWords are short real words whose only vowel is Y.
var yWords = map[string]bool{"FRY": true, "SKY": true, "SLY": true, "SPY": true, "DRY": true, "FLY": true, "TRY": true, "WHY": true, "CRY": true, "PRY": true, "SHY": true, "WRY": true, "GYM": true, "HYMN": true}

// acronyms that contain vowels and so are not caught by the no-vowel rule.
var acronyms = map[string]bool{
	"ARRL": true, "ARES": true, "RACES": true, "ARC": true, "ARS": true, "ARA": true, "ARG": true, "EOC": true,
	"EMA": true, "EMS": true, "USA": true, "USAF": true, "USN": true, "USCG": true, "NASA": true, "NOAA": true,
	"FEMA": true, "MIT": true, "CERT": true, "VFW": true, "YMCA": true, "YL": true, "DX": true, "UHF": true,
	"VHF": true, "QRP": true, "NY": true, "IEEE": true, "UAV": true, "ATV": true, "ACS": true, "OEM": true, "EMC": true,
}

// titleWords are short vowel-less words that really are words, not initials.
var titleWords = map[string]bool{
	"ST": true, "MR": true, "MRS": true, "MS": true, "DR": true, "JR": true, "SR": true, "MT": true, "FT": true,
	"HWY": true, "RD": true, "LTD": true,
}
