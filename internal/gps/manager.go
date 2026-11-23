// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status values reported to the UI.
const (
	StatusStopped    = "stopped"    // no receiver chosen
	StatusConnecting = "connecting" // opening the port
	StatusError      = "error"      // cannot open it, or it went away; will keep retrying
	StatusNoData     = "no_data"    // port is open but no NMEA is arriving
	StatusAcquiring  = "acquiring"  // NMEA arriving, but no valid fix yet
	StatusFix        = "fix"        // valid, current fix
)

// SimulatorPort is the name of the built-in test receiver (see Simulator).
const SimulatorPort = "simulator"

var (
	ErrUnknownPort = errors.New("that serial port is not on this computer's list; plug the receiver in and refresh the list")
	ErrBadBaud     = errors.New("unsupported baud rate")
)

var autoBauds = []int{9600, 4800, 38400, 115200}

func validBaud(b int) bool {
	for _, v := range []int{4800, 9600, 19200, 38400, 57600, 115200} {
		if v == b {
			return true
		}
	}
	return false
}

// PortInfo describes one serial port the operating system lists.
type PortInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	USB         bool   `json:"usb"`
	VID         string `json:"vid,omitempty"`
	PID         string `json:"pid,omitempty"`
}

// Found is a port that produced valid NMEA during a scan.
type Found struct {
	Port        string `json:"port"`
	Baud        int    `json:"baud"`
	Description string `json:"description"`
}

// Opener opens a serial port for reading. Reads should time out (returning 0,
// nil) so the reader can notice cancellation.
type Opener func(name string, baud int) (io.ReadCloser, error)

// Lister lists the serial ports the OS knows about.
type Lister func() ([]PortInfo, error)

// Options configures a Manager. Zero values pick sensible defaults, except
// Open and List, which are required.
type Options struct {
	ConfigPath   string // JSON file remembering the chosen receiver ("" = don't persist)
	Open         Opener
	List         Lister
	Now          func() time.Time
	RetryDelay   time.Duration // between reconnect attempts (default 3 s)
	DetectWindow time.Duration // listening time per baud rate when detecting (default 2.5 s)
	StaleAfter   time.Duration // data older than this no longer counts (default 5 s)
	PollEvery    time.Duration // how often to ask a u-blox receiver for satellite status (default 2 s)
}

// Snapshot is the current state, safe to marshal as JSON.
type Snapshot struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Port    string `json:"port,omitempty"`
	Baud    int    `json:"baud,omitempty"`

	// UTC is the time in the last valid fix sentence. OffsetMS is GPS time minus
	// this computer's clock, in milliseconds: add it to the system clock to get
	// GPS time. AgeMS is how long ago that sentence arrived.
	UTC         *time.Time `json:"utc,omitempty"`
	OffsetMS    *float64   `json:"offset_ms,omitempty"`
	AgeMS       *int64     `json:"age_ms,omitempty"`
	DateSuspect bool       `json:"date_suspect,omitempty"`

	Lat     *float64 `json:"lat,omitempty"`
	Lon     *float64 `json:"lon,omitempty"`
	Grid    string   `json:"grid,omitempty"`
	AltM    *float64 `json:"alt_m,omitempty"`
	Sats    *int     `json:"sats,omitempty"`
	HDOP    *float64 `json:"hdop,omitempty"`
	Quality *int     `json:"quality,omitempty"`

	// Satellites the receiver reports (from NMEA GSV/GSA, or from a u-blox
	// status request when the receiver does not print satellite sentences).
	Satellites []Satellite `json:"satellites,omitempty"`
	SatsInView int         `json:"sats_in_view,omitempty"`
	SatsSource string      `json:"sats_source,omitempty"` // "nmea" or "ublox"
	// Device details, known only for u-blox receivers we have asked.
	Version  *Version  `json:"version,omitempty"`
	Hardware *Hardware `json:"hardware,omitempty"`

	// UTC verification. A receiver that has not yet received the current
	// leap-second count from the satellites uses a built-in default, which makes
	// its UTC wrong by whole seconds. For u-blox receivers we ask whether that is
	// the case. UTC and OffsetMS already include CorrectionMS; RawOffsetMS is
	// what the receiver itself reported.
	UTCStatus     string   `json:"utc_status,omitempty"` // "verified", "unverified" or "unknown"
	LeapUsed      *int     `json:"leap_used,omitempty"`  // leap seconds the receiver is using
	PublishedLeap int      `json:"published_leap,omitempty"`
	CorrectionMS  *float64 `json:"correction_ms,omitempty"` // added to the receiver's time; nil when none
	RawOffsetMS   *float64 `json:"raw_offset_ms,omitempty"`
}

