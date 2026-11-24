// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// serve starts a fake GitHub that answers with the given status and body.
func serve(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("User-Agent") != "groundwave" {
			t.Errorf("User-Agent = %q, want groundwave", r.Header.Get("User-Agent"))
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func check(t *testing.T, version string, status int, body string) (Result, error) {
	t.Helper()
	srv, _ := serve(t, status, body)
	return New(Options{Version: version, URL: srv.URL, AnyHostForTests: true}).Check(context.Background())
}

func TestNewerAndSameAndDev(t *testing.T) {
	const rel = `{"tag_name":"v1.2.0","draft":false,"prerelease":false}`
	for _, tc := range []struct {
		version     string
		newer, dev  bool
		description string
	}{
		{"1.0.0", true, false, "older release"},
		{"1.1.9", true, false, "older patch"},
		{"1.2.0", false, false, "same"},
		{"1.2.1", false, false, "running is newer"},
		{"2.0.0", false, false, "running is a later major"},
		{"1.2.0-preview", true, false, "preview of the same version is older than its release"},
		{"1.3.0-preview", false, false, "preview of a later version"},
		{"v1.0.0", true, false, "leading v"},
		{"dev", false, true, "development build"},
		{"", false, true, "empty version"},
	} {
		res, err := check(t, tc.version, 200, rel)
		if err != nil {
			t.Errorf("%s (%q): %v", tc.description, tc.version, err)
			continue
		}
		if res.Newer != tc.newer || res.Dev != tc.dev {
			t.Errorf("%s (%q): newer=%v dev=%v, want newer=%v dev=%v", tc.description, tc.version, res.Newer, res.Dev, tc.newer, tc.dev)
		}
		if res.Latest != "1.2.0" || res.URL != ReleasesURL || res.Current != tc.version {
			t.Errorf("%s: bad result %+v", tc.description, res)
		}
	}
}

func TestFriendlyErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{404, `{}`, "No release has been published yet"},
		{403, `{}`, "limiting requests"},
		{429, `{}`, "limiting requests"},
		{500, `{}`, "status 500"},
		{200, `not json`, "not understood"},
		{200, `{"tag_name":"latest"}`, "not understood"},
		{200, `{"tag_name":"v1.0.0-rc1"}`, "not understood"},
		{200, `{"tag_name":"v1.0"}`, "not understood"},
		{200, `{"tag_name":"v2.0.0","prerelease":true}`, "not understood"},
		{200, `{"tag_name":"v2.0.0","draft":true}`, "not understood"},
		{200, `{"tag_name":"v1.0.0/../x"}`, "not understood"},
	} {
		_, err := check(t, "1.0.0", tc.status, tc.body)
		var ue *Error
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, tc.want) {
			t.Errorf("status %d body %q: got %v, want an Error containing %q", tc.status, tc.body, err, tc.want)
		}
	}
}

func TestOversizedAnswerIsRefused(t *testing.T) {
	big := `{"tag_name":"v1.2.0","pad":"` + strings.Repeat("x", maxBody) + `"}`
	_, err := check(t, "1.0.0", 200, big)
	var ue *Error
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "larger than expected") {
		t.Fatalf("got %v, want the larger-than-expected error", err)
	}
}

func TestLinkIsOursNotGitHubs(t *testing.T) {
	res, err := check(t, "1.0.0", 200, `{"tag_name":"v1.2.0","html_url":"https://evil.example/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://github.com/kd9psm/groundwave/releases/latest" {
		t.Fatalf("URL = %q, want the fixed releases link", res.URL)
	}
}

func TestOfflineIsFriendly(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	_, err := New(Options{Version: "1.0.0", URL: url, AnyHostForTests: true}).Check(context.Background())
	var ue *Error
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "offline") {
		t.Fatalf("got %v, want the offline message", err)
	}
}

func TestOnlyGitHubHTTPSIsAllowed(t *testing.T) {
	for _, u := range []string{"http://api.github.com/repos/x/y/releases/latest", "https://example.org/x", "https://github.com.evil.example/x", "ftp://github.com/x"} {
		_, err := New(Options{Version: "1.0.0", URL: u}).Check(context.Background())
		var ue *Error
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "not a GitHub https address") {
			t.Errorf("%s: got %v, want the address refused", u, err)
		}
	}
}

func TestRedirectOffGitHubIsRefused(t *testing.T) {
	other, _ := serve(t, 200, `{"tag_name":"v9.9.9"}`)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer redir.Close()
	// With the test escape hatch off, a redirect to a non-GitHub address must fail even if the first
	// address were allowed. Simulate by calling the redirect policy directly.
	c := New(Options{Version: "1.0.0"})
	req, _ := http.NewRequest("GET", other.URL, nil)
	if err := c.client.CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("a redirect to a non-GitHub address was allowed")
	}
	ok, _ := http.NewRequest("GET", "https://github.com/kd9psm/groundwave/releases/latest", nil)
	if err := c.client.CheckRedirect(ok, []*http.Request{{}}); err != nil {
		t.Fatalf("a redirect within github.com was refused: %v", err)
	}
	plain, _ := http.NewRequest("GET", "http://github.com/x", nil)
	if err := c.client.CheckRedirect(plain, []*http.Request{{}}); err == nil {
		t.Fatal("a redirect to plain http was allowed")
	}
}

func TestRepeatedPressesAreCachedBriefly(t *testing.T) {
	srv, hits := serve(t, 200, `{"tag_name":"v1.2.0"}`)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	c := New(Options{Version: "1.0.0", URL: srv.URL, AnyHostForTests: true, Now: func() time.Time { return now }})
	for i := 0; i < 3; i++ {
		if _, err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if *hits != 1 {
		t.Fatalf("GitHub was asked %d times in a row, want 1", *hits)
	}
	now = now.Add(time.Minute)
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *hits != 2 {
		t.Fatalf("after the cache expired GitHub was asked %d times in total, want 2", *hits)
	}
}

func TestFailuresAreNotCached(t *testing.T) {
	srv, hits := serve(t, 403, `{}`)
	c := New(Options{Version: "1.0.0", URL: srv.URL, AnyHostForTests: true})
	c.Check(context.Background())
	c.Check(context.Background())
	if *hits != 2 {
		t.Fatalf("a failed check was cached (%d requests)", *hits)
	}
}
