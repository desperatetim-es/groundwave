// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package ntp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scripted answers per server, and a record of every query made.
type fakeNet struct {
	mu      sync.Mutex
	answers map[string][]any // per server: Sample or error, used in order (last repeats)
	calls   map[string]int
	total   atomic.Int32
}

func newFakeNet() *fakeNet { return &fakeNet{answers: map[string][]any{}, calls: map[string]int{}} }

func (f *fakeNet) query(ctx context.Context, server string, now func() time.Time) (Sample, error) {
	f.total.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls[server]
	f.calls[server]++
	a := f.answers[server]
	if len(a) == 0 {
		return Sample{}, errors.New("no route to host")
	}
	if i >= len(a) {
		i = len(a) - 1
	}
	switch v := a[i].(type) {
	case Sample:
		v.Server = server
		return v, nil
	case error:
		return Sample{}, v
	}
	return Sample{}, errors.New("bad script")
}

func smp(offset, delay time.Duration) Sample {
	return Sample{Offset: offset, Delay: delay, Stratum: 2}
}

func newChecker(f *fakeNet, servers []string, cfg string) *Checker {
	return NewChecker(Options{
		ConfigPath: cfg, Servers: servers, Query: f.query,
		Interval: 40 * time.Millisecond, RetryAfterFail: 15 * time.Millisecond,
		MinManual: 50 * time.Millisecond, SampleGap: time.Millisecond, Timeout: 2 * time.Second,
	})
}

func ms(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConsensusIsTheMedianAndIgnoresOneWrongServer(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{smp(ms(3510), ms(20))}
	f.answers["b"] = []any{smp(ms(3520), ms(30))}
	f.answers["c"] = []any{smp(ms(9000), ms(25))} // one server with a wildly wrong clock
	c := newChecker(f, []string{"a", "b", "c"}, "")
	st, err := c.CheckNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.OffsetMS == nil || *st.OffsetMS != 3520 || st.Answered != 3 {
		t.Fatalf("consensus = %v from %d servers, want 3520", st.OffsetMS, st.Answered)
	}
	if st.SpreadMS == nil || *st.SpreadMS != 5490 || !strings.Contains(st.Message, "disagree") {
		t.Errorf("spread %v message %q", st.SpreadMS, st.Message)
	}
	if len(st.Results) != 3 || st.CheckedAt == nil || st.AgeMS == nil {
		t.Errorf("results = %+v", st.Results)
	}
}

func TestEachServerContributesItsLowestDelaySample(t *testing.T) {
	f := newFakeNet()
	// the middle sample has the lowest round-trip time, so its offset is the one to trust
	f.answers["a"] = []any{smp(ms(500), ms(200)), smp(ms(120), ms(15)), smp(ms(900), ms(300))}
	c := newChecker(f, []string{"a"}, "")
	st, _ := c.CheckNow(context.Background())
	if st.Results[0].OffsetMS != 120 || st.Results[0].DelayMS != 15 {
		t.Errorf("result = %+v", st.Results[0])
	}
}

func TestPartialAndTotalFailureAreExplained(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{smp(ms(100), ms(20))}
	f.answers["b"] = []any{errors.New("b did not answer in time")}
	c := newChecker(f, []string{"a", "b"}, "")
	st, _ := c.CheckNow(context.Background())
	if st.Answered != 1 || !strings.Contains(st.Message, "Only one") || st.Results[1].OK || st.Results[1].Error == "" {
		t.Errorf("partial: %+v", st)
	}

	f2 := newFakeNet() // nothing answers: like being offline
	c2 := newChecker(f2, []string{"a", "b"}, "")
	st, _ = c2.CheckNow(context.Background())
	if st.OffsetMS != nil || st.Answered != 0 || !strings.Contains(st.Message, "offline") {
		t.Errorf("offline: %+v", st)
	}
}

func TestKissOfDeathStopsFurtherSamplesToThatServer(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{KissCode("RATE")}
	c := newChecker(f, []string{"a"}, "")
	st, _ := c.CheckNow(context.Background())
	if f.calls["a"] != 1 {
		t.Errorf("asked a server that said RATE %d times", f.calls["a"])
	}
	if st.Results[0].OK || !strings.Contains(st.Results[0].Error, "slow down") {
		t.Errorf("result = %+v", st.Results[0])
	}
}

func TestManualChecksAreRateLimited(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{smp(ms(1), ms(1))}
	c := newChecker(f, []string{"a"}, "")
	if _, err := c.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := f.total.Load()
	if _, err := c.CheckNow(context.Background()); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second immediate check: %v", err)
	}
	if f.total.Load() != before {
		t.Error("a rate-limited check still sent queries")
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := c.CheckNow(context.Background()); err != nil {
		t.Errorf("check after the wait: %v", err)
	}
}

func TestRateLimitCountsFromTheEndOfACheck(t *testing.T) {
	// A check that itself takes longer than the minimum gap must not let the next one in at once.
	slow := func(ctx context.Context, s string, now func() time.Time) (Sample, error) {
		time.Sleep(80 * time.Millisecond)
		return smp(0, ms(1)), nil
	}
	c := NewChecker(Options{Servers: []string{"a"}, Query: slow, Samples: 1, MinManual: 60 * time.Millisecond})
	if _, err := c.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CheckNow(context.Background()); !errors.Is(err, ErrTooSoon) {
		t.Errorf("second check right after a slow one: %v", err)
	}
}

func TestOnlyOneCheckRunsAtATime(t *testing.T) {
	release := make(chan struct{})
	slow := func(ctx context.Context, s string, now func() time.Time) (Sample, error) {
		<-release
		return smp(0, ms(1)), nil
	}
	c := NewChecker(Options{Servers: []string{"a"}, Query: slow, Samples: 1, MinManual: time.Millisecond})
	done := make(chan struct{})
	go func() { c.CheckNow(context.Background()); close(done) }()
	waitFor(t, time.Second, "check to start", func() bool { return c.State().Checking })
	time.Sleep(5 * time.Millisecond)
	if _, err := c.CheckNow(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("concurrent check: %v", err)
	}
	close(release)
	<-done
}

// The privacy promise: nothing is sent unless the user switched it on or asked.
func TestNothingIsSentWhileOff(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{smp(0, ms(1))}
	cfg := filepath.Join(t.TempDir(), "time.json")
	c := newChecker(f, []string{"a"}, cfg)
	c.Resume() // no saved file
	time.Sleep(150 * time.Millisecond)
	if f.total.Load() != 0 {
		t.Fatalf("%d queries were sent while the feature was off", f.total.Load())
	}
	c.SetEnabled(true)
	waitFor(t, time.Second, "first automatic check", func() bool { return f.total.Load() > 0 })
	c.SetEnabled(false)
	time.Sleep(30 * time.Millisecond)
	settled := f.total.Load()
	time.Sleep(150 * time.Millisecond)
	if f.total.Load() != settled {
		t.Errorf("queries continued after switching off: %d -> %d", settled, f.total.Load())
	}
}

func TestAutomaticChecksRepeatAndRetryWhenOffline(t *testing.T) {
	f := newFakeNet()
	f.answers["a"] = []any{errors.New("offline"), errors.New("offline"), errors.New("offline"), smp(ms(250), ms(5))}
	c := newChecker(f, []string{"a"}, "")
	defer c.Close()
	c.SetEnabled(true)
	waitFor(t, 2*time.Second, "a successful check after offline retries", func() bool {
		s := c.State()
		return s.OffsetMS != nil && *s.OffsetMS == 250
	})
	before := f.total.Load()
	waitFor(t, 2*time.Second, "another scheduled check", func() bool { return f.total.Load() > before })
}

func TestChoiceIsRemembered(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "time.json")
	f := newFakeNet()
	f.answers["x"] = []any{smp(0, ms(1))}

	c1 := newChecker(f, []string{"x"}, cfg)
	c1.SetEnabled(true)
	c1.Close()

	c2 := newChecker(f, []string{"x"}, cfg)
	defer c2.Close()
	f.total.Store(0)
	c2.Resume()
	if !c2.State().Enabled {
		t.Error("the on-state was not restored")
	}
	waitFor(t, time.Second, "check after resume", func() bool { return f.total.Load() > 0 })

	c2.SetEnabled(false)
	c3 := newChecker(f, []string{"x"}, cfg)
	c3.Resume()
	if c3.State().Enabled {
		t.Error("the off-state was not restored")
	}
}