type phase int

const (
	phaseIdle phase = iota
	phaseConnecting
	phaseError
	phaseConnected
)

// Manager owns the connection to one receiver and the latest decoded state.
type Manager struct {
	o Options

	ctl sync.Mutex // serialises Start / Stop

	mu      sync.Mutex // guards everything below
	port    string
	baud    int // requested (0 = auto)
	usedBd  int // actual baud while connected
	phase   phase
	errMsg  string
	cancel  context.CancelFunc
	done    chan struct{}
	scanBus sync.Mutex

	lastData  time.Time // any valid sentence
	lastValid time.Time // last valid RMC
	fixTime   time.Time
	origin    time.Time // monotonic reference point for the clock estimate
	anchors   []int64   // for each fix: receiver UTC at origin, in ns (see utcAtLocked)
	suspect   bool
	hasPos    bool
	lat, lon  float64
	altM      *float64
	sats      *int
	hdop      *float64
	quality   *int

	portVID  string              // USB vendor id of the connected port, if known
	nmeaSats map[string]satEntry // latest satellite reports from GSV, by system and PRN
	usedPRN  map[int]time.Time   // PRNs named in GSA, with the time they were last seen
	lastGSV  time.Time           // last GSV sentence
	ubxSats  []Satellite         // latest answer to a NAV-SVINFO request
	ubxSatAt time.Time
	hw       *Hardware
	ver      *Version
	leap     *timeGPS // the receiver's leap-second status (u-blox only)
	utcValid bool     // NAV-TIMEUTC says the receiver has valid UTC data
}

type satEntry struct {
	Satellite
	seen time.Time
}

// NewManager builds a Manager. It does not open anything.
func NewManager(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = 3 * time.Second
	}
	if o.DetectWindow <= 0 {
		o.DetectWindow = 2500 * time.Millisecond
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 5 * time.Second
	}
	if o.PollEvery <= 0 {
		o.PollEvery = 2 * time.Second
	}
	return &Manager{o: o, origin: o.Now()}
}

// Ports lists serial ports.
func (m *Manager) Ports() ([]PortInfo, error) {
	ports, err := m.o.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Name < ports[j].Name })
	return ports, nil
}

func (m *Manager) portInfo(name string) (PortInfo, bool) {
	ports, err := m.o.List()
	if err != nil {
		return PortInfo{}, false
	}
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return PortInfo{}, false
}

// Start connects to the given port (baud 0 = detect) and remembers the choice.
// Only ports the operating system lists are accepted, so a request can never
// make the program open an arbitrary file path.
func (m *Manager) Start(port string, baud int) error {
	if _, ok := m.portInfo(port); !ok {
		return ErrUnknownPort
	}
	if baud != 0 && !validBaud(baud) {
		return ErrBadBaud
	}
	m.begin(port, baud)
	if port != SimulatorPort { // the test receiver only exists with --gps-sim, so never remember it
		m.saveConfig(port, baud)
	}
	return nil
}

func (m *Manager) begin(port string, baud int) {
	m.ctl.Lock()
	defer m.ctl.Unlock()
	m.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.port, m.baud, m.usedBd = port, baud, 0
	m.phase, m.errMsg = phaseConnecting, ""
	m.cancel, m.done = cancel, done
	m.resetFixLocked()
	m.mu.Unlock()
	go m.run(ctx, port, baud, done)
}

