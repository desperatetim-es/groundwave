// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
	"time"
)

// ---- helpers shared with manager_test.go ------------------------------------------

func (u *ubxPort) leapOr(d byte) byte {
	if u.leap != 0 {
		return u.leap
	}
	return d
}
func (u *ubxPort) validOr(d byte) byte {
	if u.valid != 0 {
		return u.valid
	}
	return d
}
func (u *ubxPort) utcOr(d byte) byte {
	if u.utc != 0 {
		return u.utc
	}
	return d
}

// The flags byte also has unused bits; 0x08 means "nothing valid" without being zero.
const noFlags = 0x08

// realTimeGPS builds a NAV-TIMEGPS payload as the real dongle sent it:
// week 2444, accuracy 47 ns, and (for the dongle) leapS 16 with valid = 0b011.
func realTimeGPS(leap, valid byte) []byte {
	p := make([]byte, 16)
	binary.LittleEndian.PutUint16(p[8:], 2444)
	p[10], p[11] = leap, valid
	binary.LittleEndian.PutUint32(p[12:], 47)
	return p
}

// realTimeUTC builds a NAV-TIMEUTC payload: accuracy 2 s, UTC date and time, valid flags.
func realTimeUTC(valid byte) []byte {
	p := make([]byte, 20)
	binary.LittleEndian.PutUint32(p[4:], 2_000_000_000)
	binary.LittleEndian.PutUint16(p[12:], 2026)
	p[14], p[15], p[16], p[17], p[18], p[19] = 11, 12, 22, 11, 55, valid
	return p
}

func isReadOnlyPoll(w []byte) bool {
	for _, id := range [][2]byte{
		{ubxClassNAV, ubxIDSVInfo}, {ubxClassNAV, ubxIDTimeGPS}, {ubxClassNAV, ubxIDTimeUTC},
		{ubxClassMON, ubxIDMonHW}, {ubxClassMON, ubxIDMonVer},
	} {
		if bytes.Equal(w, ubxPoll(id[0], id[1])) {
			return true
		}
	}
	return false
}

func (u *ubxPort) countWrites(poll []byte) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, w := range u.writes {
		if bytes.Equal(w, poll) {
			n++
		}
	}
	return n
}

// ---- decoding ---------------------------------------------------------------------

func TestParseTimeGPSAndUTC(t *testing.T) {
	// exactly what the dongle answered: 16 leap seconds, week and TOW valid, leap seconds NOT valid
	g, ok := parseTimeGPS(realTimeGPS(16, 0b011))
	if !ok || g.Leap != 16 || g.LeapValid || !g.TOWValid || !g.WeekValid {
		t.Errorf("dongle values: %+v %v", g, ok)
	}
	g, _ = parseTimeGPS(realTimeGPS(18, 0b111))
	if g.Leap != 18 || !g.LeapValid {
		t.Errorf("valid values: %+v", g)
	}
	if g, _ := parseTimeGPS(realTimeGPS(0xF6, 0b011)); g.Leap != -10 {
		t.Errorf("signed leap = %d", g.Leap)
	}
	if _, ok := parseTimeGPS(make([]byte, 10)); ok {
		t.Error("short NAV-TIMEGPS accepted")
	}
	if v, ok := parseTimeUTC(realTimeUTC(0b011)); !ok || v {
		t.Errorf("dongle UTC flag: %v %v", v, ok)
	}
	if v, ok := parseTimeUTC(realTimeUTC(0b111)); !ok || !v {
		t.Errorf("valid UTC flag: %v %v", v, ok)
	}
	if _, ok := parseTimeUTC(nil); ok {
		t.Error("nil NAV-TIMEUTC accepted")
	}
}

func TestLeapCorrectionMath(t *testing.T) {
	cases := []struct {
		name string
		in   timeGPS
		want float64
	}{
		{"the real dongle: 16 used, 18 true", timeGPS{Leap: 16, TOWValid: true, WeekValid: true}, -2000},
		{"older default of 15", timeGPS{Leap: 15, TOWValid: true, WeekValid: true}, -3000},
		{"older default of 17", timeGPS{Leap: 17, TOWValid: true, WeekValid: true}, -1000},
		{"already current but unverified", timeGPS{Leap: 18, TOWValid: true, WeekValid: true}, 0},
		{"a count above the published one is not touched", timeGPS{Leap: 19, TOWValid: true, WeekValid: true}, 0},
		{"receiver trusts its own value", timeGPS{Leap: 16, LeapValid: true, TOWValid: true, WeekValid: true}, 0},
		{"receiver has no valid time yet", timeGPS{Leap: 16}, 0},
		{"negative is implausible", timeGPS{Leap: -3, TOWValid: true, WeekValid: true}, 0},
	}
	for _, c := range cases {
		if got := leapCorrectionMS(c.in); got != c.want {
			t.Errorf("%s: correction = %v, want %v", c.name, got, c.want)
		}
	}
	if PublishedLeapSeconds != 18 {
		t.Error("the published leap-second count changed: review the correction and the docs")
	}
}

// ---- manager behaviour ----------------------------------------------------------------

// ubloxFix connects a fake u-blox and feeds it one valid RMC, returning the port and manager.
func ubloxFix(t *testing.T, u *ubxPort) *Manager {
	t.Helper()
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return u, nil }, listWithVID("1546"), "")
	t.Cleanup(func() { m.Stop(false) })
	m.Start("COM12", 9600)
	u.send(nmea(rmcFix))
	waitFor(t, 3*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })
	return m
}

