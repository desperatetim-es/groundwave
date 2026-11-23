// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- test doubles -----------------------------------------------------------

// fakePort is an io.ReadCloser fed by the test. Like a real serial port with a
// read timeout it returns (0, nil) when nothing is waiting.
type fakePort struct {
	in     chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFakePort() *fakePort {
	return &fakePort{in: make(chan []byte, 64), closed: make(chan struct{})}
}

func (f *fakePort) Read(p []byte) (int, error) {
	select {
	case b := <-f.in:
		return copy(p, b), nil
	case <-f.closed:
		return 0, io.EOF
	case <-time.After(5 * time.Millisecond):
		return 0, nil
	}
}
func (f *fakePort) Close() error  { f.once.Do(func() { close(f.closed) }); return nil }
func (f *fakePort) send(s string) { f.in <- []byte(s) }

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func nmea(body string) string { return "$" + body + "*" + Checksum(body) + "\r\n" }

const (
	// 14:23:05.00 UTC on 12 Nov 2026 at 42 deg 13.0' N, 88 deg 14.0' W (grid EN52vf)
	rmcFix  = "GNRMC,142305.00,A,4213.0000,N,08814.0000,W,0.0,0.0,121126,,,A"
	rmcVoid = "GNRMC,142305.00,V,,,,,,,121126,,,N"
	ggaFix  = "GNGGA,142305.00,4213.0000,N,08814.0000,W,1,09,1.1,272.0,M,-34.0,M,,"
)

func listOf(names ...string) Lister {
	return func() ([]PortInfo, error) {
		out := []PortInfo{}
		for _, n := range names {
			out = append(out, PortInfo{Name: n, Description: "test " + n})
		}
		return out, nil
	}
}

func newMgr(clock *fakeClock, open Opener, list Lister, cfg string) *Manager {
	o := Options{ConfigPath: cfg, Open: open, List: list,
		RetryDelay: 15 * time.Millisecond, DetectWindow: 120 * time.Millisecond, StaleAfter: 5 * time.Second,
		PollEvery: 20 * time.Millisecond}
	if clock != nil {
		o.Now = clock.Now
	}
	return NewManager(o)
}

func fixedClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 12, 14, 23, 5, 300_000_000, time.UTC)} // 300 ms after the GPS second
}

// ---- tests ---------------------------------------------------------------------

func TestFixFlow(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)

	if s := m.Snapshot(); s.Status != StatusStopped {
		t.Fatalf("initial status %q", s.Status)
	}
	if err := m.Start("COM9", 9600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "connected", func() bool { return m.Snapshot().Status == StatusNoData })

	fp.send(nmea(ggaFix))
	fp.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })

	s := m.Snapshot()
	if !s.UTC.Equal(time.Date(2026, 11, 12, 14, 23, 5, 0, time.UTC)) {
		t.Errorf("utc = %v", s.UTC)
	}
	// The sentence "arrived" 300 ms after its timestamp; its own transmission
	// time at 9600 baud (~75 ms) is subtracted, so GPS is ~225 ms behind the
	// computer's clock: offset = gps - system = -300 + 75.
	if s.OffsetMS == nil || *s.OffsetMS > -205 || *s.OffsetMS < -245 {
		t.Errorf("offset = %v ms, want about -225", s.OffsetMS)
	}
	if s.Grid != "EN52vf" || s.Lat == nil || math.Abs(*s.Lat-42.21667) > 0.001 || math.Abs(*s.Lon+88.23333) > 0.001 {
		t.Errorf("position: grid %q lat %v lon %v", s.Grid, s.Lat, s.Lon)
	}
	if s.Sats == nil || *s.Sats != 9 || s.HDOP == nil || *s.HDOP != 1.1 || s.AltM == nil || *s.AltM != 272 || s.Quality == nil || *s.Quality != 1 {
		t.Errorf("gga details: %+v", s)
	}
	if s.DateSuspect {
		t.Error("date wrongly flagged")
	}
	if s.AgeMS == nil || *s.AgeMS != 0 {
		t.Errorf("age = %v", s.AgeMS)
	}

	// A fix goes stale if sentences stop arriving.
	clock.Advance(6 * time.Second)
	if st := m.Snapshot().Status; st != StatusNoData {
		t.Errorf("stale status = %q, want no_data", st)
	}
	// and the old position must not be shown as if it were current
	if s := m.Snapshot(); s.Lat != nil || s.Lon != nil || s.Grid != "" {
		t.Errorf("a position from before the fix was lost is still shown: %+v %+v %q", s.Lat, s.Lon, s.Grid)
	}
}

