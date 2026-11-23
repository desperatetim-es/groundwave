// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"errors"
	"strconv"
)

// Satellite is one satellite the receiver reports. SNR is nil while the
// receiver is searching for it but has no signal yet.
type Satellite struct {
	Sys  string `json:"sys"` // GPS, GLONASS, Galileo, BeiDou, QZSS, SBAS
	PRN  int    `json:"prn"`
	Elev *int   `json:"elev,omitempty"` // degrees above the horizon
	Azim *int   `json:"azim,omitempty"` // degrees from true north
	SNR  *int   `json:"snr,omitempty"`  // signal strength, dB-Hz
	Used bool   `json:"used"`           // used in the current fix
}

// constellation names a satellite's system from the NMEA talker and PRN. NMEA
// numbers GPS 1-32, SBAS 33-64 and 120-158, GLONASS 65-96, QZSS 193-202,
// Galileo 301-336 (or 211-246 in u-blox numbering).
func constellation(talker string, prn int) string {
	switch talker {
	case "GL":
		return "GLONASS"
	case "GA":
		return "Galileo"
	case "GB", "BD":
		return "BeiDou"
	case "GQ":
		return "QZSS"
	}
	switch {
	case prn >= 1 && prn <= 32:
		return "GPS"
	case prn >= 33 && prn <= 64, prn >= 120 && prn <= 158:
		return "SBAS"
	case prn >= 65 && prn <= 96:
		return "GLONASS"
	case prn >= 193 && prn <= 202:
		return "QZSS"
	case prn >= 211 && prn <= 246, prn >= 301 && prn <= 336:
		return "Galileo"
	}
	return "GNSS"
}

// GSV lists satellites in view, up to four per sentence.
type GSV struct {
	Total  int // number of GSV sentences in this group
	Num    int // this sentence's number, from 1
	InView int
	Sats   []Satellite
}

// ParseGSV decodes a GSV sentence.
func ParseGSV(s Sentence) (GSV, error) {
	if s.Type != "GSV" {
		return GSV{}, errors.New("not GSV")
	}
	var g GSV
	var err1, err2, err3 error
	g.Total, err1 = strconv.Atoi(s.field(0))
	g.Num, err2 = strconv.Atoi(s.field(1))
	g.InView, err3 = strconv.Atoi(s.field(2))
	if err1 != nil || err2 != nil || err3 != nil {
		return GSV{}, errors.New("GSV: bad header")
	}
	// Each satellite is four fields: PRN, elevation, azimuth, SNR. NMEA 4.1
	// adds one trailing signal-id field, which we ignore.
	for i := 3; i+4 <= len(s.Fields); i += 4 { // a lone trailing field is the signal id
		prn, err := strconv.Atoi(s.field(i))
		if err != nil || prn <= 0 {
			continue
		}
		sat := Satellite{Sys: constellation(s.Talker, prn), PRN: prn}
		if v, err := strconv.Atoi(s.field(i + 1)); err == nil && v >= 0 && v <= 90 {
			sat.Elev = &v
		}
		if v, err := strconv.Atoi(s.field(i + 2)); err == nil && v >= 0 && v <= 359 {
			sat.Azim = &v
		}
		if v, err := strconv.Atoi(s.field(i + 3)); err == nil && v > 0 && v <= 99 {
			sat.SNR = &v // an empty field means "not tracked"
		}
		g.Sats = append(g.Sats, sat)
	}
	return g, nil
}

// GSA reports the fix mode and which satellites are used.
type GSA struct {
	Mode int   // 1 = no fix, 2 = 2D, 3 = 3D
	Used []int // PRNs
}

// ParseGSA decodes a GSA sentence.
func ParseGSA(s Sentence) (GSA, error) {
	if s.Type != "GSA" {
		return GSA{}, errors.New("not GSA")
	}
	mode, err := strconv.Atoi(s.field(1))
	if err != nil {
		return GSA{}, errors.New("GSA: bad mode")
	}
	g := GSA{Mode: mode}
	for i := 2; i <= 13; i++ {
		if prn, err := strconv.Atoi(s.field(i)); err == nil && prn > 0 {
			g.Used = append(g.Used, prn)
		}
	}
	return g, nil
}
