// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/library"
	"github.com/kd9psm/groundwave/internal/logbook"
)

const origin = "http://127.0.0.1:47321"

func newLogbookServer(t *testing.T, f *fakeCallsigns) http.Handler {
	t.Helper()
	st, _ := library.Open(t.TempDir())
	lb, err := logbook.Open(filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lb.Close() })
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, Callsigns: f, Logbook: lb, Addr: addr, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func qsosOf(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var v struct {
		Total int              `json:"total"`
		QSOs  []map[string]any `json:"qsos"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return v.QSOs
}

func TestLogbookEndToEnd(t *testing.T) {
	f := &fakeCallsigns{records: []callsign.Record{{Call: "W9CHI", Lat: f64(41.8781), Lon: f64(-87.6298), Grid: "EN61bv"}}}
	h := newLogbookServer(t, f)
	// a contact with no grid of its own uses the callsign database's, and says so
	rec := do(h, "POST", "/api/logbook", addr, origin, []byte(`{"call":"w9chi","date":"20260102","time":"1304","freq_mhz":14.074,"mode":"ft8"}`), "application/json")
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var made map[string]any
	json.Unmarshal(rec.Body.Bytes(), &made)
	if made["call"] != "W9CHI" || made["band"] != "20m" || made["shown_grid_from"] != "database" {
		t.Errorf("%v", made)
	}
	if made["grid"] != nil {
		t.Errorf("the database grid was stored without being asked: %v", made)
	}

	// distance is computed from wherever the operator is now
	rec = do(h, "GET", "/api/logbook?from=EN52vf", addr, "", nil, "")
	qs := qsosOf(t, rec.Body.Bytes())
	if rec.Code != 200 || len(qs) != 1 || qs[0]["distance_km"] == nil || qs[0]["compass"] == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	d1 := qs[0]["distance_km"].(float64)
	if d1 < 50 || d1 > 90 {
		t.Errorf("Cary to Chicago is about 60 km, got %v", d1)
	}
	d2 := qsosOf(t, do(h, "GET", "/api/logbook?from=FN31pr", addr, "", nil, "").Body.Bytes())[0]["distance_km"].(float64)
	if d2 < 1000 {
		t.Errorf("moving the operator did not change the distance: %v", d2)
	}
	if do(h, "GET", "/api/logbook?from=zz", addr, "", nil, "").Code != 400 {
		t.Error("a bad grid should be a 400")
	}

	// the operator overrides the grid (a portable station): it is stored and wins over the database
	id := int(made["id"].(float64))
	rec = do(h, "PUT", "/api/logbook/"+itoa(id), addr, origin, []byte(`{"call":"W9CHI/P","date":"20260102","time":"130400","freq_mhz":14.074,"mode":"FT8","grid":"fn31pr"}`), "application/json")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"shown_grid_from":"log"`) || !strings.Contains(rec.Body.String(), `"grid":"FN31pr"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// validation errors are 400s with a sentence a person can act on
	for _, bad := range []string{`{}`, `{"call":"W1AW","date":"2026","time":"1","mode":"CW","band":"20m"}`, `not json`, `{"call":"W1AW","date":"20260102","time":"1304","mode":"CW","band":"20m","grid":"ZZ99"}`} {
		rec := do(h, "POST", "/api/logbook", addr, origin, []byte(bad), "application/json")
		if rec.Code != 400 || strings.Contains(rec.Body.String(), "invalid contact:") {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	// import then export
	adif := "<EOH><CALL:4>W1AW<QSO_DATE:8>20260103<TIME_ON:4>0100<BAND:3>40m<MODE:2>CW<EOR>"
	rec = do(h, "POST", "/api/logbook/import", addr, origin, []byte(adif), "text/plain")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"added":1`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", "/api/logbook/export", addr, "", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(rec.Body.String(), "<CALL:4>W1AW") || !strings.Contains(rec.Body.String(), "<EOR>") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec = do(h, "GET", "/api/logbook?q=W1AW", addr, "", nil, ""); len(qsosOf(t, rec.Body.Bytes())) != 1 {
		t.Errorf("search: %s", rec.Body)
	}

	// delete
	if rec := do(h, "DELETE", "/api/logbook/"+itoa(id), addr, origin, nil, ""); rec.Code != 200 {
		t.Errorf("%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/logbook/"+itoa(id), addr, origin, nil, ""); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
}

func TestGeoDistance(t *testing.T) {
	h := newLogbookServer(t, &fakeCallsigns{})
	rec := do(h, "GET", "/api/geo?from=EN52vf&to=FN31pr", addr, "", nil, "")
	var v map[string]any
	json.Unmarshal(rec.Body.Bytes(), &v)
	// Cary IL to Hartford CT is roughly 1,400 km, to the east
	if d, _ := v["distance_km"].(float64); rec.Code != 200 || d < 1200 || d > 1400 || v["compass"] == "" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/api/geo", "/api/geo?from=EN52", "/api/geo?from=EN52&to=ZZ", "/api/geo?from=%00&to=EN52"} {
		if rec := do(h, "GET", bad, addr, "", nil, ""); rec.Code != 400 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
}

func TestLogbookIsProtected(t *testing.T) {
	h := newLogbookServer(t, &fakeCallsigns{})
	body := []byte(`{"call":"W1AW","date":"20260102","time":"1304","freq_mhz":14.074,"mode":"FT8"}`)
	// another web page cannot write to, or wipe, the log
	for _, o := range []string{"", "http://evil.example", "http://127.0.0.1:9999", "null"} {
		for _, c := range [][2]string{{"POST", "/api/logbook"}, {"PUT", "/api/logbook/1"}, {"DELETE", "/api/logbook/1"}, {"POST", "/api/logbook/import"}} {
			if rec := do(h, c[0], c[1], addr, o, body, "application/json"); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s from %q: %d", c[0], c[1], o, rec.Code)
			}
		}
	}
	// DNS rebinding: a foreign Host cannot even read it
	if rec := do(h, "GET", "/api/logbook/export", "evil.example", "", nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("%d", rec.Code)
	}
	// without a logbook the routes answer cleanly
	h2, _ := New(Config{Addr: addr})
	if rec := do(h2, "GET", "/api/logbook", addr, "", nil, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("%d", rec.Code)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