func TestVoidFixIsAcquiring(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	waitFor(t, 2*time.Second, "connected", func() bool { return m.Snapshot().Status == StatusNoData })

	fp.send(nmea(rmcVoid))
	waitFor(t, 2*time.Second, "acquiring", func() bool { return m.Snapshot().Status == StatusAcquiring })
	s := m.Snapshot()
	if s.UTC != nil || s.OffsetMS != nil {
		t.Error("an unverified time must not be reported as GPS time")
	}
	// then it gets a fix
	fp.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })
}

func TestCorruptedAndForeignSentencesAreIgnored(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	waitFor(t, 2*time.Second, "connected", func() bool { return m.Snapshot().Status == StatusNoData })

	fp.send("garbage \x00\xff\r\n")
	fp.send("$GNRMC,142305.00,A,4213.0000,N,08814.0000,W,0.0,0.0,121126,,,A*00\r\n") // bad checksum
	time.Sleep(60 * time.Millisecond)
	if st := m.Snapshot().Status; st != StatusNoData {
		t.Fatalf("noise changed the status to %q", st)
	}
	fp.send(nmea("GPGSV,1,1,00")) // valid but irrelevant sentence: data is flowing, no fix
	waitFor(t, 2*time.Second, "acquiring", func() bool { return m.Snapshot().Status == StatusAcquiring })
}

func TestDateSuspectWhenYearIsWrong(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	// the classic GPS week-number rollover: right time of day, date ~19.6 years early
	fp.send(nmea("GNRMC,142305.00,A,4213.0000,N,08814.0000,W,0.0,0.0,300406,,,A"))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })
	if !m.Snapshot().DateSuspect {
		t.Error("a date 20 years off was not flagged")
	}
}

