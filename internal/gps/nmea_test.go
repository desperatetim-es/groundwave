// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Real-world style sentences (checksums computed with Checksum below).
func sentence(body string) string { return "$" + body + "*" + Checksum(body) + "\r\n" }

func TestChecksumKnownValue(t *testing.T) {
	// The classic example from the NMEA documentation.
	if got := Checksum("GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,"); got != "47" {
		t.Fatalf("checksum = %s, want 47", got)
	}
	if got := Checksum("GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W"); got != "6A" {
		t.Fatalf("checksum = %s, want 6A", got)
	}
}

func TestParseSentenceValidation(t *testing.T) {
	good := "$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A"
	s, err := ParseSentence(good)
	if err != nil || s.Talker != "GP" || s.Type != "RMC" || s.Fields[0] != "123519" {
		t.Fatalf("good sentence rejected: %v %+v", err, s)
	}
	for name, line := range map[string]string{
		"bad checksum":   strings.Replace(good, "*6A", "*6B", 1),
		"no checksum":    "$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W",
		"no dollar":      "GPRMC,123519,A*00",
		"empty":          "",
		"garbage":        "\x00\xff\x01 hello",
		"short checksum": "$GPRMC,123519*6",
	} {
		if _, err := ParseSentence(line); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// other talkers: GNSS multi-constellation, GLONASS
	for _, body := range []string{"GNRMC,123519,A,4807.038,N,01131.000,E,0,0,230394,,", "GLRMC,123519,A,4807.038,N,01131.000,E,0,0,230394,,"} {
		if s, err := ParseSentence(sentence(body)); err != nil || s.Type != "RMC" {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestParseRMC(t *testing.T) {
	s, _ := ParseSentence("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")
	r, err := ParseRMC(s)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid || !r.HasPos {
		t.Fatalf("expected valid with position: %+v", r)
	}
	want := time.Date(1994, 3, 23, 12, 35, 19, 0, time.UTC)
	if !r.Time.Equal(want) {
		t.Errorf("time = %v, want %v", r.Time, want)
	}
	if math.Abs(r.Lat-48.1173) > 0.0001 || math.Abs(r.Lon-11.516667) > 0.0001 {
		t.Errorf("position = %v, %v", r.Lat, r.Lon)
	}
}

func TestParseRMCVariants(t *testing.T) {
	mk := func(body string) RMC {
		t.Helper()
		s, err := ParseSentence(sentence(body))
		if err != nil {
			t.Fatal(err)
		}
		r, err := ParseRMC(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// fractional seconds, southern/western hemispheres
	r := mk("GNRMC,010203.25,A,3351.000,S,15112.600,E,0.1,0,011126,,,A")
	if !r.Time.Equal(time.Date(2026, 11, 1, 1, 2, 3, 250000000, time.UTC)) {
		t.Errorf("fractional time = %v", r.Time)
	}
	if r.Lat > -33.84 || r.Lat < -33.86 {
		t.Errorf("south latitude = %v", r.Lat)
	}
	r = mk("GPRMC,235959.00,A,4213.000,N,08814.000,W,0,0,311226,,,A")
	if r.Lon > -88.2 || r.Lon < -88.3 {
		t.Errorf("west longitude = %v", r.Lon)
	}
	// void status: time may be present but the fix is not valid
	if mk("GPRMC,120000,V,,,,,,,121126,,,N").Valid {
		t.Error("status V reported valid")
	}
	// mode N (newer receivers) overrides status A
	if mk("GPRMC,120000,A,4213.000,N,08814.000,W,0,0,121126,,,N").Valid {
		t.Error("mode N reported valid")
	}
	// no position yet but valid time
	r = mk("GPRMC,120000,V,,,,,,,121126,,")
	if r.HasPos {
		t.Error("empty position reported present")
	}
	// leap second 60 is accepted without panicking
	mk("GPRMC,235960,A,4213.000,N,08814.000,W,0,0,311216,,,A")
}

func TestParseRMCRejectsBadFields(t *testing.T) {
	for name, body := range map[string]string{
		"bad hour":   "GPRMC,250000,A,4213.000,N,08814.000,W,0,0,121126,,",
		"bad minute": "GPRMC,126000,A,4213.000,N,08814.000,W,0,0,121126,,",
		"no time":    "GPRMC,,A,4213.000,N,08814.000,W,0,0,121126,,",
		"bad date":   "GPRMC,120000,A,4213.000,N,08814.000,W,0,0,311126,,", // 31 Nov
		"short date": "GPRMC,120000,A,4213.000,N,08814.000,W,0,0,1211,,",
		"zero month": "GPRMC,120000,A,4213.000,N,08814.000,W,0,0,121026,,"[:0] + "GPRMC,120000,A,4213.000,N,08814.000,W,0,0,120026,,",
	} {
		s, err := ParseSentence(sentence(body))
		if err != nil {
			t.Fatalf("%s: framing failed: %v", name, err)
		}
		if _, err := ParseRMC(s); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// out-of-range position is dropped, not trusted
	s, _ := ParseSentence(sentence("GPRMC,120000,A,9500.000,N,08814.000,W,0,0,121126,,"))
	if r, err := ParseRMC(s); err != nil || r.HasPos {
		t.Errorf("latitude 95 accepted: %+v %v", r, err)
	}
	s, _ = ParseSentence(sentence("GPRMC,120000,A,4260.000,N,08814.000,W,0,0,121126,,"))
	if r, _ := ParseRMC(s); r.HasPos {
		t.Error("minutes = 60 accepted")
	}
}

func TestParseGGA(t *testing.T) {
	s, _ := ParseSentence("$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,*47")
	g, err := ParseGGA(s)
	if err != nil {
		t.Fatal(err)
	}
	if g.Quality != 1 || g.Sats != 8 || g.HDOP == nil || *g.HDOP != 0.9 || g.AltM == nil || *g.AltM != 545.4 || !g.HasPos {
		t.Fatalf("unexpected: %+v", g)
	}
	// no fix: quality 0, empty fields
	s, _ = ParseSentence(sentence("GPGGA,123519,,,,,0,00,,,M,,M,,"))
	g, err = ParseGGA(s)
	if err != nil || g.Quality != 0 || g.HDOP != nil || g.AltM != nil || g.HasPos {
		t.Fatalf("no-fix GGA: %+v %v", g, err)
	}
}

func TestParseGSV(t *testing.T) {
	// 4 satellites, the first three with no signal (00) or an empty SNR field
	s, _ := ParseSentence(sentence("GPGSV,3,1,11,03,03,111,00,04,15,270,,06,01,010,22,13,06,292,35"))
	g, err := ParseGSV(s)
	if err != nil || g.Total != 3 || g.Num != 1 || g.InView != 11 || len(g.Sats) != 4 {
		t.Fatalf("%+v %v", g, err)
	}
	if g.Sats[0].SNR != nil || g.Sats[1].SNR != nil || g.Sats[2].SNR == nil || *g.Sats[2].SNR != 22 || *g.Sats[3].SNR != 35 {
		t.Errorf("snr: %+v", g.Sats)
	}
	if g.Sats[0].Sys != "GPS" || g.Sats[0].PRN != 3 || *g.Sats[0].Elev != 3 || *g.Sats[0].Azim != 111 {
		t.Errorf("first sat: %+v", g.Sats[0])
	}
	// NMEA 4.1 appends a signal id; it must not become a phantom satellite
	s, _ = ParseSentence(sentence("GLGSV,1,1,01,65,40,100,30,1"))
	g, _ = ParseGSV(s)
	if len(g.Sats) != 1 || g.Sats[0].Sys != "GLONASS" || g.Sats[0].PRN != 65 {
		t.Errorf("signal id handling / glonass: %+v", g.Sats)
	}
	// SBAS numbers reported by a GPS talker, and a last sentence with fewer satellites
	s, _ = ParseSentence(sentence("GPGSV,1,1,02,33,10,200,25,,,,"))
	g, _ = ParseGSV(s)
	if len(g.Sats) != 1 || g.Sats[0].Sys != "SBAS" {
		t.Errorf("sbas / empty slot: %+v", g.Sats)
	}
	// header problems and out-of-range values are rejected or dropped
	if s, _ := ParseSentence(sentence("GPGSV,x,1,01,05,10,100,20")); true {
		if _, err := ParseGSV(s); err == nil {
			t.Error("bad header accepted")
		}
	}
	s, _ = ParseSentence(sentence("GPGSV,1,1,01,05,95,400,150"))
	g, _ = ParseGSV(s)
	if len(g.Sats) != 1 || g.Sats[0].Elev != nil || g.Sats[0].Azim != nil || g.Sats[0].SNR != nil {
		t.Errorf("out-of-range values kept: %+v", g.Sats)
	}
}

func TestParseGSA(t *testing.T) {
	s, _ := ParseSentence(sentence("GNGSA,A,3,02,05,13,15,24,29,,,,,,,1.8,1.0,1.5"))
	g, err := ParseGSA(s)
	if err != nil || g.Mode != 3 || len(g.Used) != 6 || g.Used[0] != 2 || g.Used[5] != 29 {
		t.Fatalf("%+v %v", g, err)
	}
	s, _ = ParseSentence(sentence("GPGSA,A,1,,,,,,,,,,,,,99.99,99.99,99.99"))
	if g, err := ParseGSA(s); err != nil || g.Mode != 1 || len(g.Used) != 0 {
		t.Errorf("no-fix GSA: %+v %v", g, err)
	}
}

func TestGrid(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		pairs    int
		want     string
	}{
		{42.213, -88.237, 3, "EN52vf"}, // the repo owner's area, verified earlier with the CLI calculator
		{42.213, -88.237, 2, "EN52"},
		{42.213, -88.237, 1, "EN"},
		{51.5, -0.12, 3, "IO91wm"},   // London
		{-33.86, 151.2, 3, "QF56od"}, // Sydney
		{35.68, 139.69, 3, "PM95uq"}, // Tokyo
		{0, 0, 3, "JJ00aa"},          // null island
		{90, 180, 3, "RR99xx"},       // north pole / antimeridian edge stays valid
		{-90, -180, 3, "AA00aa"},     // south-west corner
		// the NMEA documentation example position, worked by hand: lon 11.5167 -> J, 5, s; lat 48.1173 -> N, 8, c
		{48.1173, 11.5167, 3, "JN58sc"},
	} {
		if got := Grid(c.lat, c.lon, c.pairs); got != c.want {
			t.Errorf("Grid(%v, %v, %d) = %q, want %q", c.lat, c.lon, c.pairs, got, c.want)
		}
	}
	if Grid(91, 0, 3) != "" || Grid(0, 181, 3) != "" || Grid(math.NaN(), 0, 3) != "" {
		t.Error("invalid coordinates produced a grid")
	}
	if got := Grid(42.213, -88.237, 4); !strings.HasPrefix(got, "EN52vf") || len(got) != 8 {
		t.Errorf("extended grid = %q", got)
	}
}
