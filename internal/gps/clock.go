// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"sort"
	"time"
)

func medianInt(v []int64) int64 {
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// utcAtLocked returns the receiver's UTC at the instant now, taken from the
// median of recent fixes. Each anchor is the receiver's UTC at a fixed
// monotonic origin, so adding the monotonic time elapsed since then gives UTC
// now without ever consulting the (steppable) wall clock. The caller holds m.mu.
func (m *Manager) utcAtLocked(now time.Time) (time.Time, bool) {
	if len(m.anchors) == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, medianInt(m.anchors)+now.Sub(m.origin).Nanoseconds()).UTC(), true
}

// utcStateLocked reports whether the receiver's UTC is verified and, if it is
// not, the correction (in ms) to add so the time matches true UTC. The caller
// holds m.mu.
func (m *Manager) utcStateLocked() (status string, corrMS float64) {
	switch {
	case m.utcValid || (m.leap != nil && m.leap.LeapValid):
		return UTCVerified, 0 // the receiver trusts its own UTC: use it as it is
	case m.leap != nil:
		return UTCUnverified, leapCorrectionMS(*m.leap)
	}
	return UTCUnknown, 0
}

// TimeAt returns the best estimate of true UTC at the instant now (use
// time.Now()), with its uncertainty, for serving time to other programs. ok is
// false unless there is a current, plausible fix: it is withheld when the
// receiver's date is more than a day from the computer's (a sign of a wrong
// date), and whenever the data has gone stale.
func (m *Manager) TimeAt(now time.Time) (t time.Time, ok bool, dispersion time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phase != phaseConnected || m.lastValid.IsZero() || now.Sub(m.lastValid) > m.o.StaleAfter || m.suspect {
		return time.Time{}, false, 0
	}
	est, ok := m.utcAtLocked(now)
	if !ok {
		return time.Time{}, false, 0
	}
	_, corr := m.utcStateLocked()
	est = est.Add(time.Duration(corr * float64(time.Millisecond)))
	lo, hi := m.anchors[0], m.anchors[0]
	for _, a := range m.anchors {
		lo, hi = min(lo, a), max(hi, a)
	}
	// A plain serial receiver is good to roughly a third of a second; add how much
	// the recent fixes disagreed with each other.
	return est, true, 300*time.Millisecond + time.Duration(hi-lo)
}
