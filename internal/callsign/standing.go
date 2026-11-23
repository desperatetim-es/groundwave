// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import "time"

// The FCC keeps a license marked "Active" until its renewal grace period has
// ended, so the status code alone says nothing about whether the holder is
// licensed today. Standing works that out from the expiry date.
const (
	StandingActive  = "active"  // currently licensed
	StandingGrace   = "grace"   // the term has ended but it can still be renewed (up to 2 years); not licensed to operate
	StandingExpired = "expired" // the grace period is over
	StandingOther   = "other"   // cancelled, terminated or pending
)

// GraceYears is the FCC renewal grace period after a license expires.
const GraceYears = 2

// Standing returns a license's real standing on the given date, with a plain-language label.
func Standing(status, expires string, now time.Time) (code, label string) {
	today := now.Format("2006-01-02")
	graceEnd := now.AddDate(-GraceYears, 0, 0).Format("2006-01-02")
	switch status {
	case "A", "E":
		switch {
		case status == "A" && (expires == "" || expires >= today):
			return StandingActive, "Active"
		case expires != "" && expires >= graceEnd:
			return StandingGrace, "Expired (in the renewal grace period)"
		default:
			return StandingExpired, "Expired"
		}
	}
	return StandingOther, StatusName(status)
}
