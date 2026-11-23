// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"encoding/binary"
	"strings"
)

// u-blox binary (UBX) support. Used only to ask a u-blox receiver for status it
// does not print as NMEA: all requests here are read-only polls (empty
// payloads) that never change the receiver's configuration.

const (
	ubxClassNAV = 0x01
	ubxClassMON = 0x0A

	ubxIDSVInfo = 0x30 // NAV-SVINFO: satellites and signal strength
	ubxIDMonVer = 0x04 // MON-VER: firmware and hardware versions
	ubxIDMonHW  = 0x09 // MON-HW: antenna and interference status

	ubxIDTimeGPS = 0x20 // NAV-TIMEGPS: GPS time and the leap-second count the receiver is using
	ubxIDTimeUTC = 0x21 // NAV-TIMEUTC: UTC time and whether the receiver considers it valid
)

const ubxVendorID = "1546" // USB vendor id of u-blox

func isUblox(vid string) bool { return strings.EqualFold(vid, ubxVendorID) }

func ubxChecksum(b []byte) (byte, byte) {
	var a, c byte
	for _, x := range b {
		a += x
		c += a
	}
	return a, c
}

// ubxFrame builds a complete UBX message.
func ubxFrame(class, id byte, payload []byte) []byte {
	body := make([]byte, 0, 4+len(payload))
	body = append(body, class, id, byte(len(payload)), byte(len(payload)>>8))
	body = append(body, payload...)
	a, c := ubxChecksum(body)
	return append(append([]byte{0xB5, 0x62}, body...), a, c)
}

// ubxPoll builds a poll request (a message with no payload).
func ubxPoll(class, id byte) []byte { return ubxFrame(class, id, nil) }

// ubxToSystem names the constellation for u-blox satellite numbering.
func ubxToSystem(svid int) string {
	switch {
	case svid >= 1 && svid <= 32:
		return "GPS"
	case svid >= 33 && svid <= 64:
		return "BeiDou"
	case svid >= 65 && svid <= 96:
		return "GLONASS"
	case svid >= 120 && svid <= 158:
		return "SBAS"
	case svid >= 159 && svid <= 163:
		return "BeiDou"
	case svid >= 193 && svid <= 202:
		return "QZSS"
	case svid >= 211 && svid <= 246:
		return "Galileo"
	}
	return "GNSS"
}

// parseSVInfo decodes a NAV-SVINFO payload: an 8-byte header and then 12 bytes
// per channel.
func parseSVInfo(p []byte) ([]Satellite, bool) {
	if len(p) < 8 {
		return nil, false
	}
	n := int(p[4])
	if len(p) < 8+12*n {
		return nil, false
	}
	sats := make([]Satellite, 0, n)
	for k := 0; k < n; k++ {
		b := p[8+12*k:]
		svid := int(b[1])
		if svid == 0 {
			continue
		}
		s := Satellite{Sys: ubxToSystem(svid), PRN: svid, Used: b[2]&0x01 != 0}
		if cno := int(b[4]); cno > 0 {
			s.SNR = &cno
		}
		// Elevation -91 means unknown, and the receiver then reports azimuth 0, so
		// the azimuth is only meaningful when the elevation is.
		if el := int(int8(b[5])); el >= 0 && el <= 90 {
			s.Elev = &el
			if az := int(int16(binary.LittleEndian.Uint16(b[6:8]))); az >= 0 && az <= 359 {
				s.Azim = &az
			}
		}
		sats = append(sats, s)
	}
	return sats, true
}

// timeGPS is what NAV-TIMEGPS tells us about leap seconds.
type timeGPS struct {
	Leap      int  // leap seconds (GPS minus UTC) the receiver is currently using
	LeapValid bool // true once it has received the value from the satellites
	TOWValid  bool
	WeekValid bool
}

// parseTimeGPS decodes NAV-TIMEGPS (16 bytes): iTOW, fTOW, week, leapS (signed),
// valid flags (bit 0 time-of-week, bit 1 week, bit 2 leap seconds), tAcc.
func parseTimeGPS(p []byte) (timeGPS, bool) {
	if len(p) < 16 {
		return timeGPS{}, false
	}
	v := p[11]
	return timeGPS{Leap: int(int8(p[10])), TOWValid: v&1 != 0, WeekValid: v&2 != 0, LeapValid: v&4 != 0}, true
}

// parseTimeUTC decodes the validity flag of NAV-TIMEUTC (20 bytes). Bit 2 of the
// last byte is validUTC: the receiver has the UTC parameters from the satellites.
func parseTimeUTC(p []byte) (validUTC, ok bool) {
	if len(p) < 20 {
		return false, false
	}
	return p[19]&4 != 0, true
}

// Hardware is the antenna and interference status a u-blox receiver reports.
type Hardware struct {
	Antenna      string `json:"antenna"`       // "OK", "short circuit", "no antenna connected", ...
	AntennaPower string `json:"antenna_power"` // "on", "off", "unknown"
	Noise        int    `json:"noise"`         // noise level per millisecond
	AGC          int    `json:"agc"`           // automatic gain control, 0-8191
	Jamming      int    `json:"jamming"`       // interference indicator, 0 (none) to 255
}

// parseMonHW decodes the fields we use from a MON-HW payload (68 bytes on u-blox 6/7).
func parseMonHW(p []byte) (Hardware, bool) {
	if len(p) < 54 {
		return Hardware{}, false
	}
	ant := []string{"initializing", "unknown", "OK", "short circuit", "no antenna connected"}
	pwr := []string{"off", "on", "unknown"}
	h := Hardware{
		Antenna:      "unknown",
		AntennaPower: "unknown",
		Noise:        int(binary.LittleEndian.Uint16(p[16:18])),
		AGC:          int(binary.LittleEndian.Uint16(p[18:20])),
		Jamming:      int(p[53]),
	}
	if int(p[20]) < len(ant) {
		h.Antenna = ant[p[20]]
	}
	if int(p[21]) < len(pwr) {
		h.AntennaPower = pwr[p[21]]
	}
	return h, true
}

// Version is what a u-blox receiver says about itself.
type Version struct {
	Firmware string `json:"firmware"`
	Hardware string `json:"hardware"`
	Protocol string `json:"protocol,omitempty"`
	Systems  string `json:"systems,omitempty"`
}

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// parseMonVer decodes MON-VER: 30 bytes software, 10 bytes hardware, then
// 30-byte extension strings.
func parseMonVer(p []byte) (Version, bool) {
	if len(p) < 40 {
		return Version{}, false
	}
	v := Version{Firmware: cstr(p[0:30]), Hardware: cstr(p[30:40])}
	for o := 40; o+30 <= len(p); o += 30 {
		ext := cstr(p[o : o+30])
		switch {
		case strings.HasPrefix(ext, "PROTVER"):
			v.Protocol = strings.TrimSpace(strings.TrimPrefix(ext, "PROTVER"))
		case strings.Contains(ext, "GPS") || strings.Contains(ext, "GLO"):
			v.Systems = ext
		}
	}
	return v, true
}
