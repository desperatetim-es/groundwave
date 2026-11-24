// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kd9psm/groundwave/internal/callsign"
)

func TestUpdateAndCancelPassTheSource(t *testing.T) {
	f := &fakeCallsigns{}
	h := newCallsignServer(t, f)
	origin := "http://" + addr
	for target, want := range map[string]string{
		"/api/callsigns/update":           "us", // no source: the US database, as before other countries existed
		"/api/callsigns/update?source=ca": "ca",
		"/api/callsigns/update?source=us": "us",
	} {
		f.lastSource = ""
		if rec := do(h, "POST", target, addr, origin, nil, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("%s: got %d %s", target, rec.Code, rec.Body)
		}
		if f.lastSource != want {
			t.Errorf("%s: source %q, want %q", target, f.lastSource, want)
		}
	}
	f.lastSource = ""
	if rec := do(h, "POST", "/api/callsigns/cancel?source=ca", addr, origin, nil, ""); rec.Code != http.StatusOK || f.lastSource != "ca" {
		t.Errorf("cancel: %d, source %q", rec.Code, f.lastSource)
	}
}

func TestUnknownSourceIsABadRequest(t *testing.T) {
	f := &fakeCallsigns{startErr: callsign.ErrUnknownSource}
	h := newCallsignServer(t, f)
	rec := do(h, "POST", "/api/callsigns/update?source=zz", addr, "http://"+addr, nil, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestStatusCarriesEachSource(t *testing.T) {
	f := &fakeCallsigns{status: callsign.Status{Available: true, Records: 5, Sources: []callsign.SourceStatus{
		{ID: "us", Country: "United States", Org: "FCC", Download: "about 200 MB", Status: callsign.Status{}},
		{ID: "ca", Country: "Canada", Org: "ISED", Download: "about 3 MB", Credit: "Source: ISED", Status: callsign.Status{Available: true, Records: 5}},
	}}}
	rec := do(newCallsignServer(t, f), "GET", "/api/callsigns/status", addr, "", nil, "")
	var got struct {
		Available bool `json:"available"`
		Sources   []struct {
			ID        string `json:"id"`
			Country   string `json:"country"`
			Download  string `json:"download"`
			Credit    string `json:"credit"`
			Available bool   `json:"available"`
			Records   int    `json:"records"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %s", rec.Body)
	}
	if !got.Available || len(got.Sources) != 2 || got.Sources[1].ID != "ca" || !got.Sources[1].Available || got.Sources[1].Records != 5 || got.Sources[1].Credit != "Source: ISED" || got.Sources[0].Available {
		t.Fatalf("status: %s", rec.Body)
	}
}

func TestCanadianRecordsAreListedNotExpiredAndKeepTheirCountry(t *testing.T) {
	f := &fakeCallsigns{records: []callsign.Record{
		{Country: "ca", Call: "VE3ABC", Name: "John Smith", Status: "A", StatusName: "Listed", ClassName: "Basic", City: "Ottawa", State: "ON", ZIP: "K1A", Lat: f64(45.4), Lon: f64(-75.7), Grid: "FN25ev"},
		{Country: "us", Call: "W9OLD", Name: "Old Timer", Status: "E", Expires: "2010-01-01"},
	}}
	rec := do(newCallsignServer(t, f), "GET", "/api/callsigns/search?q=a&from=EN52vf", addr, "", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	rs := resultsOf(t, rec.Body.Bytes())
	if len(rs) != 2 || rs[0]["country"] != "ca" || rs[0]["standing"] != "listed" || rs[0]["standing_name"] != "Listed" {
		t.Errorf("Canadian record: %v", rs[0])
	}
	if rs[0]["distance_km"] == nil {
		t.Error("a Canadian record with a location should get a distance")
	}
	if rs[1]["country"] != "us" || rs[1]["standing"] != "expired" {
		t.Errorf("US record keeps the FCC's standing rules: %v", rs[1])
	}
}
