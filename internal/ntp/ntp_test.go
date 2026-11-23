// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package ntp

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

const ntpUnixDelta = 2208988800 // seconds from 1900-01-01 to 1970-01-01

func putNTP(b []byte, t time.Time) {
	secs := uint64(t.Unix() + ntpUnixDelta)
	frac := uint64(t.Nanosecond()) << 32 / 1_000_000_000
	binary.BigEndian.PutUint32(b[0:4], uint32(secs))
	binary.BigEndian.PutUint32(b[4:8], uint32(frac))
}

// reply describes how a fake server answers.
type reply struct {
	skew       time.Duration // server clock minus real time
	stratum    byte
	li         byte
	mode       byte
	version    byte
	kiss       string
	badOrigin  bool
	zeroStamps bool
	short      bool
	delay      time.Duration // how long it sits on the request
	drop       bool
	t3Before   bool // transmit timestamp earlier than receive timestamp
}

func goodReply() reply { return reply{stratum: 2, mode: modeServer, version: 4} }

// serve starts a fake NTP server on a loopback UDP port and returns host:port.
func serve(t *testing.T, r reply) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < packetSize || r.drop {
				continue
			}
			req := append([]byte(nil), buf[:n]...)
			go func() {
				time.Sleep(r.delay)
				now := time.Now().Add(r.skew)
				out := make([]byte, packetSize)
				out[0] = r.li<<6 | r.version<<3 | r.mode
				out[1] = r.stratum
				copy(out[24:32], req[40:48]) // echo the client's transmit time as origin
				if r.badOrigin {
					out[24] ^= 0xFF
				}
				if r.kiss != "" {
					copy(out[12:16], r.kiss)
				}
				putNTP(out[32:40], now)
				putNTP(out[40:48], now)
				if r.t3Before {
					putNTP(out[40:48], now.Add(-time.Second))
				}
				if r.zeroStamps {
					for i := 32; i < 48; i++ {
						out[i] = 0
					}
				}
				if r.short {
					out = out[:20]
				}
				pc.WriteTo(out, addr)
			}()
		}
	}()
	return pc.LocalAddr().String()
}

func ctx5() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func TestQueryMeasuresTheServerSkew(t *testing.T) {
	for _, skew := range []time.Duration{0, 2500 * time.Millisecond, -3512 * time.Millisecond, 90 * time.Second} {
		r := goodReply()
		r.skew = skew
		srv := serve(t, r)
		ctx, cancel := ctx5()
		s, err := Query(ctx, srv, nil)
		cancel()
		if err != nil {
			t.Fatalf("skew %v: %v", skew, err)
		}
		if d := s.Offset - skew; d < -50*time.Millisecond || d > 50*time.Millisecond {
			t.Errorf("skew %v: measured offset %v", skew, s.Offset)
		}
		if s.Delay < 0 || s.Delay > 200*time.Millisecond || s.Stratum != 2 || s.Server != srv || s.Addr == "" {
			t.Errorf("sample = %+v", s)
		}
	}
}

func TestOffsetAndDelayArithmetic(t *testing.T) {
	// The textbook example: the local clock is 2.0 s behind the server.
	// T1 = 100.0 (local send), T2 = 102.1 (server receive), T3 = 102.2 (server
	// send), T4 = 100.3 (local receive). Delay = 0.3 - 0.1 = 0.2 s and
	// offset = ((2.1) + (1.9)) / 2 = 2.0 s.
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	t1 := base
	t4 := base.Add(300 * time.Millisecond)
	var nonce [8]byte
	copy(nonce[:], "12345678")
	p := make([]byte, packetSize)
	p[0], p[1] = 4<<3|modeServer, 2
	copy(p[24:32], nonce[:])
	putNTP(p[32:40], base.Add(2100*time.Millisecond))
	putNTP(p[40:48], base.Add(2200*time.Millisecond))
	s, err := parseResponse(p, nonce, t1, t4)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(s.Offset-2*time.Second)) > float64(time.Millisecond) || math.Abs(float64(s.Delay-200*time.Millisecond)) > float64(time.Millisecond) {
		t.Errorf("offset %v delay %v, want 2s and 200ms", s.Offset, s.Delay)
	}
}

