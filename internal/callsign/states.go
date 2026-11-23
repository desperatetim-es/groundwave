// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import "strings"

// stateNames maps a lower-case state or territory name to its two-letter code.
var stateNames = map[string]string{
	"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR", "california": "CA", "colorado": "CO",
	"connecticut": "CT", "delaware": "DE", "florida": "FL", "georgia": "GA", "hawaii": "HI", "idaho": "ID",
	"illinois": "IL", "indiana": "IN", "iowa": "IA", "kansas": "KS", "kentucky": "KY", "louisiana": "LA",
	"maine": "ME", "maryland": "MD", "massachusetts": "MA", "michigan": "MI", "minnesota": "MN",
	"mississippi": "MS", "missouri": "MO", "montana": "MT", "nebraska": "NE", "nevada": "NV",
	"new hampshire": "NH", "new jersey": "NJ", "new mexico": "NM", "new york": "NY", "north carolina": "NC",
	"north dakota": "ND", "ohio": "OH", "oklahoma": "OK", "oregon": "OR", "pennsylvania": "PA",
	"rhode island": "RI", "south carolina": "SC", "south dakota": "SD", "tennessee": "TN", "texas": "TX",
	"utah": "UT", "vermont": "VT", "virginia": "VA", "washington": "WA", "west virginia": "WV",
	"wisconsin": "WI", "wyoming": "WY",
	"district of columbia": "DC", "washington dc": "DC", "washington d c": "DC",
	"puerto rico": "PR", "guam": "GU", "virgin islands": "VI", "us virgin islands": "VI", "u s virgin islands": "VI",
	"american samoa": "AS", "northern mariana islands": "MP",
}

// stateCodes are the two-letter codes the FCC uses, including the military
// mail codes (AA, AE, AP).
var stateCodes = map[string]bool{
	"AL": true, "AK": true, "AZ": true, "AR": true, "CA": true, "CO": true, "CT": true, "DE": true, "FL": true,
	"GA": true, "HI": true, "ID": true, "IL": true, "IN": true, "IA": true, "KS": true, "KY": true, "LA": true,
	"ME": true, "MD": true, "MA": true, "MI": true, "MN": true, "MS": true, "MO": true, "MT": true, "NE": true,
	"NV": true, "NH": true, "NJ": true, "NM": true, "NY": true, "NC": true, "ND": true, "OH": true, "OK": true,
	"OR": true, "PA": true, "RI": true, "SC": true, "SD": true, "TN": true, "TX": true, "UT": true, "VT": true,
	"VA": true, "WA": true, "WV": true, "WI": true, "WY": true, "DC": true, "PR": true, "GU": true, "VI": true,
	"AS": true, "MP": true, "AA": true, "AE": true, "AP": true,
}

// StateCode turns a two-letter code or a full name ("IL", "il", "Illinois",
// "north carolina", "D.C.") into the two-letter code. ok is false if it is
// neither.
func StateCode(s string) (code string, ok bool) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.NewReplacer(".", "", ",", "").Replace(t)
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return "", false
	}
	if c, found := stateNames[t]; found {
		return c, true
	}
	if len(t) == 2 {
		if c := strings.ToUpper(t); stateCodes[c] {
			return c, true
		}
	}
	return "", false
}
