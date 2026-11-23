// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package ntp

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"
)

// DefaultServers are public servers that follow UTC without leap-second
// smearing. (Some large providers smear leap seconds, which would disagree
// with UTC for a day around a leap second.)
var DefaultServers = []string{"time.cloudflare.com", "time.nist.gov", "pool.ntp.org"}

var (
	ErrTooSoon = errors.New("please wait a few seconds between time checks")
	ErrBusy    = errors.New("a time check is already running")
)

// QueryFunc asks one server for the time (see Query).
type QueryFunc func(ctx context.Context, server string, now func() time.Time) (Sample, error)

// Result is the outcome for one server, using its best (lowest round-trip) sample.
type Result struct {
	Server   string  `json:"server"`
	OK       bool    `json:"ok"`
	Error    string  `json:"error,omitempty"`
	OffsetMS float64 `json:"offset_ms,omitempty"` // server time minus this computer's clock
	DelayMS  float64 `json:"delay_ms,omitempty"`
	Stratum  int     `json:"stratum,omitempty"`
}

// State is everything the UI needs.
type State struct {
	Enabled   bool       `json:"enabled"`
	Servers   []string   `json:"servers"`
	IntervalS int        `json:"interval_s"`
	Checking  bool       `json:"checking"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	AgeMS     *int64     `json:"age_ms,omitempty"`
	Results   []Result   `json:"results,omitempty"`
	// OffsetMS is the consensus (median) of the servers that answered: positive
	// means this computer's clock is behind. SpreadMS is how far apart the
	// servers were.
	OffsetMS *float64 `json:"offset_ms,omitempty"`
	SpreadMS *float64 `json:"spread_ms,omitempty"`
	Answered int      `json:"answered,omitempty"`
	Message  string   `json:"message,omitempty"`
}

// Options configures a Checker.
type Options struct {
	ConfigPath     string // JSON file remembering the on/off choice and servers ("" = don't persist)
	Servers        []string
	Interval       time.Duration // between automatic checks (default 10 minutes)
	MinManual      time.Duration // minimum gap between manual checks (default 10 s)
	RetryAfterFail time.Duration // when nothing answered (default 60 s)
	Timeout        time.Duration // for one whole check (default 10 s)
	Samples        int           // queries per server (default 3)
	SampleGap      time.Duration // between samples (default 150 ms)
	Query          QueryFunc
	Now            func() time.Time
}

// Checker compares this computer's clock with public time servers. It only
// talks to the network when switched on or asked to check.
type Checker struct {
	o Options

	mu       sync.Mutex
	enabled  bool
	servers  []string
	checking bool
	lastRun  time.Time // when the last check started
	checked  time.Time // when the last check finished
	results  []Result
	cons     *float64
	spread   *float64
	answered int
	message  string
	cancel   context.CancelFunc
}

// NewChecker builds a Checker. It does not touch the network.
func NewChecker(o Options) *Checker {
	if len(o.Servers) == 0 {
		o.Servers = DefaultServers
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Minute
	}
	if o.MinManual <= 0 {
		o.MinManual = 10 * time.Second
	}
	if o.RetryAfterFail <= 0 {
		o.RetryAfterFail = 60 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Samples <= 0 {
		o.Samples = 3
	}
	if o.SampleGap <= 0 {
		o.SampleGap = 150 * time.Millisecond
	}
	if o.Query == nil {
		o.Query = Query
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Checker{o: o, servers: append([]string(nil), o.Servers...)}
}

// ---- persistence ----------------------------------------------------------

type saved struct {
	Enabled bool     `json:"ntp_enabled"`
	Servers []string `json:"servers,omitempty"`
}

// A host name is dot-separated labels of letters, digits and inner hyphens
// (at most 63 characters each), optionally followed by :port.
var serverRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*(:[0-9]{1,5})?$`)

// ValidServer reports whether s looks like a host name or IPv4 address with an
// optional port. Servers come only from the config file, never from requests.
func ValidServer(s string) bool { return len(s) <= 260 && serverRE.MatchString(s) }

func (c *Checker) save() {
	if c.o.ConfigPath == "" {
		return
	}
	b, _ := json.MarshalIndent(saved{Enabled: c.enabled, Servers: c.servers}, "", "  ")
	os.WriteFile(c.o.ConfigPath, b, 0o644) // best effort
}

// Resume loads the saved choice and, if it was on, starts the automatic checks.
func (c *Checker) Resume() {
	if c.o.ConfigPath == "" {
		return
	}
	b, err := os.ReadFile(c.o.ConfigPath)
	if err != nil {
		return
	}
	var s saved
	if json.Unmarshal(b, &s) != nil {
		return
	}
	c.mu.Lock()
	if len(s.Servers) > 0 && len(s.Servers) <= 8 {
		ok := true
		for _, v := range s.Servers {
			ok = ok && ValidServer(v)
		}
		if ok {
			c.servers = s.Servers
		}
	}
	c.mu.Unlock()
	if s.Enabled {
		c.SetEnabled(true)
	}
}

// ---- control ---------------------------------------------------------------

