// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package gps reads a GPS receiver's NMEA 0183 text stream and keeps the
// latest time, position and fix quality. Nothing here talks to hardware
// directly except serial.go; the parser and the state machine are pure and
// are tested with recorded and simulated sentences.
package gps

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Sentence is one checksum-verified NMEA sentence.
type Sentence struct {
	Talker string   // "GP", "GN", "GL", ...
	Type   string   // "RMC", "GGA", ...
	Fields []string // after the sentence id, comma separated
}

var (
	errNotNMEA  = errors.New("not an NMEA sentence")
	errChecksum = errors.New("bad NMEA checksum")
)

// ParseSentence validates the framing and checksum of one line. The checksum
// is required: lines without one are treated as noise.
func ParseSentence(line string) (Sentence, error) {
	line = strings.TrimSpace(line)
	if len(line) < 9 || line[0] != '$' {
		return Sentence{}, errNotNMEA
	}
	star := strings.LastIndexByte(line, '*')
	if star < 0 || len(line)-star != 3 {
		return Sentence{}, errNotNMEA
	}
	want, err := strconv.ParseUint(line[star+1:], 16, 8)
	if err != nil {
		return Sentence{}, errNotNMEA
	}
	var sum byte
	for i := 1; i < star; i++ {
		sum ^= line[i]
	}
	if sum != byte(want) {
		return Sentence{}, errChecksum
	}
	parts := strings.Split(line[1:star], ",")
	id := parts[0]
	if len(id) < 3 {
		return Sentence{}, errNotNMEA
	}
	s := Sentence{Type: id[len(id)-3:], Fields: parts[1:]}
	s.Talker = id[:len(id)-3]
	return s, nil
}

func (s Sentence) field(i int) string {
	if i < len(s.Fields) {
		return s.Fields[i]
	}
	return ""
}

// RMC is "recommended minimum" data: UTC date and time, validity and position.
type RMC struct {
	Time   time.Time // UTC, with date
	Valid  bool      // receiver says the fix is valid (status A, mode not N)
	HasPos bool
	Lat    float64
	Lon    float64
}

// ParseRMC decodes an RMC sentence.
func ParseRMC(s Sentence) (RMC, error) {
	if s.Type != "RMC" {
		return RMC{}, fmt.Errorf("not RMC: %s", s.Type)
	}
	tod, err := parseTimeOfDay(s.field(0))
	if err != nil {
		return RMC{}, err
	}
	date, err := parseDate(s.field(8))
	if err != nil {
		return RMC{}, err
	}
	r := RMC{Time: date.Add(tod)}
	r.Valid = s.field(1) == "A" && s.field(11) != "N" && s.field(12) != "N"
	if lat, lon, ok := parsePos(s.field(2), s.field(3), s.field(4), s.field(5)); ok {
		r.HasPos, r.Lat, r.Lon = true, lat, lon
	}
	return r, nil
}

// GGA carries fix quality, satellites in use, HDOP and altitude.
type GGA struct {
	Quality int // 0 = no fix
	Sats    int
	HDOP    *float64
	AltM    *float64
	HasPos  bool
	Lat     float64
	Lon     float64
}

// ParseGGA decodes a GGA sentence.
func ParseGGA(s Sentence) (GGA, error) {
	if s.Type != "GGA" {
		return GGA{}, fmt.Errorf("not GGA: %s", s.Type)
	}
	var g GGA
	var err error
	if g.Quality, err = strconv.Atoi(s.field(5)); err != nil {
		return GGA{}, errors.New("GGA: bad fix quality")
	}
	g.Sats, _ = strconv.Atoi(s.field(6))
	if v, err := strconv.ParseFloat(s.field(7), 64); err == nil && !math.IsNaN(v) {
		g.HDOP = &v
	}
	if v, err := strconv.ParseFloat(s.field(8), 64); err == nil && !math.IsNaN(v) {
		g.AltM = &v
	}
	if lat, lon, ok := parsePos(s.field(1), s.field(2), s.field(3), s.field(4)); ok {
		g.HasPos, g.Lat, g.Lon = true, lat, lon
	}
	return g, nil
}

// parseTimeOfDay reads hhmmss or hhmmss.sss as a duration since midnight UTC.
func parseTimeOfDay(s string) (time.Duration, error) {
	if len(s) < 6 {
		return 0, errors.New("bad time")
	}
	h, e1 := strconv.Atoi(s[0:2])
	m, e2 := strconv.Atoi(s[2:4])
	sec, e3 := strconv.ParseFloat(s[4:], 64)
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec >= 61 || math.IsNaN(sec) {
		return 0, errors.New("bad time")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)), nil
}

// parseDate reads ddmmyy. GPS time began in 1980, so two-digit years 80-99 mean
// 1980-1999 and 00-79 mean 2000-2079.
func parseDate(s string) (time.Time, error) {
	if len(s) != 6 {
		return time.Time{}, errors.New("bad date")
	}
	d, e1 := strconv.Atoi(s[0:2])
	mo, e2 := strconv.Atoi(s[2:4])
	y, e3 := strconv.Atoi(s[4:6])
	if e1 != nil || e2 != nil || e3 != nil || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return time.Time{}, errors.New("bad date")
	}
	year := 2000 + y
	if y >= 80 {
		year = 1900 + y
	}
	t := time.Date(year, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d { // e.g. 31 February rolled over
		return time.Time{}, errors.New("bad date")
	}
	return t, nil
}

// parsePos converts NMEA ddmm.mmmm / dddmm.mmmm plus hemisphere into degrees.
func parsePos(lat, ns, lon, ew string) (float64, float64, bool) {
	la, ok1 := parseCoord(lat, 2)
	lo, ok2 := parseCoord(lon, 3)
	if !ok1 || !ok2 || (ns != "N" && ns != "S") || (ew != "E" && ew != "W") {
		return 0, 0, false
	}
	if ns == "S" {
		la = -la
	}
	if ew == "W" {
		lo = -lo
	}
	if la < -90 || la > 90 || lo < -180 || lo > 180 {
		return 0, 0, false
	}
	return la, lo, true
}

func parseCoord(s string, degDigits int) (float64, bool) {
	if len(s) <= degDigits {
		return 0, false
	}
	deg, err1 := strconv.Atoi(s[:degDigits])
	min, err2 := strconv.ParseFloat(s[degDigits:], 64)
	if err1 != nil || err2 != nil || min < 0 || min >= 60 || math.IsNaN(min) {
		return 0, false
	}
	return float64(deg) + min/60, true
}

// Checksum returns the two-digit hex checksum for the body of a sentence
// (the text between '$' and '*'). Used to build simulated sentences.
func Checksum(body string) string {
	var sum byte
	for i := 0; i < len(body); i++ {
		sum ^= body[i]
	}
	return fmt.Sprintf("%02X", sum)
}
