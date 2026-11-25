// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kd9psm/groundwave/internal/library"
	"github.com/kd9psm/groundwave/internal/updatecheck"
)

type fakeUpdates struct {
	res   updatecheck.Result
	err   error
	calls int32
	last  bool // Last reports a result
}

func (f *fakeUpdates) Check(context.Context) (updatecheck.Result, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.res, f.err
}
func (f *fakeUpdates) Last() (updatecheck.Result, bool) { return f.res, f.last }

func serverWith(t *testing.T, u UpdateChecker, open func(string) error) http.Handler {
	t.Helper()
	st, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, Addr: addr, Version: "test", Updates: u, OpenURL: open})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUpdateCheckReportsTheResult(t *testing.T) {
	f := &fakeUpdates{res: updatecheck.Result{Current: "1.0.0", Latest: "1.1.0", Newer: true, URL: updatecheck.ReleasesURL}}
	h := serverWith(t, f, nil)
	rec := do(h, "POST", "/api/update-check", addr, origin, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	var got updatecheck.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Newer || got.Latest != "1.1.0" || got.URL != updatecheck.ReleasesURL {
		t.Fatalf("unexpected result %+v", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the answer should not be cached")
	}
}

func TestUpdateCheckIsPostOnlyAndGuarded(t *testing.T) {
	f := &fakeUpdates{}
	h := serverWith(t, f, nil)
	// a GET only reads the last result and never contacts GitHub (the request itself is a POST)
	if rec := do(h, "GET", "/api/update-check", addr, origin, nil, ""); rec.Code != http.StatusOK || f.calls != 0 {
		t.Errorf("GET: got %d after %d checks, want a plain read of the last result", rec.Code, f.calls)
	}
	for _, o := range []string{"", "https://evil.example.com", "http://127.0.0.1:9999", "null"} {
		if rec := do(h, "POST", "/api/update-check", addr, o, nil, ""); rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: got %d, want 403", o, rec.Code)
		}
	}
	if rec := do(h, "POST", "/api/update-check", "evil.example.com", origin, nil, ""); rec.Code != http.StatusForbidden && rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("wrong host: got %d, want 403", rec.Code)
	}
	if f.calls != 0 {
		t.Fatalf("a refused request still contacted GitHub (%d calls)", f.calls)
	}
}

func TestUpdateCheckErrorsAreFriendlyAndNeverLeakDetail(t *testing.T) {
	h := serverWith(t, &fakeUpdates{err: &updatecheck.Error{Msg: "Could not reach GitHub. You may be offline."}}, nil)
	rec := do(h, "POST", "/api/update-check", addr, origin, nil, "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "offline") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	h = serverWith(t, &fakeUpdates{err: errors.New(`Get "https://api.github.com/x": dial tcp 10.0.0.1:443: secret detail`)}, nil)
	rec = do(h, "POST", "/api/update-check", addr, origin, nil, "")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "10.0.0.1") {
		t.Fatalf("internal error text reached the client: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateCheckWithoutACheckerIsUnavailable(t *testing.T) {
	h, _ := newServer(t) // built without Updates: must never reach the network
	rec := do(h, "POST", "/api/update-check", addr, origin, nil, "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestOpenExternalOnlyOpensFixedLinks(t *testing.T) {
	var opened []string
	h := serverWith(t, nil, func(u string) error { opened = append(opened, u); return nil })
	for name, want := range map[string]string{"repo": "https://github.com/kd9psm/groundwave", "releases": "https://github.com/kd9psm/groundwave/releases/latest"} {
		opened = nil
		if rec := do(h, "POST", "/api/open/"+name, addr, origin, nil, ""); rec.Code != http.StatusNoContent {
			t.Errorf("%s: got %d, want 204", name, rec.Code)
		}
		if len(opened) != 1 || opened[0] != want {
			t.Errorf("%s: opened %v, want [%s]", name, opened, want)
		}
	}
	opened = nil
	for _, name := range []string{"", "evil", "https://evil.example", "..%2Fx", "REPO", "repo/extra"} {
		if rec := do(h, "POST", "/api/open/"+name, addr, origin, nil, ""); rec.Code < 300 || rec.Code == http.StatusNoContent {
			t.Errorf("name %q: got %d, want a refusal", name, rec.Code)
		}
	}
	if rec := do(h, "POST", "/api/open/repo", addr, "https://evil.example.com", nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin open: got %d, want 403", rec.Code)
	}
	if rec := do(h, "GET", "/api/open/repo", addr, origin, nil, ""); rec.Code < 400 {
		t.Errorf("GET open: got %d, want an error (opening is POST only)", rec.Code)
	}
	if len(opened) != 0 {
		t.Fatalf("a refused or unknown request opened %v", opened)
	}
}

func TestOpenExternalFailureIsFriendly(t *testing.T) {
	h := serverWith(t, nil, func(string) error { return errors.New("exec: xdg-open: not found in /usr/secret/path") })
	rec := do(h, "POST", "/api/open/repo", addr, origin, nil, "")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	h, _ = newServer(t) // no OpenURL
	if rec := do(h, "POST", "/api/open/repo", addr, origin, nil, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without OpenURL: got %d, want 503", rec.Code)
	}
}