// Resume reconnects to the receiver saved by a previous run, if any. It never
// scans or touches other ports.
func (m *Manager) Resume() {
	port, baud, ok := m.loadConfig()
	if !ok {
		return
	}
	m.begin(port, baud)
}

// Stop disconnects. With forget set, the saved choice is also removed.
func (m *Manager) Stop(forget bool) {
	m.ctl.Lock()
	m.stopLocked()
	m.mu.Lock()
	m.port, m.baud, m.usedBd, m.phase, m.errMsg = "", 0, 0, phaseIdle, ""
	m.resetFixLocked()
	m.mu.Unlock()
	m.ctl.Unlock()
	if forget && m.o.ConfigPath != "" {
		os.Remove(m.o.ConfigPath)
	}
}

func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func (m *Manager) resetFixLocked() {
	m.lastData, m.lastValid, m.fixTime = time.Time{}, time.Time{}, time.Time{}
	m.anchors, m.suspect, m.hasPos = nil, false, false
	m.altM, m.sats, m.hdop, m.quality = nil, nil, nil, nil
	m.nmeaSats, m.usedPRN, m.lastGSV = nil, nil, time.Time{}
	m.ubxSats, m.ubxSatAt, m.hw, m.ver = nil, time.Time{}, nil, nil
	m.leap, m.utcValid = nil, false
}

// ---- persistence --------------------------------------------------------

type savedConfig struct {
	Port string `json:"port"`
	Baud int    `json:"baud"`
}

func (m *Manager) saveConfig(port string, baud int) {
	if m.o.ConfigPath == "" {
		return
	}
	b, _ := json.MarshalIndent(savedConfig{Port: port, Baud: baud}, "", "  ")
	os.WriteFile(m.o.ConfigPath, b, 0o644) // best effort: the receiver just won't be remembered
}

func (m *Manager) loadConfig() (string, int, bool) {
	if m.o.ConfigPath == "" {
		return "", 0, false
	}
	b, err := os.ReadFile(m.o.ConfigPath)
	if err != nil {
		return "", 0, false
	}
	var c savedConfig
	if json.Unmarshal(b, &c) != nil || c.Port == "" || (c.Baud != 0 && !validBaud(c.Baud)) {
		return "", 0, false
	}
	return c.Port, c.Baud, true
}

// ---- connection loop ------------------------------------------------------

func (m *Manager) run(ctx context.Context, port string, baud int, done chan struct{}) {
	defer close(done)
	for ctx.Err() == nil {
		m.setPhase(phaseConnecting, "")
		rc, lr, used, err := m.connect(ctx, port, baud)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.setPhase(phaseError, err.Error())
			if !sleepCtx(ctx, m.o.RetryDelay) {
				return
			}
			continue
		}
		m.mu.Lock()
		m.usedBd, m.phase, m.errMsg = used, phaseConnected, ""
		m.resetFixLocked()
		m.mu.Unlock()

		stop := make(chan struct{})
		go func() { // unblock a Read that is waiting when we are cancelled
			select {
			case <-ctx.Done():
				rc.Close()
			case <-stop:
			}
		}()
		// Only a receiver positively identified as u-blox by its USB vendor id is
		// ever sent anything, and only read-only status requests.
		if pi, ok := m.portInfo(port); ok && isUblox(pi.VID) {
			if w, ok := rc.(io.Writer); ok {
				go m.poll(ctx, stop, w)
			}
		}
		err = m.readLoop(ctx, lr, used)
		close(stop)
		rc.Close()
		if ctx.Err() != nil {
			return
		}
		m.setPhase(phaseError, fmt.Sprintf("the receiver stopped responding or was unplugged (%v); trying again", err))
		if !sleepCtx(ctx, m.o.RetryDelay) {
			return
		}
	}
}