func TestEraHandling(t *testing.T) {
	// After 2036 NTP's 32-bit seconds wrap; the era closest to local time is chosen.
	for _, want := range []time.Time{
		time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
		time.Date(2040, 1, 1, 0, 0, 0, 500_000_000, time.UTC),
		time.Date(2035, 12, 31, 23, 59, 59, 0, time.UTC),
	} {
		secs := uint64(want.Sub(ntpEpoch) / time.Second)
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b[0:4], uint32(secs))
		binary.BigEndian.PutUint32(b[4:8], uint32(uint64(want.Nanosecond())<<32/1_000_000_000))
		got := fromNTP(b, want.Add(3*time.Second))
		if d := got.Sub(want); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("fromNTP(%v) = %v", want, got)
		}
	}
}

func TestQueryRejectsBadReplies(t *testing.T) {
	cases := map[string]struct {
		r       reply
		wantErr string
	}{
		"spoofed origin":      {func() reply { r := goodReply(); r.badOrigin = true; return r }(), "does not match"},
		"server unsynced":     {func() reply { r := goodReply(); r.li = 3; return r }(), "not synchronized"},
		"stratum 16":          {func() reply { r := goodReply(); r.stratum = 16; return r }(), "not synchronized"},
		"client mode reply":   {func() reply { r := goodReply(); r.mode = modeClient; return r }(), "reply mode"},
		"ancient version":     {func() reply { r := goodReply(); r.version = 2; return r }(), "version"},
		"truncated":           {func() reply { r := goodReply(); r.short = true; return r }(), "too short"},
		"no timestamps":       {func() reply { r := goodReply(); r.zeroStamps = true; return r }(), "no timestamps"},
		"time runs backwards": {func() reply { r := goodReply(); r.t3Before = true; return r }(), "inconsistent"},
		"silent":              {func() reply { r := goodReply(); r.drop = true; return r }(), "did not answer"},
	}
	for name, c := range cases {
		srv := serve(t, c.r)
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		_, err := Query(ctx, srv, nil)
		cancel()
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.wantErr)
		}
	}
}

func TestOneSilentAttemptDoesNotUseTheWholeBudget(t *testing.T) {
	// The context allows 20 s, but a single attempt must give up after perQueryTimeout.
	srv := serve(t, func() reply { r := goodReply(); r.drop = true; return r }())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err := Query(ctx, srv, nil)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took < 2*time.Second || took > 5*time.Second {
		t.Errorf("a silent server held the attempt for %v, want about %v", took, perQueryTimeout)
	}
}

func TestKissOfDeath(t *testing.T) {
	r := goodReply()
	r.stratum, r.kiss = 0, "RATE"
	srv := serve(t, r)
	ctx, cancel := ctx5()
	defer cancel()
	_, err := Query(ctx, srv, nil)
	var k KissCode
	if !errors.As(err, &k) || k != "RATE" || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("err = %v", err)
	}
}

func TestQueryUnreachableHostGivesAReadableError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Query(ctx, "", nil); err == nil {
		t.Error("empty server accepted")
	}
	// nothing listens here: UDP gives either a refusal or a timeout, both reported plainly
	if _, err := Query(ctx, "127.0.0.1:9", nil); err == nil {
		t.Error("expected an error from a closed port")
	}
}

func TestStampsFollowTheLocalClockNotTheRealOne(t *testing.T) {
	// The caller's clock is 10 minutes behind reality; the (real-time) server is right.
	behind := func() time.Time { return time.Now().Add(-10 * time.Minute) }
	srv := serve(t, goodReply())
	ctx, cancel := ctx5()
	defer cancel()
	s, err := Query(ctx, srv, behind)
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Offset - 10*time.Minute; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("offset = %v, want about 10m", s.Offset)
	}
}
