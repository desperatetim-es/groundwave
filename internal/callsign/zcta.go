// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// zcta.csv: ZIP code centre points from the U.S. Census Bureau (public domain);
// see the comment lines at the top of the file for the source.
//
//go:embed zcta.csv
var zctaCSV string

var (
	zctaOnce sync.Once
	zctaMap  map[string][2]float64
)

func loadZCTA() {
	zctaMap = make(map[string][2]float64, 34000)
	for _, line := range strings.Split(zctaCSV, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) != 3 {
			continue
		}
		lat, e1 := strconv.ParseFloat(f[1], 64)
		lon, e2 := strconv.ParseFloat(f[2], 64)
		if e1 != nil || e2 != nil {
			continue
		}
		zctaMap[f[0]] = [2]float64{lat, lon}
	}
}

// ZIPLocation returns the centre point of a ZIP code (the first five digits of
// a ZIP or ZIP+4 are used). The location is approximate: it is the middle of the
// ZIP code's area, not any address.
func ZIPLocation(zip string) (lat, lon float64, ok bool) {
	zctaOnce.Do(loadZCTA)
	z := cleanZIP(zip)
	if z == "" {
		return 0, 0, false
	}
	p, ok := zctaMap[z]
	return p[0], p[1], ok
}

// cleanZIP returns the five-digit part of a ZIP or ZIP+4, or "".
func cleanZIP(zip string) string {
	z := strings.TrimSpace(zip)
	if len(z) < 5 {
		return ""
	}
	z = z[:5]
	for i := 0; i < 5; i++ {
		if z[i] < '0' || z[i] > '9' {
			return ""
		}
	}
	return z
}
