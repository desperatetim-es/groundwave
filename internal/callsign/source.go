// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"context"
	"strings"
)

// A Source is one country's published list of amateur licenses: where the user's
// computer downloads it from, how it is read into a database, and what is shown
// about it. The program never ships a list; each one is fetched by the user, from
// the issuing authority, when they press the button.
type Source struct {
	ID      string // "us", "ca": also stored in the database and sent with every result
	Country string // for display: "United States"
	Org     string // the issuing authority, for messages: "FCC", "ISED"
	DBFile  string // the database file in the callsigns folder
	ZipName string // the temporary download name

	URL      string            // where the list is published (a constant; never taken from a request)
	Host     func(string) bool // download addresses and redirects must be https on one of these hosts
	MaxBytes int64             // a larger download is refused
	Download string            // what the user is told to expect, for example "about 200 MB"
	Disk     string            // free disk space needed while it builds
	Credit   string            // attribution shown in the app
	Import   func(ctx context.Context, o ImportOptions) (ImportStats, error)
}

// US is the FCC's weekly amateur license file.
var US = &Source{
	ID: IDUS, Country: "United States", Org: "FCC",
	DBFile: "callsigns.db", ZipName: "l_amat.zip",
	URL: FCCURL, Host: fccHost, MaxBytes: 2 << 30,
	Download: "about 200 MB", Disk: "roughly 500 MB",
	Credit: "Source: the FCC's public amateur license file (refreshed weekly). Only call sign, name, class, status, dates and city, state and ZIP are kept. Street addresses, phone numbers and email addresses are never saved.",
	Import: Import,
}

// Canada is ISED Canada's amateur call sign list.
var Canada = &Source{
	ID: IDCA, Country: "Canada", Org: "ISED",
	DBFile: "callsigns-ca.db", ZipName: "amateur_delim.zip",
	URL: ISEDURL, Host: isedHost, MaxBytes: 100 << 20,
	Download: "about 3 MB", Disk: "roughly 50 MB",
	Credit: "Source: Innovation, Science and Economic Development Canada (ISED), Canadian amateur radio call sign list, https://ised-isde.canada.ca/site/amateur-radio-operator-certificate-services/en/node/5. " +
		"Only call sign, name, qualifications, city, province and the first three characters of the postal code are kept. Street addresses are never saved. " +
		"Locations are approximate (the centre of the postal-code area), from GeoNames postal code data, https://www.geonames.org/, licensed CC BY 4.0.",
	Import: ImportCanada,
}

// The source IDs, as stored in each database and sent with each result.
const (
	IDUS = "us"
	IDCA = "ca"
)

// Sources lists every source the program knows, in display order.
var Sources = []*Source{US, Canada}

// SourceByID returns the source with that ID, or nil.
func SourceByID(id string) *Source {
	for _, s := range Sources {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// ISEDURL is where ISED Canada publishes the amateur call sign list (a zip holding a
// delimited text file).
const ISEDURL = "https://apc-cap.ic.gc.ca/datafiles/amateur_delim.zip"

// isedHost accepts ISED's download host and the Government of Canada sites it may
// redirect to.
func isedHost(h string) bool {
	h = strings.ToLower(h)
	return h == "ic.gc.ca" || strings.HasSuffix(h, ".ic.gc.ca") || h == "canada.ca" || strings.HasSuffix(h, ".canada.ca")
}