func (m *Manager) setPhase(p phase, msg string) {
	m.mu.Lock()
	m.phase, m.errMsg = p, msg
	m.mu.Unlock()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// connect opens the port, detecting the baud rate when baud is 0.
func (m *Manager) connect(ctx context.Context, port string, baud int) (io.ReadCloser, *frameReader, int, error) {
	if _, ok := m.portInfo(port); !ok {
		return nil, nil, 0, fmt.Errorf("%s is not connected; plug the receiver in", port)
	}
	if baud != 0 {
		rc, err := m.o.Open(port, baud)
		if err != nil {
			return nil, nil, 0, err
		}
		return rc, newFrameReader(rc, m.o.Now), baud, nil
	}
	for _, b := range autoBauds {
		rc, err := m.o.Open(port, b)
		if err != nil {
			return nil, nil, 0, err
		}
		lr := newFrameReader(rc, m.o.Now)
		ok, err := m.sniff(ctx, lr, m.o.DetectWindow)
		if ok {
			return rc, lr, b, nil
		}
		rc.Close()
		if ctx.Err() != nil {
			return nil, nil, 0, ctx.Err()
		}
		_ = err
	}
	return nil, nil, 0, errors.New("the port opened but no GPS data was found at 9600, 4800, 38400 or 115200 baud; check that this is the GPS receiver")
}

// sniff reports whether a valid NMEA sentence arrives within window.
func (m *Manager) sniff(ctx context.Context, lr *frameReader, window time.Duration) (bool, error) {
	deadline := time.Now().Add(window)
	for {
		fr, err := lr.next(ctx, deadline)
		if err != nil {
			return false, err
		}
		if fr.isUBX {
			continue
		}
		if _, perr := ParseSentence(fr.nmea); perr == nil {
			return true, nil
		}
	}
}

func (m *Manager) readLoop(ctx context.Context, lr *frameReader, baud int) error {
	for {
		fr, err := lr.next(ctx, time.Time{})
		if err != nil {
			return err
		}
		if fr.isUBX {
			m.handleUBX(fr)
		} else {
			m.handle(fr.nmea, fr.rx, baud)
		}
	}
}

// poll periodically asks a u-blox receiver for status. The requests are
// read-only (empty-payload UBX polls). Version is asked once; satellite and
// antenna status only while the receiver is not already printing GSV sentences.
func (m *Manager) poll(ctx context.Context, stop <-chan struct{}, w io.Writer) {
	t := time.NewTicker(m.o.PollEvery)
	defer t.Stop()
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
			}
		}
		m.mu.Lock()
		printing := !m.lastGSV.IsZero() && m.o.Now().Sub(m.lastGSV) < m.o.StaleAfter
		verified := m.leap != nil && (m.leap.LeapValid || m.utcValid)
		m.mu.Unlock()

		if first {
			// Right after connecting: ask who it is, and whether its UTC is trustworthy,
			// before the first satellite request, so the very first fix is shown correctly.
			w.Write(ubxPoll(ubxClassMON, ubxIDMonVer))
			w.Write(ubxPoll(ubxClassNAV, ubxIDTimeGPS))
			w.Write(ubxPoll(ubxClassNAV, ubxIDTimeUTC))
			first = false
			continue
		}
		if !verified { // once the receiver has confirmed its UTC data that cannot change until it restarts
			w.Write(ubxPoll(ubxClassNAV, ubxIDTimeGPS))
			w.Write(ubxPoll(ubxClassNAV, ubxIDTimeUTC))
		}
		if printing {
			continue
		}
		w.Write(ubxPoll(ubxClassNAV, ubxIDSVInfo))
		w.Write(ubxPoll(ubxClassMON, ubxIDMonHW))
	}
}

