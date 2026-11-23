// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// DefaultMaxBytes is the largest file accepted when no limit is configured.
const DefaultMaxBytes = 256 << 20

var ErrWebPage = errors.New("that link is a web page, not a document; use a direct link to the file")

// Fetcher downloads a document the user asked for. It is never used except in
// response to the user pasting a URL.
type Fetcher struct {
	// AllowPrivate permits loopback and private network addresses. Leave false
	// in the app; tests set it so they can use a local test server.
	AllowPrivate bool
	Timeout      time.Duration // whole request; default 2 minutes
	MaxBytes     int64
}

// Fetch downloads rawURL into the store.
func (f *Fetcher) Fetch(ctx context.Context, st *Store, rawURL string) (Doc, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Doc{}, errors.New("enter a full http:// or https:// link")
	}
	if u.User != nil {
		return Doc{}, errors.New("links containing a username or password are not supported")
	}
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	if f.MaxBytes <= 0 {
		f.MaxBytes = DefaultMaxBytes
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Doc{}, err
	}
	req.Header.Set("User-Agent", "groundwave")

	resp, err := f.client().Do(req)
	if err != nil {
		return Doc{}, fmt.Errorf("could not download: %w", cleanErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Doc{}, fmt.Errorf("the server answered %s", resp.Status)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") {
		return Doc{}, ErrWebPage
	}
	if resp.ContentLength > f.MaxBytes {
		return Doc{}, ErrTooLarge
	}
	return st.Add(io.Reader(resp.Body), u.Path, u.String(), f.MaxBytes)
}

func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (f *Fetcher) client() *http.Client {
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
		// Control runs on the address actually being connected to, so it also
		// covers redirects and DNS tricks.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return errors.New("unexpected address")
			}
			if !f.AllowPrivate && isPrivate(ip) {
				return errors.New("links to private or local network addresses are not allowed")
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil, // a proxy would bypass the address check
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 15 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirect to an unsupported address")
			}
			return nil
		},
	}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}
