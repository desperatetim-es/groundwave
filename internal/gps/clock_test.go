// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"io"
	"testing"
	"time"
)

func within(t *testing.T, name string, got, want time.Time, tol time.Duration) {
	t.Helper()
	if d := got.Sub(want); d < -tol || d > tol {
		t.Errorf("%s = %v, want %v (±%v)", name, got.Format("15:04:05.000"), want.Format("15:04:05.000"), tol)
	}
}

func TestTimeAtFollowsTheFixAndAdvances(t *testing.T) {
	clock := fixedClock() // 14:23:05.300; the receiver's sentence says 14:23:05.00
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)

	if _, ok, _ := m.TimeAt(clock.Now()); ok {
		t.Fatal("served time with no receiver")
	}
	m.Start("COM9", 9600)
	fp.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })

	// The sentence took ~75 ms to arrive at 9600 baud, so receiver UTC is 14:23:05.075 "now".
	got, ok, disp := m.TimeAt(clock.Now())
	if !ok {
		t.Fatal("no time despite a fix")
	}
	within(t, "time now", got, time.Date(2026, 11, 12, 14, 23, 5, 75_000_000, time.UTC), 20*time.Millisecond)
	if disp < 300*time.Millisecond || disp > 400*time.Millisecond {
		t.Errorf("dispersion = %v", disp)
	}
	// two seconds later it has advanced by two seconds
	clock.Advance(2 * time.Second)
	fp.send(nmea("GNRMC,142307.00,A,4213.0000,N,08814.0000,W,0.0,0.0,121126,,,A"))
	time.Sleep(20 * time.Millisecond)
	got, _, _ = m.TimeAt(clock.Now())
	within(t, "two seconds later", got, time.Date(2026, 11, 12, 14, 23, 7, 75_000_000, time.UTC), 20*time.Millisecond)
	// and the snapshot's offset is consistent with it
	s := m.Snapshot()
	if s.OffsetMS == nil || *s.OffsetMS < -240 || *s.OffsetMS > -210 {
		t.Errorf("offset = %v", s.OffsetMS)
	}
	// once data stops arriving it must stop being served
	clock.Advance(6 * time.Second)
	if _, ok, _ := m.TimeAt(clock.Now()); ok {
		t.Error("stale time was still served")
	}
}

func TestTimeAtIsWithheldWhenItCannotBeTrusted(t *testing.T) {
	// void fix: the receiver has a time but says it is not valid
	fp := newFakePort()
	clock := fixedClock()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	fp.send(nmea(rmcVoid))
	waitFor(t, 2*time.Second, "acquiring", func() bool { return m.Snapshot().Status == StatusAcquiring })
	if _, ok, _ := m.TimeAt(clock.Now()); ok {
		t.Error("served the time of a void fix")
	}

	// a date 20 years off (week-number rollover): the time of day may be fine but the date is not
	fp2 := newFakePort()
	m2 := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp2, nil }, listOf("COM9"), "")
	defer m2.Stop(false)
	m2.Start("COM9", 9600)
	fp2.send(nmea("GNRMC,142305.00,A,4213.0000,N,08814.0000,W,0.0,0.0,300406,,,A"))
	waitFor(t, 2*time.Second, "fix", func() bool { return m2.Snapshot().Status == StatusFix })
	if _, ok, _ := m2.TimeAt(clock.Now()); ok {
		t.Error("served a time whose date is 20 years wrong")
	}

	// the port goes away
	fp3 := newFakePort()
	m3 := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp3, nil }, listOf("COM9"), "")
	defer m3.Stop(false)
	m3.Start("COM9", 9600)
	fp3.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m3.Snapshot().Status == StatusFix })
	m3.Stop(false)
	if _, ok, _ := m3.TimeAt(clock.Now()); ok {
		t.Error("served time after the receiver was disconnected")
	}
}

func TestTimeAtAppliesTheLeapSecondCorrection(t *testing.T) {
	u := &ubxPort{fakePort: newFakePort()} // the real dongle: 16 s in use, unverified
	m := ubloxFix(t, u)
	waitFor(t, 3*time.Second, "unverified", func() bool { return m.Snapshot().UTCStatus == UTCUnverified })
	got, ok, _ := m.TimeAt(fixedClock().Now())
	if !ok {
		t.Fatal("no time")
	}
	within(t, "corrected time", got, time.Date(2026, 11, 12, 14, 23, 3, 75_000_000, time.UTC), 20*time.Millisecond)

	v := &ubxPort{fakePort: newFakePort(), leap: 18, valid: 0b111}
	m2 := ubloxFix(t, v)
	waitFor(t, 3*time.Second, "verified", func() bool { return m2.Snapshot().UTCStatus == UTCVerified })
	got, _, _ = m2.TimeAt(fixedClock().Now())
	within(t, "verified time", got, time.Date(2026, 11, 12, 14, 23, 5, 75_000_000, time.UTC), 20*time.Millisecond)
}

func TestDispersionGrowsWhenFixesDisagree(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	fp.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })
	// one second later a second report is 400 ms inconsistent with the first
	clock.Advance(time.Second)
	fp.send(nmea("GNRMC,142306.40,A,4213.0000,N,08814.0000,W,0.0,0.0,121126,,,A"))
	time.Sleep(30 * time.Millisecond)
	_, ok, disp := m.TimeAt(clock.Now())
	if !ok || disp < 650*time.Millisecond {
		t.Errorf("dispersion = %v, want at least ~700 ms (300 ms base + 400 ms spread)", disp)
	}
}

func TestMedianInt(t *testing.T) {
	if got := medianInt([]int64{5, 1, 3}); got != 3 {
		t.Errorf("odd = %d", got)
	}
	if got := medianInt([]int64{1, 2, 3, 10}); got != 2 {
		t.Errorf("even = %d", got)
	}
	// one wild sample cannot move the estimate
	if got := medianInt([]int64{100, 101, 99, 5_000_000, 100}); got != 100 {
		t.Errorf("outlier = %d", got)
	}
}

func TestTimeAtFromTheSimulatorTracksTheRealClock(t *testing.T) {
	m := NewManager(DefaultOptions("", true))
	defer m.Stop(false)
	if err := m.Start(SimulatorPort, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 6*time.Second, "simulated fix", func() bool { return m.Snapshot().Status == StatusFix })
	for i := 0; i < 3; i++ {
		now := time.Now()
		got, ok, _ := m.TimeAt(now)
		if !ok {
			t.Fatal("no time from the simulator")
		}
		within(t, "simulated receiver vs real clock", got, now.UTC(), 300*time.Millisecond)
		time.Sleep(400 * time.Millisecond)
	}
}