// handleUBX stores the answer to one of our status requests.
func (m *Manager) handleUBX(fr frame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case fr.class == ubxClassNAV && fr.id == ubxIDSVInfo:
		if sats, ok := parseSVInfo(fr.payload); ok {
			m.ubxSats, m.ubxSatAt = sats, fr.rx
			m.lastData = fr.rx
		}
	case fr.class == ubxClassMON && fr.id == ubxIDMonHW:
		if h, ok := parseMonHW(fr.payload); ok {
			m.hw = &h
		}
	case fr.class == ubxClassMON && fr.id == ubxIDMonVer:
		if v, ok := parseMonVer(fr.payload); ok {
			m.ver = &v
		}
	case fr.class == ubxClassNAV && fr.id == ubxIDTimeGPS:
		if t, ok := parseTimeGPS(fr.payload); ok {
			m.leap = &t
		}
	case fr.class == ubxClassNAV && fr.id == ubxIDTimeUTC:
		if v, ok := parseTimeUTC(fr.payload); ok {
			m.utcValid = v
		}
	}
}

// handle decodes one line and updates the state.
func (m *Manager) handle(line string, rx time.Time, baud int) {
	s, err := ParseSentence(line)
	if err != nil {
		return
	}
	switch s.Type {
	case "GSV":
		g, err := ParseGSV(s)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastData, m.lastGSV = rx, rx
		if m.nmeaSats == nil {
			m.nmeaSats = map[string]satEntry{}
		}
		for _, sat := range g.Sats {
			key := sat.Sys + ":" + strconv.Itoa(sat.PRN)
			// Multi-frequency receivers repeat a satellite per signal: keep the
			// strongest one seen in this epoch.
			if old, ok := m.nmeaSats[key]; ok && rx.Sub(old.seen) < 300*time.Millisecond && old.SNR != nil && (sat.SNR == nil || *sat.SNR < *old.SNR) {
				sat.SNR = old.SNR
			}
			m.nmeaSats[key] = satEntry{Satellite: sat, seen: rx}
		}
	case "GSA":
		g, err := ParseGSA(s)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastData = rx
		if m.usedPRN == nil {
			m.usedPRN = map[int]time.Time{}
		}
		for _, prn := range g.Used {
			m.usedPRN[prn] = rx
		}
	case "RMC":
		r, err := ParseRMC(s)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastData = rx
		if !r.Valid {
			return
		}
		m.lastValid, m.fixTime = rx, r.Time
		// The sentence was sent just after the top of the second and took a
		// moment to arrive. Subtract its own transmission time (10 bits per
		// byte) to estimate when it started arriving.
		tx := time.Duration(float64(len(strings.TrimRight(line, "\r\n"))+2) * 10 / float64(baud) * float64(time.Second))
		// Express the receiver's UTC as a constant: its value at a fixed monotonic
		// origin. The monotonic clock never jumps, so this stays right even while the
		// operating system steps the wall clock (for
		// example when it resyncs), unlike an offset measured against the wall clock.
		anchor := r.Time.UnixNano() - rx.Add(-tx).Sub(m.origin).Nanoseconds()
		m.anchors = append(m.anchors, anchor)
		if len(m.anchors) > 9 {
			m.anchors = m.anchors[len(m.anchors)-9:]
		}
		m.suspect = math.Abs(float64(r.Time.Sub(rx))) > float64(24*time.Hour)
		if r.HasPos {
			m.hasPos, m.lat, m.lon = true, r.Lat, r.Lon
		}
	case "GGA":
		g, err := ParseGGA(s)
		if err != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.lastData = rx
		q, n := g.Quality, g.Sats
		m.quality, m.sats, m.hdop, m.altM = &q, &n, g.HDOP, g.AltM
		if g.Quality > 0 && g.HasPos && !m.hasPos {
			m.hasPos, m.lat, m.lon = true, g.Lat, g.Lon
		}
	default:
		m.mu.Lock()
		m.lastData = rx
		m.mu.Unlock()
	}
}