func TestUnknownPortRejected(t *testing.T) {
	var opened atomic.Int32
	m := newMgr(nil, func(string, int) (io.ReadCloser, error) { opened.Add(1); return newFakePort(), nil }, listOf("COM9"), "")
	defer m.Stop(false)
	for _, p := range []string{"/etc/passwd", `\\.\pipe\x`, "COM10", "", "../../dev/mem"} {
		if err := m.Start(p, 9600); !errors.Is(err, ErrUnknownPort) {
			t.Errorf("Start(%q) = %v, want ErrUnknownPort", p, err)
		}
	}
	if err := m.Start("COM9", 12345); !errors.Is(err, ErrBadBaud) {
		t.Errorf("bad baud: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if opened.Load() != 0 {
		t.Fatal("a port was opened for a rejected request")
	}
}

func TestPersistResumeForget(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gps.json")
	var opened atomic.Int32
	open := func(string, int) (io.ReadCloser, error) { opened.Add(1); return newFakePort(), nil }

	m1 := newMgr(nil, open, listOf("COM9"), cfg)
	if err := m1.Start("COM9", 4800); err != nil {
		t.Fatal(err)
	}
	m1.Stop(false) // quitting the app keeps the choice

	m2 := newMgr(nil, open, listOf("COM9"), cfg)
	m2.Resume()
	waitFor(t, 2*time.Second, "resume", func() bool { s := m2.Snapshot(); return s.Port == "COM9" && s.Baud == 4800 && s.Status == StatusNoData })
	m2.Stop(true) // forgetting removes it

	m3 := newMgr(nil, open, listOf("COM9"), cfg)
	m3.Resume()
	if s := m3.Snapshot(); s.Status != StatusStopped {
		t.Errorf("forgotten receiver came back: %+v", s)
	}
}

func TestResumeWithReceiverNotPluggedInKeepsTrying(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gps.json")
	m0 := newMgr(nil, func(string, int) (io.ReadCloser, error) { return newFakePort(), nil }, listOf("COM9"), cfg)
	m0.Start("COM9", 9600)
	m0.Stop(false)

	var plugged atomic.Bool
	list := func() ([]PortInfo, error) {
		if plugged.Load() {
			return []PortInfo{{Name: "COM9"}}, nil
		}
		return nil, nil
	}
	m := newMgr(nil, func(string, int) (io.ReadCloser, error) { return newFakePort(), nil }, list, cfg)
	defer m.Stop(false)
	m.Resume()
	waitFor(t, 2*time.Second, "not-found error", func() bool { return m.Snapshot().Status == StatusError })
	plugged.Store(true)
	waitFor(t, 2*time.Second, "connected after plug-in", func() bool { return m.Snapshot().Status == StatusNoData })
}

func TestReconnectsAfterOpenFailureAndUnplug(t *testing.T) {
	clock := fixedClock()
	var mu sync.Mutex
	var calls int
	var current *fakePort
	open := func(string, int) (io.ReadCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return nil, errors.New("COM9 is in use by another program")
		}
		current = newFakePort()
		return current, nil
	}
	m := newMgr(clock, open, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)

	waitFor(t, 2*time.Second, "error", func() bool {
		s := m.Snapshot()
		return s.Status == StatusError && s.Message != ""
	})
	waitFor(t, 2*time.Second, "reconnect", func() bool { return m.Snapshot().Status == StatusNoData })

	mu.Lock()
	p := current
	mu.Unlock()
	p.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix", func() bool { return m.Snapshot().Status == StatusFix })

	p.Close() // the receiver is unplugged: reads return EOF
	waitFor(t, 2*time.Second, "error after unplug", func() bool { return m.Snapshot().Status == StatusError })
	waitFor(t, 2*time.Second, "reconnect after unplug", func() bool { return m.Snapshot().Status == StatusNoData })
	mu.Lock()
	p = current
	mu.Unlock()
	p.send(nmea(rmcFix))
	waitFor(t, 2*time.Second, "fix again", func() bool { return m.Snapshot().Status == StatusFix })
}

// talker opens ports that emit sentences continuously, but only at the right baud.
func talker(goodBaud int, stop <-chan struct{}) Opener {
	return func(name string, baud int) (io.ReadCloser, error) {
		fp := newFakePort()
		if baud == goodBaud {
			go func() {
				tick := time.NewTicker(15 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-tick.C:
						select {
						case fp.in <- []byte(nmea(rmcFix)):
						default:
						}
					case <-fp.closed:
						return
					case <-stop:
						return
					}
				}
			}()
		}
		return fp, nil
	}
}

func TestAutoBaudDetection(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	m := newMgr(fixedClock(), talker(4800, stop), listOf("COM9"), "")
	defer m.Stop(false)
	if err := m.Start("COM9", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "fix at detected baud", func() bool {
		s := m.Snapshot()
		return s.Status == StatusFix && s.Baud == 4800
	})
}

func TestAutoBaudGivesUpWithAHelpfulMessage(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	m := newMgr(fixedClock(), talker(0, stop), listOf("COM9"), "") // never speaks
	defer m.Stop(false)
	m.Start("COM9", 0)
	waitFor(t, 5*time.Second, "detection failure", func() bool {
		s := m.Snapshot()
		return s.Status == StatusError && s.Message != ""
	})
}

func TestScanFindsOnlyTheGPS(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	var opens sync.Map
	open := func(name string, baud int) (io.ReadCloser, error) {
		opens.Store(name, true)
		if name == "COM3" {
			return talker(9600, stop)(name, baud)
		}
		if name == "COM5" {
			return nil, errors.New("access denied") // busy or no permission: skipped quietly
		}
		return newFakePort(), nil // silent
	}
	m := newMgr(fixedClock(), open, listOf("COM3", "COM4", "COM5"), "")
	found, err := m.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Port != "COM3" || found[0].Baud != 9600 {
		t.Fatalf("found = %+v", found)
	}
}

