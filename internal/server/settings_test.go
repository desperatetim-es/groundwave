// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kd9psm/groundwave/internal/library"
	"github.com/kd9psm/groundwave/internal/settings"
	"github.com/kd9psm/groundwave/internal/updatecheck"
)

func settingsServer(t *testing.T, st *settings.Store, u UpdateChecker) http.Handler {
	t.Helper()
	lib, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Store: lib, Fetcher: &library.Fetcher{}, Addr: addr, Version: "test", Settings: st, Updates: u})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func put(h http.Handler, body string) (int, string) {
	rec := do(h, "PUT", "/api/settings", addr, "http://"+addr, []byte(body), "application/json")
	return rec.Code, rec.Body.String()
}

func settingsOf(t *testing.T, h http.Handler) settings.Settings {
	t.Helper()
	var s settings.Settings
	json.Unmarshal(do(h, "GET", "/api/settings", addr, "", nil, "").Body.Bytes(), &s)
	return s
}

func TestSettingsDefaultsAndChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	h := settingsServer(t, settings.Open(path), nil)
	rec := do(h, "GET", "/api/settings", addr, "", nil, "")
	var got settings.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if got.CheckUpdatesOnStart || got.CallsignLaunch != "off" || len(got.HiddenPages) != 0 || !strings.Contains(rec.Body.String(), `"hidden_pages":[]`) {
		t.Fatalf("defaults: %s", rec.Body)
	}
	if code, body := put(h, `{"check_updates_on_start": true, "hidden_pages": ["logbook","library"], "callsign_launch": "check"}`); code != 200 {
		t.Fatalf("PUT: %d %s", code, body)
	}
	// only the fields sent change
	if code, _ := put(h, `{"callsign_launch": "update"}`); code != 200 {
		t.Fatal("partial PUT failed")
	}
	got = settingsOf(t, h)
	if !got.CheckUpdatesOnStart || got.CallsignLaunch != "update" || strings.Join(got.HiddenPages, ",") != "library,logbook" {
		t.Fatalf("after changes: %+v", got)
	}
	// and it survives a restart
	if again := settings.Open(path).Get(); !again.CheckUpdatesOnStart || again.CallsignLaunch != "update" {
		t.Fatalf("not saved: %+v", again)
	}
}

func TestSettingsRefuseBadInput(t *testing.T) {
	h := settingsServer(t, settings.Open(""), nil)
	for name, body := range map[string]string{
		"unknown field":  `{"phone_home": true}`,
		"bad launch":     `{"callsign_launch": "always"}`,
		"unknown page":   `{"hidden_pages": ["settings"]}`,
		"wrong type":     `{"check_updates_on_start": "yes"}`,
		"not json":       `nope`,
		"empty":          ``,
		"array":          `[]`,
		"traversal page": `{"hidden_pages": ["../x"]}`,
	} {
		if code, _ := put(h, body); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
	if g := settingsOf(t, h); g.CheckUpdatesOnStart || g.CallsignLaunch != "off" {
		t.Fatalf("a refused request changed something: %+v", g)
	}
}

func TestSettingsPutIsGuarded(t *testing.T) {
	h := settingsServer(t, settings.Open(""), nil)
	for _, o := range []string{"", "https://evil.example.com", "http://127.0.0.1:9999", "null"} {
		if rec := do(h, "PUT", "/api/settings", addr, o, []byte(`{"check_updates_on_start": true}`), "application/json"); rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: got %d, want 403", o, rec.Code)
		}
	}
	if rec := do(h, "PUT", "/api/settings", "evil.example.com", "http://"+addr, []byte(`{}`), "application/json"); rec.Code == 200 {
		t.Error("a wrong Host was accepted")
	}
	if settingsOf(t, h).CheckUpdatesOnStart {
		t.Fatal("a refused request changed the settings")
	}
}

func TestLastUpdateResultNeverContactsAnything(t *testing.T) {
	f := &fakeUpdates{res: updatecheck.Result{Current: "1.0.0", Latest: "1.1.0", Newer: true, URL: updatecheck.ReleasesURL}, last: true}
	h := settingsServer(t, nil, f)
	rec := do(h, "GET", "/api/update-check", addr, "", nil, "")
	var got struct {
		Checked bool   `json:"checked"`
		Latest  string `json:"latest"`
		Newer   bool   `json:"newer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got.Checked || !got.Newer || got.Latest != "1.1.0" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if f.calls != 0 {
		t.Fatalf("reading the last result contacted GitHub (%d calls)", f.calls)
	}
	// nothing checked yet
	rec = do(settingsServer(t, nil, &fakeUpdates{}), "GET", "/api/update-check", addr, "", nil, "")
	if !strings.Contains(rec.Body.String(), `"checked":false`) {
		t.Fatalf("before any check: %s", rec.Body)
	}
	// and with no checker at all
	if rec := do(settingsServer(t, nil, nil), "GET", "/api/update-check", addr, "", nil, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"checked":false`) {
		t.Fatalf("without a checker: %d %s", rec.Code, rec.Body)
	}
}
