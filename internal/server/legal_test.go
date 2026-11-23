// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestLegalTexts(t *testing.T) {
	h, _ := newServer(t)
	for path, want := range map[string]string{
		"/api/legal/license":     "GNU GENERAL PUBLIC LICENSE",
		"/api/legal/third-party": "modernc.org/sqlite",
	} {
		rec := do(h, "GET", path, addr, "", nil, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: content type %q, want text/plain", path, ct)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: body does not contain %q", path, want)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", path)
		}
	}
}

func TestLegalOnlyServesTheTwoKnownFiles(t *testing.T) {
	h, _ := newServer(t)
	for _, name := range []string{"unknown", "..%2Fgo.mod", "LICENSE", "../LICENSE", "license%2F..%2Fgo.mod", ""} {
		if rec := do(h, "GET", "/api/legal/"+name, addr, "", nil, ""); rec.Code == http.StatusOK {
			t.Errorf("/api/legal/%s was served (200); want it refused", name)
		}
	}
}

func TestLegalChecksHost(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(h, "GET", "/api/legal/license", "evil.example.com", "", nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("foreign host: got %d, want 403", rec.Code)
	}
}