func TestScanSkipsTheConnectedPort(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	var opens sync.Map
	open := func(name string, baud int) (io.ReadCloser, error) {
		opens.Store(name, true)
		return talker(9600, stop)(name, baud)
	}
	m := newMgr(fixedClock(), open, listOf("COM3", "COM4"), "")
	defer m.Stop(false)
	m.Start("COM3", 9600)
	waitFor(t, 2*time.Second, "connected", func() bool { return m.Snapshot().Status == StatusFix }) // the connection's own open is done
	opens.Delete("COM3")
	found, _ := m.Scan(context.Background())
	if _, touched := opens.Load("COM3"); touched {
		t.Error("scan re-opened the port that is already connected")
	}
	if len(found) != 1 || found[0].Port != "COM4" {
		t.Errorf("found = %+v", found)
	}
}

func TestScanCanBeCancelled(t *testing.T) {
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return newFakePort(), nil }, listOf("COM3"), "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := m.Scan(ctx); err == nil {
		t.Error("expected a cancellation error")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("scan did not stop promptly")
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{5, 1, 3}); got != 3 {
		t.Errorf("odd median = %v", got)
	}
	if got := median([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("even median = %v", got)
	}
	// one wild sample (e.g. a delayed read) must not move the estimate much
	if got := median([]float64{-100, -101, -99, 900, -100}); got != -100 {
		t.Errorf("outlier median = %v", got)
	}
}

// ---- satellites ------------------------------------------------------------------

func TestSatellitesFromNMEA(t *testing.T) {
	clock := fixedClock()
	fp := newFakePort()
	m := newMgr(clock, func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)

	fp.send(nmea("GPGSV,2,1,05,02,64,110,42,05,48,200,38,09,22,305,,13,71,040,45"))
	fp.send(nmea("GPGSV,2,2,05,15,35,255,29"))
	fp.send(nmea("GLGSV,1,1,01,66,30,090,27"))
	fp.send(nmea("GNGSA,A,3,02,13,66,,,,,,,,,,1.8,1.0,1.5"))
	waitFor(t, 2*time.Second, "satellites", func() bool { return m.Snapshot().SatsInView == 6 })

	s := m.Snapshot()
	if s.SatsSource != "nmea" {
		t.Errorf("source = %q", s.SatsSource)
	}
	by := map[string]Satellite{}
	for _, sat := range s.Satellites {
		by[sat.Sys+":"+itoa(sat.PRN)] = sat
	}
	if g := by["GPS:2"]; g.SNR == nil || *g.SNR != 42 || !g.Used {
		t.Errorf("GPS 2: %+v", g)
	}
	if g := by["GPS:9"]; g.SNR != nil || g.Used {
		t.Errorf("GPS 9 has no signal and is not used: %+v", g)
	}
	if g := by["GLONASS:66"]; g.SNR == nil || !g.Used {
		t.Errorf("GLONASS 66: %+v", g)
	}
	if g := by["GPS:5"]; g.Used {
		t.Errorf("GPS 5 was not in the GSA used list: %+v", g)
	}
	// reports expire when the receiver stops sending them
	clock.Advance(11 * time.Second)
	if n := m.Snapshot().SatsInView; n != 0 {
		t.Errorf("stale satellites still shown: %d", n)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestMultiFrequencyGSVKeepsTheStrongestSignal(t *testing.T) {
	fp := newFakePort()
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return fp, nil }, listOf("COM9"), "")
	defer m.Stop(false)
	m.Start("COM9", 9600)
	fp.send(nmea("GPGSV,1,1,01,07,50,100,40,1"))
	fp.send(nmea("GPGSV,1,1,01,07,50,100,18,6")) // same satellite on a weaker second signal
	waitFor(t, 2*time.Second, "satellite", func() bool { return m.Snapshot().SatsInView == 1 })
	time.Sleep(30 * time.Millisecond)
	s := m.Snapshot()
	if len(s.Satellites) != 1 || s.Satellites[0].SNR == nil || *s.Satellites[0].SNR != 40 {
		t.Errorf("satellites = %+v", s.Satellites)
	}
}

