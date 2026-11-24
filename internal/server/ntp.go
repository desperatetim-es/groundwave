// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/kd9psm/groundwave/internal/gps"
	"github.com/kd9psm/groundwave/internal/ntp"
)

// Time is what the server needs from the internet-time checker.
type Time interface {
	State() ntp.State
	CheckNow(ctx context.Context) (ntp.State, error)
	SetEnabled(on bool)
}

// noTime is used when the server is built without a checker (tests): it never
// touches the network.
func noTime() Time {
	return ntp.NewChecker(ntp.Options{
		Query: func(context.Context, string, func() time.Time) (ntp.Sample, error) {
			return ntp.Sample{}, errors.New("internet time is not available")
		},
	})
}

// Comparison says how GPS time and internet time relate, in words.
type Comparison struct {
	GPSMinusNTPMS float64 `json:"gps_minus_ntp_ms"` // positive: GPS is ahead of internet time
	Level         string  `json:"level"`            // "agree", "leap" or "differ"
	WholeSeconds  int     `json:"whole_seconds,omitempty"`
	Note          string  `json:"note"`
}

const (
	gpsAccuracyMS = 600 // a plain NMEA receiver is good to roughly a third of a second
	gpsFreshMS    = 5000
)

// compareClocks relates the GPS offset and the internet-time offset, both
// measured against this computer's clock. It returns nil unless both are current.
func compareClocks(g gps.Snapshot, n ntp.State) *Comparison {
	if g.Status != gps.StatusFix || g.OffsetMS == nil || g.AgeMS == nil || *g.AgeMS > gpsFreshMS {
		return nil
	}
	maxAge := int64(2*n.IntervalS)*1000 + 60_000
	if n.OffsetMS == nil || n.AgeMS == nil || *n.AgeMS > maxAge {
		return nil
	}
	d := *g.OffsetMS - *n.OffsetMS
	c := &Comparison{GPSMinusNTPMS: d}
	abs := math.Abs(d)
	whole := int(math.Round(d / 1000))
	ahead := "ahead of"
	if d < 0 {
		ahead = "behind"
	}
	switch {
	case abs <= gpsAccuracyMS:
		c.Level = "agree"
		c.Note = "GPS time and internet time agree, within the GPS receiver's timing accuracy."
	case whole != 0 && math.Abs(abs-math.Abs(float64(whole))*1000) <= 250 && abs < 21_000:
		secs := whole
		if secs < 0 {
			secs = -secs
		}
		c.Level, c.WholeSeconds = "leap", whole
		c.Note = fmt.Sprintf("GPS time is almost exactly %d second%s %s internet time. That is the typical sign of a receiver that has not yet received the current leap-second count from the satellites and is using a built-in default. Keep it tracking for about 15 minutes and check again. Until then, trust internet time.", secs, plural(secs), ahead)
	default:
		c.Level = "differ"
		c.Note = fmt.Sprintf("GPS time is %.2f s %s internet time. They should agree within about half a second, so one of them is wrong: check the internet servers below agree with each other, and that the receiver has a good fix.", abs/1000, ahead)
	}
	if g.CorrectionMS != nil {
		c.Note += fmt.Sprintf(" The GPS time shown includes a %+.0f s leap-second correction, because the receiver has not yet verified its own UTC data.", *g.CorrectionMS/1000)
	}
	return c
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ntpView is the API response: the checker's state plus the GPS comparison.
type ntpView struct {
	ntp.State
	VsGPS *Comparison `json:"vs_gps,omitempty"`
}

func (s *srv) ntpView() ntpView {
	st := s.Time.State()
	return ntpView{State: st, VsGPS: compareClocks(s.GPS.Snapshot(), st)}
}

func (s *srv) ntpStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.ntpView())
}

// ntpCheck runs one check on request (the "Check now" button).
func (s *srv) ntpCheck(w http.ResponseWriter, r *http.Request) {
	_, err := s.Time.CheckNow(r.Context())
	switch {
	case errors.Is(err, ntp.ErrTooSoon), errors.Is(err, ntp.ErrBusy):
		w.Header().Set("Retry-After", "10")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": err.Error(), "state": s.ntpView()})
		return
	case err != nil && !errors.Is(err, context.Canceled):
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ntpView())
}

// ntpSettings switches the automatic checks on or off.
func (s *srv) ntpSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, errors.New("send a JSON body like {\"enabled\": true}"))
		return
	}
	s.Time.SetEnabled(*req.Enabled)
	writeJSON(w, http.StatusOK, s.ntpView())
}