// SetEnabled switches the automatic checks on or off and remembers the choice.
func (c *Checker) SetEnabled(on bool) {
	c.mu.Lock()
	if on == c.enabled {
		c.mu.Unlock()
		return
	}
	c.enabled = on
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if on {
		ctx, cancel := context.WithCancel(context.Background())
		c.cancel = cancel
		go c.loop(ctx)
	}
	c.save()
	c.mu.Unlock()
}

// Close stops the automatic checks without changing the saved choice.
func (c *Checker) Close() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.mu.Unlock()
}

func (c *Checker) loop(ctx context.Context) {
	for {
		_, err := c.run(ctx, false)
		wait := c.o.Interval
		c.mu.Lock()
		failed := c.answered == 0
		c.mu.Unlock()
		if err != nil && !errors.Is(err, ErrBusy) || failed {
			wait = c.o.RetryAfterFail // offline or nothing answered: try again sooner
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// CheckNow runs one check on request. It works whether or not the automatic
// checks are on, but is rate limited.
func (c *Checker) CheckNow(ctx context.Context) (State, error) {
	return c.run(ctx, true)
}

func (c *Checker) run(parent context.Context, manual bool) (State, error) {
	now := c.o.Now()
	c.mu.Lock()
	if c.checking {
		c.mu.Unlock()
		return c.State(), ErrBusy
	}
	if manual && !c.lastRun.IsZero() && now.Sub(c.lastRun) < c.o.MinManual {
		c.mu.Unlock()
		return c.State(), ErrTooSoon
	}
	c.checking, c.lastRun = true, now
	servers := append([]string(nil), c.servers...)
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, c.o.Timeout)
	defer cancel()
	results := c.queryAll(ctx, servers)
	cons, spread, n := consensus(results)

	c.mu.Lock()
	c.checking = false
	c.results, c.cons, c.spread, c.answered = results, cons, spread, n
	c.checked = c.o.Now()
	c.lastRun = c.checked // the rate limit counts from when the check finished
	switch {
	case n == 0:
		c.message = "No time server answered. You may be offline, or UDP port 123 may be blocked by a firewall."
	case n == 1 && len(results) > 1:
		c.message = "Only one time server answered, so the result is less certain."
	case spread != nil && *spread > 100:
		c.message = "The time servers disagree by more than 0.1 s, so treat this reading with caution."
	default:
		c.message = ""
	}
	c.mu.Unlock()
	return c.State(), parent.Err()
}

// queryAll asks every server in parallel and keeps each one's best sample.
func (c *Checker) queryAll(ctx context.Context, servers []string) []Result {
	out := make([]Result, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv string) {
			defer wg.Done()
			out[i] = c.queryServer(ctx, srv)
		}(i, srv)
	}
	wg.Wait()
	return out
}

func (c *Checker) queryServer(ctx context.Context, srv string) Result {
	r := Result{Server: srv}
	var best *Sample
	var lastErr error
	for i := 0; i < c.o.Samples; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(c.o.SampleGap):
			}
		}
		if ctx.Err() != nil {
			break
		}
		s, err := c.o.Query(ctx, srv, c.o.Now)
		if err != nil {
			lastErr = err
			var kiss KissCode
			if errors.As(err, &kiss) { // the server told us to go away: don't ask again now
				break
			}
			continue
		}
		if best == nil || s.Delay < best.Delay {
			cp := s
			best = &cp
		}
	}
	if best == nil {
		r.Error = "no answer"
		if lastErr != nil {
			r.Error = lastErr.Error()
		}
		return r
	}
	r.OK = true
	r.OffsetMS = float64(best.Offset) / float64(time.Millisecond)
	r.DelayMS = float64(best.Delay) / float64(time.Millisecond)
	r.Stratum = best.Stratum
	return r
}

// consensus returns the median offset of the servers that answered and how
// far apart they were. The median ignores a single wrong server.
func consensus(results []Result) (offset, spread *float64, n int) {
	var v []float64
	for _, r := range results {
		if r.OK {
			v = append(v, r.OffsetMS)
		}
	}
	if len(v) == 0 {
		return nil, nil, 0
	}
	sort.Float64s(v)
	var med float64
	if len(v)%2 == 1 {
		med = v[len(v)/2]
	} else {
		med = (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	sp := v[len(v)-1] - v[0]
	if math.IsNaN(med) || math.IsNaN(sp) {
		return nil, nil, 0
	}
	return &med, &sp, len(v)
}

// State returns the current state.
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := State{
		Enabled:   c.enabled,
		Servers:   append([]string(nil), c.servers...),
		IntervalS: int(c.o.Interval / time.Second),
		Checking:  c.checking,
		Results:   append([]Result(nil), c.results...),
		OffsetMS:  c.cons,
		SpreadMS:  c.spread,
		Answered:  c.answered,
		Message:   c.message,
	}
	if !c.checked.IsZero() {
		t := c.checked
		age := c.o.Now().Sub(c.checked).Milliseconds()
		s.CheckedAt, s.AgeMS = &t, &age
	}
	return s
}