// ubxPort is a fake u-blox receiver: it records what is written to it and
// answers the read-only status requests like the real one does.
type ubxPort struct {
	*fakePort
	mu     sync.Mutex
	writes [][]byte
	leap   byte // the leap-second count it claims to be using (default 16, like the real dongle)
	valid  byte // NAV-TIMEGPS valid flags: bit0 TOW, bit1 week, bit2 leap seconds (default 0b011)
	utc    byte // NAV-TIMEUTC valid flags (default 0b011: UTC not valid)
	silent bool // never answers the time polls
}

func (u *ubxPort) Write(p []byte) (int, error) {
	u.mu.Lock()
	u.writes = append(u.writes, append([]byte(nil), p...))
	u.mu.Unlock()
	switch {
	case bytes.Equal(p, ubxPoll(ubxClassNAV, ubxIDSVInfo)):
		u.fakePort.in <- ubxFrame(ubxClassNAV, ubxIDSVInfo, realSVInfo())
	case bytes.Equal(p, ubxPoll(ubxClassMON, ubxIDMonHW)):
		u.fakePort.in <- ubxFrame(ubxClassMON, ubxIDMonHW, realMonHW())
	case bytes.Equal(p, ubxPoll(ubxClassMON, ubxIDMonVer)):
		u.fakePort.in <- ubxFrame(ubxClassMON, ubxIDMonVer, realMonVer())
	case bytes.Equal(p, ubxPoll(ubxClassNAV, ubxIDTimeGPS)) && !u.silent:
		u.fakePort.in <- ubxFrame(ubxClassNAV, ubxIDTimeGPS, realTimeGPS(u.leapOr(16), u.validOr(0b011)))
	case bytes.Equal(p, ubxPoll(ubxClassNAV, ubxIDTimeUTC)) && !u.silent:
		u.fakePort.in <- ubxFrame(ubxClassNAV, ubxIDTimeUTC, realTimeUTC(u.utcOr(0b011)))
	}
	return len(p), nil
}

func (u *ubxPort) wrote(poll []byte) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, w := range u.writes {
		if bytes.Equal(w, poll) {
			return true
		}
	}
	return false
}

func (u *ubxPort) writeCount() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.writes) }

func listWithVID(vid string) Lister {
	return func() ([]PortInfo, error) {
		return []PortInfo{{Name: "COM12", Description: "USB Serial Device", USB: true, VID: vid, PID: "01A7"}}, nil
	}
}

func TestUbloxStatusWhenReceiverPrintsNoSatelliteSentences(t *testing.T) {
	// The situation from the field: valid GGA/RMC with no fix, no GSV at all.
	up := &ubxPort{fakePort: newFakePort()}
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return up, nil }, listWithVID("1546"), "")
	defer m.Stop(false)
	m.Start("COM12", 9600)
	up.send(nmea("GPGGA,,,,,,0,00,99.99,,,,,,"))
	up.send(nmea("GPRMC,,V,,,,,,,,,,N"))

	waitFor(t, 3*time.Second, "u-blox satellites", func() bool {
		s := m.Snapshot()
		return s.SatsSource == "ublox" && s.SatsInView == 4 && s.Hardware != nil && s.Version != nil
	})
	s := m.Snapshot()
	for _, sat := range s.Satellites {
		if sat.SNR != nil || sat.Used {
			t.Errorf("a searching satellite was shown as tracked: %+v", sat)
		}
	}
	if s.Hardware.Antenna != "OK" || s.Hardware.Jamming != 247 || s.Hardware.Noise != 114 {
		t.Errorf("hardware = %+v", s.Hardware)
	}
	if s.Version.Firmware != "1.00 (59842)" || s.Version.Hardware != "00070000" {
		t.Errorf("version = %+v", s.Version)
	}
	if s.Status != StatusAcquiring {
		t.Errorf("status = %q, want acquiring (data flowing, no fix)", s.Status)
	}
	// Everything we ever sent was one of the three read-only polls.
	up.mu.Lock()
	defer up.mu.Unlock()
	for _, w := range up.writes {
		if !isReadOnlyPoll(w) {
			t.Errorf("unexpected write: % X", w)
		}
	}
}

