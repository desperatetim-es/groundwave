// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// fsa.csv: approximate centre points of Canadian forward sortation areas (the first
// three characters of a postal code), from GeoNames (Creative Commons Attribution 4.0);
// see the comment lines at the top of the file for the source and licence.
//
//go:embed fsa.csv
var fsaCSV string

var (
	fsaOnce sync.Once
	fsaMap  map[string][2]float64
)

func loadFSA() {
	fsaMap = make(map[string][2]float64, 1700)
	for _, line := range strings.Split(fsaCSV, "\n") {
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
		fsaMap[f[0]] = [2]float64{lat, lon}
	}
}

// FSALocation returns the centre point of a postal code's forward sortation area (only
// its first three characters are used, so a full postal code works). The location is
// approximate: the middle of a postal district, not any address.
func FSALocation(postal string) (lat, lon float64, ok bool) {
	fsaOnce.Do(loadFSA)
	f := cleanFSA(postal)
	if f == "" {
		return 0, 0, false
	}
	p, ok := fsaMap[f]
	return p[0], p[1], ok
}

// cleanFSA returns the forward sortation area (letter, digit, letter) at the start of a
// Canadian postal code in any case and with or without a space, or "".
func cleanFSA(postal string) string {
	p := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(postal), " ", ""))
	if len(p) < 3 {
		return ""
	}
	f := p[:3]
	if f[0] >= 'A' && f[0] <= 'Z' && f[1] >= '0' && f[1] <= '9' && f[2] >= 'A' && f[2] <= 'Z' {
		return f
	}
	return ""
}
