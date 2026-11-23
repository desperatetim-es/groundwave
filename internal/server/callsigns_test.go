// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/geo"
	"github.com/kd9psm/groundwave/internal/library"
)

type fakeCallsigns struct {
	records  []callsign.Record
	err      error
	status   callsign.Status
	started  int
	cancels  int
	startErr error
	lastQ    string
	lastCall string
	lastLim  int
}

func (f *fakeCallsigns) Search(q string, limit int) ([]callsign.Record, error) {
	f.lastQ, f.lastLim = q, limit
	return f.records, f.err
}
func (f *fakeCallsigns) Lookup(call string) ([]callsign.Record, error) {
	f.lastCall = call
	return f.records, f.err
}
func (f *fakeCallsigns) Status() callsign.Status { return f.status }
func (f *fakeCallsigns) StartUpdate() error      { f.started++; return f.startErr }
func (f *fakeCallsigns) CancelUpdate()           { f.cancels++ }

func f64(v float64) *float64 { return &v }

func newCallsignServer(t *testing.T, f *fakeCallsigns) http.Handler {
	t.Helper()
	st, _ := library.Open(t.TempDir())
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, Callsigns: f, Addr: addr, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func resultsOf(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var v struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return v.Results
}

func TestCallsignSearchComputesDistanceAndBearingOnTheFly(t *testing.T) {
	// A station in the Chicago area, and one with no known location.
	f := &fakeCallsigns{records: []callsign.Record{
		{Call: "W9CHI", Name: "Chicago Station", City: "Chicago", State: "IL", Lat: f64(41.8781), Lon: f64(-87.6298), Grid: "EN61bv"},
		{Call: "W3ZIP", Name: "Nowhere", State: "PA"},
	}}
	h := newCallsignServer(t, f)

	rec := do(h, "GET", "/api/callsigns/search?q=chicago&limit=7&from=EN52vf", addr, "", nil, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.lastQ != "chicago" || f.lastLim != 7 {
		t.Errorf("the query was not passed on: %q %d", f.lastQ, f.lastLim)
	}
	res := resultsOf(t, rec.Body.Bytes())
	if len(res) != 2 {
		t.Fatalf("results: %s", rec.Body)
	}
	// compare against the shared geography code, and against a sanity range (Cary to Chicago is about 60 km)
	la, lo, _ := geo.GridCenter("EN52vf")
	wantKM := geo.DistanceKM(la, lo, 41.8781, -87.6298)
	gotKM, _ := res[0]["distance_km"].(float64)
	if gotKM != wantKM || gotKM < 50 || gotKM > 75 {
		t.Errorf("distance = %v km, want %v (and about 60)", gotKM, wantKM)
	}
	if mi, _ := res[0]["distance_mi"].(float64); mi < 31 || mi > 47 || mi != geo.KMToMiles(gotKM) {
		t.Errorf("miles = %v", mi)
	}
	// Cary is a northwest suburb, so Chicago lies to its south-east: about 48 km east and 39 km south.
	if b, _ := res[0]["bearing_deg"].(float64); b < 120 || b > 140 || res[0]["compass"] != "SE" {
		t.Errorf("bearing = %v %v (Chicago is south-east of Cary)", res[0]["bearing_deg"], res[0]["compass"])
	}
	// no location, no distance (and no invented one)
	if _, has := res[1]["distance_km"]; has {
		t.Error("a station with no location got a distance")
	}

	// the same point given as latitude and longitude
	rec = do(h, "GET", "/api/callsigns/search?q=chicago&from_lat=42.2292&from_lon=-88.2083", addr, "", nil, "")
	res = resultsOf(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || res[0]["distance_km"] == nil {
		t.Errorf("lat/lon form: %d %s", rec.Code, rec.Body)
	}
	// without a starting point there is simply no distance
	res = resultsOf(t, do(h, "GET", "/api/callsigns/search?q=chicago", addr, "", nil, "").Body.Bytes())
	if res[0]["distance_km"] != nil {
		t.Error("a distance appeared without a starting point")
	}
	// moving the starting point changes the distance: this is what the logbook's live update relies on
	far := resultsOf(t, do(h, "GET", "/api/callsigns/search?q=x&from=DM04", addr, "", nil, "").Body.Bytes())
	if d, _ := far[0]["distance_km"].(float64); d < 2000 {
		t.Errorf("from southern California the distance should be large, got %v", d)
	}
}

func TestCallsignSearchValidatesItsInput(t *testing.T) {
	f := &fakeCallsigns{records: []callsign.Record{}}
	h := newCallsignServer(t, f)
	for _, bad := range []string{
		"?q=x&from=ZZ99", "?q=x&from=EN5", "?q=x&from_lat=abc&from_lon=1", "?q=x&from_lat=91&from_lon=0",
		"?q=x&from_lat=0&from_lon=181", "?q=x&from_lat=5",
	} {
		if rec := do(h, "GET", "/api/callsigns/search"+bad, addr, "", nil, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", bad, rec.Code)
		}
	}
	// an exact call lookup uses the lookup path
	do(h, "GET", "/api/callsigns/search?call=W1AW/4", addr, "", nil, "")
	if f.lastCall != "W1AW/4" {
		t.Errorf("lookup was not used: %q", f.lastCall)
	}
	// an empty result list is [] and not null
	rec := do(h, "GET", "/api/callsigns/search?q=nobody", addr, "", nil, "")
	if !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Errorf("empty results: %s", rec.Body)
	}
}

func TestCallsignErrorsMapToTheRightStatus(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		sub  string
	}{
		{&callsign.QueryError{Msg: "type a call sign"}, http.StatusBadRequest, "type a call sign"},
		{callsign.ErrNoDatabase, http.StatusServiceUnavailable, `"needs_download":true`},
		{callsign.ErrUpdating, http.StatusServiceUnavailable, "being replaced"},
		{errors.New("sqlite: disk I/O error near /home/someone/secret"), http.StatusInternalServerError, "the callsign search failed"},
	} {
		h := newCallsignServer(t, &fakeCallsigns{err: c.err})
		rec := do(h, "GET", "/api/callsigns/search?q=x", addr, "", nil, "")
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.sub) {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("an internal error message leaked to the client: %s", rec.Body)
		}
	}
}

