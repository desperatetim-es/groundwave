// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/geo"
)

// Callsigns is what the server needs from the callsign database.
type Callsigns interface {
	Search(q string, limit int) ([]callsign.Record, error)
	Lookup(call string) ([]callsign.Record, error)
	Status() callsign.Status
	StartUpdate() error
	CancelUpdate()
}

// noCallsigns is used when the program is built without a database (tests).
type noCallsigns struct{}

func (noCallsigns) Search(string, int) ([]callsign.Record, error) { return nil, callsign.ErrNoDatabase }
func (noCallsigns) Lookup(string) ([]callsign.Record, error)      { return nil, callsign.ErrNoDatabase }
func (noCallsigns) Status() callsign.Status                       { return callsign.Status{} }
func (noCallsigns) StartUpdate() error                            { return errors.New("the callsign database is not available") }
func (noCallsigns) CancelUpdate()                                 {}

// callsignResult is a record plus its distance and bearing from the point the
// caller gave, computed on the fly (never stored: the point changes as the
// user moves, and a mobile or portable station's grid can be corrected).
type callsignResult struct {
	callsign.Record
	DistanceKM *float64 `json:"distance_km,omitempty"`
	DistanceMI *float64 `json:"distance_mi,omitempty"`
	BearingDeg *float64 `json:"bearing_deg,omitempty"`
	Compass    string   `json:"compass,omitempty"`

	// Standing is worked out from the expiry date at the moment of the lookup: the FCC keeps a
	// license marked Active through its renewal grace period.
	Standing     string `json:"standing"`
	StandingName string `json:"standing_name"`
}

// fromPoint reads the "from" location of a request: either from=<grid square>
// or from_lat and from_lon. It returns ok=false when none was given.
func fromPoint(q url.Values) (lat, lon float64, ok bool, err error) {
	if g := q.Get("from"); g != "" {
		lat, lon, err = geo.GridCenter(g)
		return lat, lon, err == nil, err
	}
	if q.Get("from_lat") == "" && q.Get("from_lon") == "" {
		return 0, 0, false, nil
	}
	lat, e1 := strconv.ParseFloat(q.Get("from_lat"), 64)
	lon, e2 := strconv.ParseFloat(q.Get("from_lon"), 64)
	if e1 != nil || e2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false, errors.New("from_lat and from_lon must be a latitude and a longitude in degrees")
	}
	return lat, lon, true, nil
}

func annotate(rs []callsign.Record, hasFrom bool, lat, lon float64, now time.Time) []callsignResult {
	out := make([]callsignResult, len(rs))
	for i, r := range rs {
		out[i].Record = r
		out[i].Standing, out[i].StandingName = callsign.Standing(r.Status, r.Expires, now)
		if hasFrom && r.Lat != nil && r.Lon != nil {
			km := geo.DistanceKM(lat, lon, *r.Lat, *r.Lon)
			mi := geo.KMToMiles(km)
			br := geo.Bearing(lat, lon, *r.Lat, *r.Lon)
			out[i].DistanceKM, out[i].DistanceMI, out[i].BearingDeg, out[i].Compass = &km, &mi, &br, geo.Compass(br)
		}
	}
	return out
}

// callsignErr maps database errors to HTTP answers.
func callsignErr(w http.ResponseWriter, err error) {
	var qe *callsign.QueryError
	switch {
	case errors.As(err, &qe):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, callsign.ErrNoDatabase):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "needs_download": true})
	case errors.Is(err, callsign.ErrUpdating):
		writeErr(w, http.StatusServiceUnavailable, err)
	default:
		writeErr(w, http.StatusInternalServerError, errors.New("the callsign search failed"))
	}
}

func (s *srv) callsignStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.Callsigns.Status())
}

func (s *srv) callsignSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lat, lon, has, err := fromPoint(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	var rs []callsign.Record
	if call := q.Get("call"); call != "" && q.Get("q") == "" {
		rs, err = s.Callsigns.Lookup(call)
	} else {
		rs, err = s.Callsigns.Search(q.Get("q"), limit)
	}
	if err != nil {
		callsignErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"results": annotate(rs, has, lat, lon, time.Now())})
}

func (s *srv) callsignUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.Callsigns.StartUpdate(); err != nil {
		if errors.Is(err, callsign.ErrBusy) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "status": s.Callsigns.Status()})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.Callsigns.Status())
}

func (s *srv) callsignCancel(w http.ResponseWriter, r *http.Request) {
	s.Callsigns.CancelUpdate()
	writeJSON(w, http.StatusOK, s.Callsigns.Status())
}
