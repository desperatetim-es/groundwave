// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package logbook

import "strings"

// bands are the ADIF 3.1.4 band names with their limits in MHz (from the ADIF specification,
// adif.org; the 5m entry is omitted as it is not an amateur allocation anywhere that matters here).
var bands = []struct {
	name   string
	lo, hi float64
}{
	{"2190m", 0.1357, 0.1378}, {"630m", 0.472, 0.479}, {"560m", 0.501, 0.504},
	{"160m", 1.8, 2.0}, {"80m", 3.5, 4.0}, {"60m", 5.06, 5.45}, {"40m", 7.0, 7.3},
	{"30m", 10.1, 10.15}, {"20m", 14.0, 14.35}, {"17m", 18.068, 18.168}, {"15m", 21.0, 21.45},
	{"12m", 24.89, 24.99}, {"10m", 28.0, 29.7}, {"8m", 40, 45}, {"6m", 50, 54},
	{"4m", 70, 71}, {"2m", 144, 148}, {"1.25m", 222, 225}, {"70cm", 420, 450},
	{"33cm", 902, 928}, {"23cm", 1240, 1300}, {"13cm", 2300, 2450}, {"9cm", 3300, 3500},
	{"6cm", 5650, 5925}, {"3cm", 10000, 10500}, {"1.25cm", 24000, 24250},
	{"6mm", 47000, 47200}, {"4mm", 75500, 81000}, {"2.5mm", 119980, 123000},
	{"2mm", 134000, 149000}, {"1mm", 241000, 250000},
}

// BandFor returns the ADIF band name for a frequency in MHz, or "" when it is outside every band.
func BandFor(mhz float64) string {
	for _, b := range bands {
		if mhz >= b.lo && mhz <= b.hi {
			return b.name
		}
	}
	return ""
}

// ValidBand reports whether s is an ADIF band name.
func ValidBand(s string) bool {
	for _, b := range bands {
		if strings.EqualFold(b.name, s) {
			return true
		}
	}
	return false
}

// bandName returns the canonical spelling of a band ("20M" gives "20m").
func bandName(s string) string {
	for _, b := range bands {
		if strings.EqualFold(b.name, s) {
			return b.name
		}
	}
	return ""
}

// submodeParent maps submodes that people type as a mode to the ADIF mode they belong under.
var submodeParent = map[string]string{
	"LSB": "SSB", "USB": "SSB",
	"FT4": "MFSK", "JS8": "MFSK", "FST4": "MFSK", "Q65": "MFSK",
	"PSK31": "PSK", "PSK63": "PSK", "PSK125": "PSK",
	"C4FM": "DIGITALVOICE", "DMR": "DIGITALVOICE", "DSTAR": "DIGITALVOICE", "FREEDV": "DIGITALVOICE", "M17": "DIGITALVOICE",
	"JT65A": "JT65", "JT65B": "JT65", "JT65C": "JT65",
}

// NormalizeMode upper-cases a mode and moves a submode typed as a mode to its place, so that "ft4"
// is stored as mode MFSK, submode FT4, which is what other programs expect.
func NormalizeMode(mode, submode string) (string, string) {
	mode = strings.ToUpper(strings.TrimSpace(mode))
	submode = strings.ToUpper(strings.TrimSpace(submode))
	if parent, ok := submodeParent[mode]; ok {
		if submode == "" {
			submode = mode
		}
		mode = parent
	}
	return mode, submode
}