func TestCallsignUpdateEndpointsNeedOurOrigin(t *testing.T) {
	f := &fakeCallsigns{}
	h := newCallsignServer(t, f)
	origin := "http://" + addr
	for _, p := range []string{"/api/callsigns/update", "/api/callsigns/cancel"} {
		for _, bad := range []string{"", "https://evil.example.com", "null"} {
			if rec := do(h, "POST", p, addr, bad, nil, ""); rec.Code != http.StatusForbidden {
				t.Errorf("%s with origin %q: got %d", p, bad, rec.Code)
			}
		}
	}
	if f.started != 0 || f.cancels != 0 {
		t.Fatalf("a forbidden request reached the database: %+v", f)
	}
	// a page on another site can start a 200 MB download no more than it can anything else
	if rec := do(h, "POST", "/api/callsigns/update", addr, origin, nil, ""); rec.Code != http.StatusAccepted || f.started != 1 {
		t.Errorf("update: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/callsigns/cancel", addr, origin, nil, ""); rec.Code != http.StatusOK || f.cancels != 1 {
		t.Errorf("cancel: %d", rec.Code)
	}
	f.startErr = callsign.ErrBusy
	f.status = callsign.Status{Update: callsign.UpdateState{Running: true, Phase: "Downloading"}}
	if rec := do(h, "POST", "/api/callsigns/update", addr, origin, nil, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Downloading") {
		t.Errorf("busy: %d %s", rec.Code, rec.Body)
	}
	f.startErr = errors.New("nope")
	if rec := do(h, "POST", "/api/callsigns/update", addr, origin, nil, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("failure: %d", rec.Code)
	}
}

func TestCallsignStatusAndDefaults(t *testing.T) {
	f := &fakeCallsigns{status: callsign.Status{Available: true, Records: 1430697}}
	h := newCallsignServer(t, f)
	rec := do(h, "GET", "/api/callsigns/status", addr, "", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"records":1430697`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}
	// a server built without a database says so rather than failing
	d, _ := newServer(t)
	if rec := do(d, "GET", "/api/callsigns/status", addr, "", nil, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Errorf("default status: %d %s", rec.Code, rec.Body)
	}
	if rec := do(d, "GET", "/api/callsigns/search?q=w1aw", addr, "", nil, ""); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "needs_download") {
		t.Errorf("default search: %d %s", rec.Code, rec.Body)
	}
}

func TestCallsignResultsCarryTheRealStandingNotJustTheFCCStatus(t *testing.T) {
	now := time.Now()
	day := func(offsetDays int) string { return now.AddDate(0, 0, offsetDays).Format("2006-01-02") }
	f := &fakeCallsigns{records: []callsign.Record{
		{Call: "W1OK", Status: "A", Expires: day(+400)},
		{Call: "W1GRACE", Status: "A", Expires: day(-30)},    // the FCC still says Active
		{Call: "W1OLD", Status: "A", Expires: day(-3 * 365)}, // long past the grace period
		{Call: "W1GONE", Status: "C", Expires: day(+400)},
	}}
	res := resultsOf(t, do(newCallsignServer(t, f), "GET", "/api/callsigns/search?q=w1", addr, "", nil, "").Body.Bytes())
	want := []string{"active", "grace", "expired", "other"}
	for i, w := range want {
		if res[i]["standing"] != w || res[i]["standing_name"] == "" {
			t.Errorf("%v: standing = %v %v, want %s", res[i]["call"], res[i]["standing"], res[i]["standing_name"], w)
		}
	}
}
