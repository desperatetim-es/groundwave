// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package updatecheck asks GitHub whether a newer release of groundwλve exists.
// It runs only when the user presses "Check for updates". It sends one anonymous
// HTTPS request to GitHub's public API (GitHub can see this computer's address,
// as with any web request), reads the newest release's version number, and
// compares it with the running version. It never downloads or installs anything,
// and the link it returns is built here, not taken from GitHub's answer.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// RepoURL is the project's home on GitHub.
	RepoURL = "https://github.com/kd9psm/groundwave"
	// ReleasesURL always redirects to the newest published release.
	ReleasesURL = RepoURL + "/releases/latest"

	apiURL  = "https://api.github.com/repos/kd9psm/groundwave/releases/latest"
	maxBody = 1 << 20 // the answer is a few kilobytes; refuse anything over 1 MB
	recheck = 30 * time.Second
)

// Error is a failure whose text is safe to show the user.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// Result is the outcome of a check.
type Result struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	Newer     bool      `json:"newer"`           // a newer release than the running version exists
	Ahead     bool      `json:"ahead,omitempty"` // the running version is newer than the latest release (a preview or unreleased build)
	Dev       bool      `json:"dev,omitempty"`   // the running version is not a release number (a development build)
	URL       string    `json:"url"`             // where to read about and download the release
	CheckedAt time.Time `json:"checked_at"`
}

// Options configures a Checker. All fields are optional except in tests.
type Options struct {
	Version string // the running version, e.g. "1.0.0" or "dev"
	URL     string // default: GitHub's latest-release API address; tests point it at a local server
	// AnyHostForTests lifts the https and github.com restrictions. Tests only.
	AnyHostForTests bool
	Now             func() time.Time
}

// Checker performs update checks.
type Checker struct {
	o      Options
	client *http.Client

	mu     sync.Mutex
	last   Result
	lastOK bool
}

// New returns a Checker for the running version.
func New(o Options) *Checker {
	if o.URL == "" {
		o.URL = apiURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	c := &Checker{o: o}
	c.client = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !o.AnyHostForTests && !githubHTTPS(req.URL) {
				return errors.New("redirected somewhere that is not github.com")
			}
			return nil
		},
	}
	return c
}

func githubHTTPS(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (h == "github.com" || strings.HasSuffix(h, ".github.com"))
}

var tagRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
var versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+](.+))?$`)

// parse splits a version like "1.2.3" or "v1.2.3-preview" into its numbers and
// any pre-release suffix.
func parse(s string) (core [3]int, pre string, ok bool) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return core, "", false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return core, "", false
		}
		core[i] = n
	}
	return core, m[4], true
}

// newer reports whether release is newer than running. A pre-release of a
// version (1.1.0-preview) counts as older than that version's release (1.1.0).
func newer(running, release [3]int, runningPre string) bool {
	for i := 0; i < 3; i++ {
		if release[i] != running[i] {
			return release[i] > running[i]
		}
	}
	return runningPre != ""
}

// Last returns the most recent successful check, however old, and whether there has been one.
// It never contacts GitHub: the page uses it to show what a check at startup found.
func (c *Checker) Last() (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.lastOK
}

// Check asks GitHub for the newest release. Results are reused for 30 seconds,
// so pressing the button repeatedly sends nothing more to GitHub.
func (c *Checker) Check(ctx context.Context) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastOK && c.o.Now().Sub(c.last.CheckedAt) < recheck {
		return c.last, nil
	}
	u, err := url.Parse(c.o.URL)
	if err != nil || (!c.o.AnyHostForTests && !githubHTTPS(u)) {
		return Result{}, &Error{"The update address is not a GitHub https address."}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "groundwave")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &Error{"Could not reach GitHub. You may be offline."}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Result{}, &Error{"No release has been published yet."}
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return Result{}, &Error{"GitHub is limiting requests from this network right now. Try again in a little while."}
	case resp.StatusCode != http.StatusOK:
		return Result{}, &Error{fmt.Sprintf("GitHub answered unexpectedly (status %d).", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Result{}, &Error{"Could not read GitHub's answer."}
	}
	if len(body) > maxBody {
		return Result{}, &Error{"GitHub's answer was larger than expected, so it was ignored."}
	}
	var rel struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.Draft || rel.Prerelease || !tagRE.MatchString(rel.Tag) {
		return Result{}, &Error{"GitHub's answer was not understood."}
	}
	latest, _, _ := parse(rel.Tag)
	res := Result{
		Current:   c.o.Version,
		Latest:    strings.TrimPrefix(rel.Tag, "v"),
		URL:       ReleasesURL,
		CheckedAt: c.o.Now(),
	}
	if running, pre, ok := parse(c.o.Version); ok {
		res.Newer = newer(running, latest, pre)
		res.Ahead = !res.Newer && running != latest
	} else {
		res.Dev = true
	}
	c.last, c.lastOK = res, true
	return res, nil
}