func median(v []float64) float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// Snapshot returns the current state.
func (m *Manager) Snapshot() Snapshot {
	now := m.o.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Port: m.port, Baud: m.baud}
	if m.usedBd != 0 {
		s.Baud = m.usedBd
	}
	switch {
	case m.port == "":
		s.Status = StatusStopped
		return s
	case m.phase == phaseConnecting || m.phase == phaseIdle:
		s.Status = StatusConnecting
		return s
	case m.phase == phaseError:
		s.Status, s.Message = StatusError, m.errMsg
		return s
	}
	switch {
	case m.lastData.IsZero() || now.Sub(m.lastData) > m.o.StaleAfter:
		s.Status = StatusNoData
		s.Message = "The port is open, but no GPS data is arriving. Check that this is the receiver and that its baud rate is right."
	case m.lastValid.IsZero() || now.Sub(m.lastValid) > m.o.StaleAfter:
		s.Status = StatusAcquiring
		s.Message = "Receiving data, but there is no valid fix yet. Receivers need a clear view of the sky; a cold start can take several minutes."
	default:
		s.Status = StatusFix
	}
	if s.Status == StatusFix {
		est, _ := m.utcAtLocked(now) // receiver UTC now, from the monotonic anchors
		raw := float64(est.UnixNano()-now.UnixNano()) / 1e6
		status, corr := m.utcStateLocked()
		s.UTCStatus = status
		if m.leap != nil {
			s.PublishedLeap = PublishedLeapSeconds
			l := m.leap.Leap
			s.LeapUsed = &l
		}
		off := raw + corr
		t := m.fixTime.Add(time.Duration(corr * float64(time.Millisecond)))
		age := now.Sub(m.lastValid).Milliseconds()
		s.UTC, s.OffsetMS, s.AgeMS, s.DateSuspect = &t, &off, &age, m.suspect
		if corr != 0 {
			s.CorrectionMS, s.RawOffsetMS = &corr, &raw
		}
	}
	// A position from before the fix was lost must not look current.
	if m.hasPos && s.Status == StatusFix {
		la, lo := m.lat, m.lon
		s.Lat, s.Lon, s.Grid = &la, &lo, Grid(la, lo, 3)
	}
	s.AltM, s.Sats, s.HDOP, s.Quality = m.altM, m.sats, m.hdop, m.quality
	s.Satellites, s.SatsSource = m.satellitesLocked(now)
	s.SatsInView = len(s.Satellites)
	if m.ver != nil {
		v := *m.ver
		s.Version = &v
	}
	if m.hw != nil {
		h := *m.hw
		s.Hardware = &h
	}
	return s
}

// satellitesLocked returns the satellites seen recently: from NMEA GSV when the
// receiver prints it, otherwise from the last u-blox status answer.
func (m *Manager) satellitesLocked(now time.Time) ([]Satellite, string) {
	fresh := 2 * m.o.StaleAfter
	var out []Satellite
	src := "nmea"
	for _, e := range m.nmeaSats {
		if now.Sub(e.seen) > fresh {
			continue
		}
		sat := e.Satellite
		if t, ok := m.usedPRN[sat.PRN]; ok && now.Sub(t) <= fresh {
			sat.Used = true
		}
		out = append(out, sat)
	}
	if len(out) == 0 && !m.ubxSatAt.IsZero() && now.Sub(m.ubxSatAt) <= fresh {
		out = append(out, m.ubxSats...)
		src = "ublox"
	}
	if len(out) == 0 {
		return nil, ""
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sys != out[j].Sys {
			return out[i].Sys < out[j].Sys
		}
		return out[i].PRN < out[j].PRN
	})
	return out, src
}

// ---- scanning -------------------------------------------------------------

// Scan listens briefly on each serial port for NMEA and reports which ones
// carry GPS data. It opens ports the user did not pick, which can briefly
// reset some boards (many ESP32 boards restart when a port is opened), so it
// must only run on an explicit user request. The currently connected port is
// skipped.
func (m *Manager) Scan(ctx context.Context) ([]Found, error) {
	if !m.scanBus.TryLock() {
		return nil, errors.New("a scan is already running")
	}
	defer m.scanBus.Unlock()

	ports, err := m.Ports()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	busy := m.port
	m.mu.Unlock()

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		out = []Found{}
		sem = make(chan struct{}, 4)
	)
	for _, p := range ports {
		if p.Name == busy {
			continue
		}
		wg.Add(1)
		go func(p PortInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, b := range autoBauds {
				if ctx.Err() != nil {
					return
				}
				rc, err := m.o.Open(p.Name, b)
				if err != nil {
					return // busy, no permission, gone: skip this port
				}
				stop := make(chan struct{})
				go func() {
					select {
					case <-ctx.Done():
						rc.Close()
					case <-stop:
					}
				}()
				ok, _ := m.sniff(ctx, newFrameReader(rc, m.o.Now), m.o.DetectWindow)
				close(stop)
				rc.Close()
				if ok {
					mu.Lock()
					out = append(out, Found{Port: p.Name, Baud: b, Description: p.Description})
					mu.Unlock()
					return
				}
			}
		}(p)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, ctx.Err()
}