func TestNothingIsWrittenToDevicesThatAreNotUblox(t *testing.T) {
	for _, vid := range []string{"", "10C4" /* Silicon Labs, e.g. an ESP32 board */, "0403", "1A86"} {
		up := &ubxPort{fakePort: newFakePort()}
		m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return up, nil }, listWithVID(vid), "")
		m.Start("COM12", 9600)
		up.send(nmea("GPRMC,,V,,,,,,,,,,N"))
		time.Sleep(150 * time.Millisecond) // several poll intervals
		m.Stop(false)
		if n := up.writeCount(); n != 0 {
			t.Errorf("vendor id %q: %d bytes-writes were sent to a device that is not a u-blox receiver", vid, n)
		}
	}
}

func TestNoStatusPollsWhenReceiverAlreadyPrintsGSV(t *testing.T) {
	up := &ubxPort{fakePort: newFakePort()}
	m := newMgr(fixedClock(), func(string, int) (io.ReadCloser, error) { return up, nil }, listWithVID("1546"), "")
	defer m.Stop(false)
	m.Start("COM12", 9600)
	stop := make(chan struct{})
	defer close(stop)
	go func() { // a receiver that sends GSV every few ms
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				select {
				case up.fakePort.in <- []byte(nmea("GPGSV,1,1,01,07,50,100,40")):
				default:
				}
			case <-stop:
				return
			}
		}
	}()
	waitFor(t, 2*time.Second, "version poll", func() bool { return up.wrote(ubxPoll(ubxClassMON, ubxIDMonVer)) })
	time.Sleep(150 * time.Millisecond)
	if up.wrote(ubxPoll(ubxClassNAV, ubxIDSVInfo)) || up.wrote(ubxPoll(ubxClassMON, ubxIDMonHW)) {
		t.Error("polled for satellite status although the receiver already prints it")
	}
	if m.Snapshot().SatsSource != "nmea" {
		t.Errorf("source = %q, want nmea", m.Snapshot().SatsSource)
	}
}

func TestSimulatorIsNeverRemembered(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gps.json")
	m := NewManager(DefaultOptions(cfg, true))
	defer m.Stop(false)
	if err := m.Start(SimulatorPort, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Fatal("the simulator choice was saved and would break the next normal launch")
	}
}

func TestSimulatorGivesAFix(t *testing.T) {
	o := DefaultOptions("", true)
	m := NewManager(o)
	defer m.Stop(false)
	if err := m.Start(SimulatorPort, 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 6*time.Second, "simulated fix", func() bool { return m.Snapshot().Status == StatusFix })
	s := m.Snapshot()
	if s.Grid != "EM29ma" {
		t.Errorf("grid = %q, want EM29ma (39.0 N, 95.0 W)", s.Grid)
	}
	if s.OffsetMS == nil || math.Abs(*s.OffsetMS) > 300 {
		t.Errorf("simulated GPS should track the system clock closely, offset = %v", s.OffsetMS)
	}
	if s.DateSuspect {
		t.Error("simulated date flagged")
	}
	waitFor(t, 4*time.Second, "simulated satellites", func() bool { return m.Snapshot().SatsInView == 8 })
	used := 0
	for _, sat := range m.Snapshot().Satellites {
		if sat.Used {
			used++
		}
	}
	if used != 6 {
		t.Errorf("simulated satellites used = %d, want 6", used)
	}
}
