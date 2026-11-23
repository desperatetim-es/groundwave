// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package ntp is a small, strict SNTP client (RFC 4330 / RFC 5905) that
// measures how far this computer's clock is from public time servers. It only
// ever sends one 48-byte request per query and never sets the system clock.
package ntp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	packetSize = 48
	ntpPort    = "123"

	modeClient = 3
	modeServer = 4

	perQueryTimeout = 3 * time.Second
)

// ntpEpoch is the NTP era-0 origin: 1900-01-01 00:00:00 UTC.
var ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// Sample is one successful exchange with a server.
type Sample struct {
	Server  string        // as configured
	Addr    string        // the address actually used
	Offset  time.Duration // server time minus this computer's clock
	Delay   time.Duration // network round-trip time (excluding server processing)
	Stratum int
}

// KissCode is returned when a server answers with stratum 0 ("kiss-o'-death").
type KissCode string

func (k KissCode) Error() string {
	switch k {
	case "RATE":
		return "the server asked us to slow down (RATE)"
	case "DENY", "RSTR":
		return "the server refused the request (" + string(k) + ")"
	}
	return "the server sent a kiss-o'-death code: " + string(k)
}

// toNTP converts an NTP 64-bit timestamp to a time, choosing the era closest to ref.
func fromNTP(b []byte, ref time.Time) time.Time {
	secs := int64(binary.BigEndian.Uint32(b[0:4]))
	frac := int64(binary.BigEndian.Uint32(b[4:8]))
	nanos := (frac * 1_000_000_000) >> 32
	t := ntpEpoch.Add(time.Duration(secs)*time.Second + time.Duration(nanos))
	// Era 0 ends in 2036. If the result is more than 68 years in the past, the
	// server is in era 1.
	if ref.Sub(t) > 68*365*24*time.Hour {
		t = t.Add(time.Duration(1<<32) * time.Second)
	}
	return t
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Query asks one server for the time. server is "host" or "host:port"; the
// default port is 123. now supplies the local clock (time.Now in production).
func Query(ctx context.Context, server string, now func() time.Time) (Sample, error) {
	if now == nil {
		now = time.Now
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		host, port = server, ntpPort
	}
	if host == "" {
		return Sample{}, errors.New("empty server name")
	}
	target := net.JoinHostPort(host, port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", target)
	if err != nil {
		return Sample{}, fmt.Errorf("cannot reach %s: %w", host, err)
	}
	defer conn.Close()
	// Each attempt gets its own short timeout, so one silent server cannot use up
	// the whole budget and starve its own retries.
	deadline := time.Now().Add(perQueryTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)

	// RFC 5905 advises a random transmit timestamp: it can't leak our clock, and
	// the server echoes it back, which lets us reject spoofed or stale replies.
	var req [packetSize]byte
	req[0] = 0<<6 | 4<<3 | modeClient // no warning, version 4, client mode
	if _, err := rand.Read(req[40:48]); err != nil {
		return Sample{}, err
	}
	var nonce [8]byte
	copy(nonce[:], req[40:48])

	t1 := now()
	if _, err := conn.Write(req[:]); err != nil {
		return Sample{}, fmt.Errorf("cannot send to %s: %w", host, err)
	}
	var resp [1024]byte
	n, err := conn.Read(resp[:])
	t4 := t1.Add(now().Sub(t1)) // keep t4 consistent with t1 even if the clock was stepped
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return Sample{}, fmt.Errorf("%s did not answer in time", host)
		}
		return Sample{}, fmt.Errorf("no answer from %s: %w", host, err)
	}
	s, err := parseResponse(resp[:n], nonce, t1, t4)
	if err != nil {
		return Sample{}, fmt.Errorf("%s: %w", host, err)
	}
	s.Server = server
	s.Addr = conn.RemoteAddr().String()
	return s, nil
}

// parseResponse validates a server reply and computes offset and delay.
func parseResponse(p []byte, nonce [8]byte, t1, t4 time.Time) (Sample, error) {
	if len(p) < packetSize {
		return Sample{}, errors.New("the reply was too short")
	}
	li, vn, mode := p[0]>>6, (p[0]>>3)&7, p[0]&7
	if mode != modeServer {
		return Sample{}, fmt.Errorf("unexpected reply mode %d", mode)
	}
	if vn < 3 || vn > 4 {
		return Sample{}, fmt.Errorf("unsupported NTP version %d", vn)
	}
	stratum := int(p[1])
	if stratum == 0 {
		return Sample{}, KissCode(string(trimNul(p[12:16])))
	}
	if stratum > 15 || li == 3 {
		return Sample{}, errors.New("the server says its own clock is not synchronized")
	}
	if [8]byte(p[24:32]) != nonce {
		return Sample{}, errors.New("the reply does not match our request (ignored)")
	}
	if isZero(p[32:40]) || isZero(p[40:48]) {
		return Sample{}, errors.New("the reply has no timestamps")
	}
	t2 := fromNTP(p[32:40], t1)
	t3 := fromNTP(p[40:48], t1)
	if t3.Before(t2) {
		return Sample{}, errors.New("the reply has inconsistent timestamps")
	}
	delay := t4.Sub(t1) - t3.Sub(t2)
	if delay < -10*time.Millisecond {
		return Sample{}, errors.New("the reply has an impossible round-trip time")
	}
	if delay < 0 {
		delay = 0
	}
	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2
	return Sample{Offset: offset, Delay: delay, Stratum: stratum}, nil
}

func trimNul(b []byte) []byte {
	for i, x := range b {
		if x == 0 {
			return b[:i]
		}
	}
	return b
}