func near(t *testing.T, name string, got *float64, want, tol float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", name, got, want, tol)
	}
}

func TestUnverifiedReceiverIsCorrectedByTheMissingLeapSeconds(t *testing.T) {
	// The real dongle: 16 leap seconds in use, nothing valid yet.
	u := &ubxPort{fakePort: newFakePort()}
	m := ubloxFix(t, u)
	waitFor(t, 3*time.Second, "unverified status", func() bool { return m.Snapshot().UTCStatus == UTCUnverified })
	s := m.Snapshot()
	if s.LeapUsed == nil || *s.LeapUsed != 16 || s.PublishedLeap != 18 {
		t.Errorf("leap used %v published %d", s.LeapUsed, s.PublishedLeap)
	}
	near(t, "correction", s.CorrectionMS, -2000, 0)
	near(t, "raw offset (what the receiver reported)", s.RawOffsetMS, -225, 20)
	near(t, "corrected offset", s.OffsetMS, -2225, 20)
	// the shown UTC moves back by the same two seconds
	if !s.UTC.Equal(time.Date(2026, 11, 12, 14, 23, 3, 0, time.UTC)) {
		t.Errorf("corrected UTC = %v", s.UTC)
	}
}

func TestVerifiedReceiverIsShownAsItReports(t *testing.T) {
	for name, u := range map[string]*ubxPort{
		"leap seconds valid":            {fakePort: newFakePort(), leap: 18, valid: 0b111},
		"UTC valid via NAV-TIMEUTC":     {fakePort: newFakePort(), leap: 18, valid: 0b011, utc: 0b111},
		"receiver legitimately says 16": {fakePort: newFakePort(), leap: 16, valid: 0b111},
	} {
		m := ubloxFix(t, u)
		waitFor(t, 3*time.Second, name+": verified", func() bool { return m.Snapshot().UTCStatus == UTCVerified })
		s := m.Snapshot()
		if s.CorrectionMS != nil || s.RawOffsetMS != nil {
			t.Errorf("%s: a verified receiver was corrected: %+v", name, s)
		}
		near(t, name+": offset", s.OffsetMS, -225, 20)
		m.Stop(false)
	}
}

func TestUnverifiedButAlreadyCurrentIsLeftAlone(t *testing.T) {
	u := &ubxPort{fakePort: newFakePort(), leap: 18, valid: 0b011}
	m := ubloxFix(t, u)
	waitFor(t, 3*time.Second, "status", func() bool { return m.Snapshot().UTCStatus == UTCUnverified })
	if s := m.Snapshot(); s.CorrectionMS != nil {
		t.Errorf("a receiver already using 18 was corrected: %+v", s.CorrectionMS)
	}
}

func TestReceiverThatDoesNotAnswerIsNotCorrected(t *testing.T) {
	u := &ubxPort{fakePort: newFakePort(), silent: true}
	m := ubloxFix(t, u)
	time.Sleep(100 * time.Millisecond)
	s := m.Snapshot()
	if s.UTCStatus != UTCUnknown || s.CorrectionMS != nil {
		t.Errorf("status %q correction %v", s.UTCStatus, s.CorrectionMS)
	}
	near(t, "offset", s.OffsetMS, -225, 20)
}

func TestNonUbloxReceiverIsNeverCorrectedOrQuestioned(t *testing.T) {
	fp := newFakePort()
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return fp, nil }, listWithVID("10C4"), "")
	defer m.Stop(false)
	m.Start("COM12", 9600)
	fp.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })
	s := m.Snapshot()
	if s.UTCStatus != UTCUnknown || s.CorrectionMS != nil || s.LeapUsed != nil {
		t.Errorf("status %q, correction %v, leap %v", s.UTCStatus, s.CorrectionMS, s.LeapUsed)
	}
}

func TestTheCorrectionSwitchesItselfOffWhenTheReceiverBecomesVerified(t *testing.T) {
	u := &ubxPort{fakePort: newFakePort()} // starts unverified, like the dongle
	m := ubloxFix(t, u)
	waitFor(t, 3*time.Second, "corrected", func() bool { s := m.Snapshot(); return s.CorrectionMS != nil })
	// the receiver finally decodes the leap-second message
	u.mu.Lock()
	u.leap, u.valid = 18, 0b111
	u.mu.Unlock()
	u.send(nmea(rmcFix)) // keep a fix flowing
	waitFor(t, 3*time.Second, "verified", func() bool {
		u.send(nmea(rmcFix))
		return m.Snapshot().UTCStatus == UTCVerified
	})
	if s := m.Snapshot(); s.CorrectionMS != nil {
		t.Errorf("the correction stayed on after verification: %v", *s.CorrectionMS)
	}
}

func TestStopsAskingOnceVerified(t *testing.T) {
	u := &ubxPort{fakePort: newFakePort(), leap: 18, valid: 0b111}
	m := ubloxFix(t, u)
	waitFor(t, 3*time.Second, "verified", func() bool { return m.Snapshot().UTCStatus == UTCVerified })
	time.Sleep(60 * time.Millisecond)
	before := u.countWrites(ubxPoll(ubxClassNAV, ubxIDTimeGPS))
	time.Sleep(120 * time.Millisecond)
	if after := u.countWrites(ubxPoll(ubxClassNAV, ubxIDTimeGPS)); after != before {
		t.Errorf("kept polling NAV-TIMEGPS after verification: %d -> %d", before, after)
	}
	if before < 1 {
		t.Error("never asked at all")
	}
}