func TestServersFromTheConfigFileAreValidated(t *testing.T) {
	for server, ok := range map[string]bool{
		"time.cloudflare.com": true, "pool.ntp.org": true, "0.pool.ntp.org:123": true, "192.168.1.1": true, "ntp-1.example.org": true,
		"": false, "has space.org": false, "http://time.org": false, "a/b": false, "-bad.org": false, "bad-.org": false,
		"evil.org\r\nX: y": false, strings.Repeat("a", 300): false, "[::1]:123": false,
	} {
		if ValidServer(server) != ok {
			t.Errorf("ValidServer(%q) = %v", server, !ok)
		}
	}
	cfg := filepath.Join(t.TempDir(), "time.json")
	if err := writeFile(cfg, `{"ntp_enabled": false, "servers": ["good.example.org", "bad server"]}`); err != nil {
		t.Fatal(err)
	}
	c := NewChecker(Options{ConfigPath: cfg})
	c.Resume()
	if got := c.State().Servers; len(got) != len(DefaultServers) || got[0] != DefaultServers[0] {
		t.Errorf("an invalid server list was accepted: %v", got)
	}
}

func TestDefaultServersAreUnsmearedPublicServers(t *testing.T) {
	for _, s := range DefaultServers {
		if strings.Contains(s, "google") {
			t.Errorf("%s smears leap seconds", s)
		}
		if !ValidServer(s) {
			t.Errorf("default server %q is not valid", s)
		}
	}
}

// End to end over real UDP against local fake servers.
func TestCheckerOverUDP(t *testing.T) {
	a, b, c := goodReply(), goodReply(), goodReply()
	a.skew, b.skew, c.skew = 2*time.Second, 2*time.Second+20*time.Millisecond, -1*time.Second
	ch := NewChecker(Options{Servers: []string{serve(t, a), serve(t, b), serve(t, c)}, Timeout: 3 * time.Second})
	st, err := ch.CheckNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Answered != 3 || st.OffsetMS == nil || *st.OffsetMS < 1950 || *st.OffsetMS > 2070 {
		t.Fatalf("consensus %v from %d servers", st.OffsetMS, st.Answered)
	}
}
