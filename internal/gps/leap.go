// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

// PublishedLeapSeconds is the current difference between GPS time and UTC.
// GPS time has no leap seconds; UTC gained its 18th since 1980 on 2017-01-01
// and no leap second has been added since (IERS Bulletin C). It would change
// only if the IERS announces a new leap second, and leap seconds are due to be
// discontinued by 2035. If one is ever announced, update this value.
//
// It is used only to correct a receiver that has not yet received the real
// value from the satellites and says so. It is never saved or learned, and the
// correction switches itself off the moment the receiver reports valid data.
const PublishedLeapSeconds = 18

// UTC verification states reported to the UI.
const (
	UTCVerified   = "verified"   // the receiver has confirmed its own UTC (leap-second) data
	UTCUnverified = "unverified" // it is still using a built-in default leap-second count
	UTCUnknown    = "unknown"    // this receiver cannot tell us (not a u-blox, or not asked yet)
)

// leapCorrectionMS returns how many milliseconds to add to the receiver's UTC
// so that it matches true UTC, given what the receiver reported about its
// leap-second count. The receiver computes UTC = GPS time - used; the truth is
// GPS time - PublishedLeapSeconds, so the correction is used - published
// (negative when the receiver is using an out-of-date, smaller count).
func leapCorrectionMS(t timeGPS) float64 {
	if t.LeapValid || !t.TOWValid || !t.WeekValid {
		return 0 // the receiver trusts its own value, or its time is not valid at all
	}
	if t.Leap < 0 || t.Leap >= PublishedLeapSeconds {
		return 0 // implausible, or already current: leave it alone
	}
	return float64(t.Leap-PublishedLeapSeconds) * 1000
}