// ---- frame reader -------------------------------------------------------------

var errTimeout = errors.New("timed out waiting for data")

// frame is one complete message from the receiver: an NMEA line, or a binary
// u-blox (UBX) message (an answer to one of our status requests).
type frame struct {
	rx      time.Time // when its last byte was read
	nmea    string
	isUBX   bool
	class   byte
	id      byte
	payload []byte
}

// frameReader splits a receiver's byte stream into NMEA lines and UBX frames.
// Bytes between messages (stray CR/LF, noise) are discarded.
type frameReader struct {
	r     io.Reader
	now   func() time.Time
	buf   []byte
	chunk [512]byte
}

func newFrameReader(r io.Reader, now func() time.Time) *frameReader {
	return &frameReader{r: r, now: now}
}

// next returns the next complete message. With a non-zero deadline it gives up
// with errTimeout. The reader must return (0, nil) periodically (a read
// timeout) or the deadline cannot be noticed.
func (f *frameReader) next(ctx context.Context, deadline time.Time) (frame, error) {
	for {
		if fr, ok := f.extract(); ok {
			return fr, nil
		}
		if err := ctx.Err(); err != nil {
			return frame{}, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return frame{}, errTimeout
		}
		n, err := f.r.Read(f.chunk[:])
		if n > 0 {
			f.buf = append(f.buf, f.chunk[:n]...)
		}
		if err != nil {
			if n == 0 || err != io.EOF {
				return frame{}, err
			}
		}
	}
}

// extract removes and returns one complete message from the buffer, if there is one.
func (f *frameReader) extract() (frame, bool) {
	for len(f.buf) > 0 {
		switch f.buf[0] {
		case '$':
			i := bytes.IndexByte(f.buf, '\n')
			// A sentence never contains another '$', and is at most 82 bytes long.
			// If it does, this '$' is noise: drop it so it cannot swallow what follows.
			if next := bytes.IndexByte(f.buf[1:], '$'); (next >= 0 && (i < 0 || next+1 < i)) || i > 120 {
				f.buf = f.buf[1:]
				continue
			}
			if i < 0 {
				if len(f.buf) > 200 { // NMEA lines are at most 82 bytes: this '$' is noise
					f.buf = f.buf[1:]
					continue
				}
				return frame{}, false
			}
			line := string(f.buf[:i])
			f.buf = f.buf[i+1:]
			return frame{nmea: line, rx: f.now()}, true
		case 0xB5:
			if len(f.buf) < 2 {
				return frame{}, false
			}
			if f.buf[1] != 0x62 {
				f.buf = f.buf[1:]
				continue
			}
			if len(f.buf) < 6 {
				return frame{}, false
			}
			n := int(f.buf[4]) | int(f.buf[5])<<8
			if n > 2048 {
				f.buf = f.buf[1:]
				continue
			}
			if len(f.buf) < n+8 {
				return frame{}, false
			}
			a, c := ubxChecksum(f.buf[2 : 6+n])
			if a != f.buf[6+n] || c != f.buf[7+n] {
				f.buf = f.buf[1:]
				continue
			}
			fr := frame{isUBX: true, class: f.buf[2], id: f.buf[3], payload: append([]byte(nil), f.buf[6:6+n]...), rx: f.now()}
			f.buf = f.buf[n+8:]
			return fr, true
		default:
			f.buf = f.buf[1:]
		}
	}
	return frame{}, false
}
