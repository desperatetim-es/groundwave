// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kd9psm/groundwave/internal/gps"
	"github.com/kd9psm/groundwave/internal/library"
	"github.com/kd9psm/groundwave/internal/ntp"
)

var pdf = []byte("%PDF-1.4\n1 0 obj<<>>endobj\n%%EOF\n")

const addr = "127.0.0.1:47321"

func newServer(t *testing.T) (http.Handler, *atomic.Bool) {
	t.Helper()
	st, err := library.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := &atomic.Bool{}
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, Addr: addr, Version: "test", Quit: func() { quit.Store(true) }})
	if err != nil {
		t.Fatal(err)
	}
	return h, quit
}

func do(h http.Handler, method, target, host, origin string, body []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+target, bytes.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func multipartBody(t *testing.T, filename string, data []byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("file", filename)
	fw.Write(data)
	w.Close()
	return b.Bytes(), w.FormDataContentType()
}

func TestHostIsChecked(t *testing.T) {
	h, _ := newServer(t)
	for _, host := range []string{"evil.example.com", "evil.example.com:47321", "127.0.0.1:9999", "192.168.1.5:47321", ""} {
		if rec := do(h, "GET", "/api/docs", host, "", nil, ""); rec.Code != http.StatusForbidden {
			t.Errorf("host %q: got %d, want 403", host, rec.Code)
		}
	}
	for _, host := range []string{addr, "localhost:47321"} {
		if rec := do(h, "GET", "/api/docs", host, "", nil, ""); rec.Code != http.StatusOK {
			t.Errorf("host %q: got %d, want 200", host, rec.Code)
		}
	}
}

func TestStateChangesNeedOwnOrigin(t *testing.T) {
	h, quit := newServer(t)
	body, ct := multipartBody(t, "a.pdf", pdf)
	for _, origin := range []string{"", "https://evil.example.com", "http://127.0.0.1:9999", "null"} {
		if rec := do(h, "POST", "/api/docs", addr, origin, body, ct); rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: upload got %d, want 403", origin, rec.Code)
		}
		if rec := do(h, "POST", "/api/quit", addr, origin, nil, ""); rec.Code != http.StatusForbidden {
			t.Errorf("origin %q: quit got %d, want 403", origin, rec.Code)
		}
	}
	if quit.Load() {
		t.Fatal("cross-site request shut the server down")
	}
	if rec := do(h, "POST", "/api/docs", addr, "http://"+addr, body, ct); rec.Code != http.StatusCreated {
		t.Fatalf("own origin upload: got %d: %s", rec.Code, rec.Body)
	}
}

func TestUploadListServeUpdateDelete(t *testing.T) {
	h, _ := newServer(t)
	origin := "http://" + addr
	body, ct := multipartBody(t, "Manual.pdf", pdf)

	rec := do(h, "POST", "/api/docs", addr, origin, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var doc library.Doc
	json.Unmarshal(rec.Body.Bytes(), &doc)

	// duplicate -> 409 with the existing doc
	rec = do(h, "POST", "/api/docs", addr, origin, body, ct)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), doc.ID) {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}

	// not a supported file -> 422
	bad, badCT := multipartBody(t, "x.pdf", []byte("MZ\x90\x00\x03"))
	if rec := do(h, "POST", "/api/docs", addr, origin, bad, badCT); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported: %d", rec.Code)
	}

	// served with OUR content type and nosniff
	rec = do(h, "GET", "/files/"+doc.ID, addr, "", nil, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(rec.Body.Bytes(), pdf) {
		t.Fatalf("file: %d %v", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Fatalf("disposition: %q", cd)
	}
	if rec := do(h, "GET", "/files/doesnotexist", addr, "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatal("missing file should be 404")
	}

	rec = do(h, "PATCH", "/api/docs/"+doc.ID, addr, origin, []byte(`{"title":"FT-60R","tags":["Yaesu","ht"]}`), "application/json")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "FT-60R") {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", "/api/docs?q=yaesu", addr, "", nil, "")
	if !strings.Contains(rec.Body.String(), doc.ID) {
		t.Fatal("search by tag failed:", rec.Body)
	}

	if rec := do(h, "DELETE", "/api/docs/"+doc.ID, addr, origin, nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(h, "GET", "/files/"+doc.ID, addr, "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatal("deleted file still served")
	}
}

func TestURLToLocalAddressRefused(t *testing.T) {
	h, _ := newServer(t)
	rec := do(h, "POST", "/api/docs/url", addr, "http://"+addr, []byte(`{"url":"http://127.0.0.1:1/x.pdf"}`), "application/json")
	if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), "private or local") {
		t.Fatalf("local URL not refused: %d %s", rec.Code, rec.Body)
	}
}

func TestCalcEndpoints(t *testing.T) {
	h, _ := newServer(t)
	get := func(path string) *httptest.ResponseRecorder { return do(h, "GET", path, addr, "", nil, "") }

	rec := get("/api/calc/dipole?f=14.2")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"feet":32.95`) {
		t.Fatalf("dipole: %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/calc/swr?r=75"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"swr":1.49999`) {
		t.Fatalf("swr from impedance: %d %s", rec.Code, rec.Body)
	}
	// an infinite value is JSON null, not an encoding failure
	if rec := get("/api/calc/swr?swr=1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"return_loss_db":null`) {
		t.Fatalf("perfect match: %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/calc/coax?loss=3&length=100&swr=3&power=100"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"power_out_w"`) {
		t.Fatalf("coax: %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/calc/power?tx=100&gain=2.15"); rec.Code != http.StatusOK {
		t.Fatalf("power: %d %s", rec.Code, rec.Body)
	}
	// error handling
	if rec := get("/api/calc/dipole"); rec.Code != http.StatusBadRequest {
		t.Errorf("missing f: got %d", rec.Code)
	}
	if rec := get("/api/calc/dipole?f=abc"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad f: got %d", rec.Code)
	}
	if rec := get("/api/calc/dipole?f=0"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("out of range f: got %d", rec.Code)
	}
	if rec := get("/api/calc/dipole?f=NaN"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("NaN f: got %d", rec.Code)
	}
	if rec := get("/api/calc/nothing?f=1"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown calculator: got %d", rec.Code)
	}
}

// fakeGPS records what the server asks of the receiver manager.
type fakeGPS struct {
	started   string
	baud      int
	stopped   bool
	forgotten bool
	scanned   int
}

func (f *fakeGPS) Snapshot() gps.Snapshot {
	if f.started == "" {
		return gps.Snapshot{Status: gps.StatusStopped}
	}
	return gps.Snapshot{Status: gps.StatusConnecting, Port: f.started, Baud: f.baud}
}
func (f *fakeGPS) Ports() ([]gps.PortInfo, error) {
	return []gps.PortInfo{{Name: "COM9", Description: "USB GPS"}}, nil
}
func (f *fakeGPS) Start(port string, baud int) error {
	if port != "COM9" {
		return gps.ErrUnknownPort
	}
	f.started, f.baud = port, baud
	return nil
}
func (f *fakeGPS) Stop(forget bool) { f.started, f.stopped, f.forgotten = "", true, forget }
func (f *fakeGPS) Scan(ctx context.Context) ([]gps.Found, error) {
	f.scanned++
	return []gps.Found{{Port: "COM9", Baud: 9600, Description: "USB GPS"}}, nil
}

func TestGPSEndpoints(t *testing.T) {
	st, _ := library.Open(t.TempDir())
	fg := &fakeGPS{}
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, GPS: fg, Addr: addr, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + addr

	if rec := do(h, "GET", "/api/gps", addr, "", nil, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stopped"`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	if rec := do(h, "GET", "/api/gps/ports", addr, "", nil, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "COM9") {
		t.Fatalf("ports: %d %s", rec.Code, rec.Body)
	}

	// All three POSTs refuse requests that don't come from our own page, and do nothing.
	for _, p := range []string{"/api/gps/connect", "/api/gps/disconnect", "/api/gps/scan"} {
		for _, bad := range []string{"", "https://evil.example.com", "null"} {
			if rec := do(h, "POST", p, addr, bad, []byte(`{"port":"COM9"}`), "application/json"); rec.Code != http.StatusForbidden {
				t.Errorf("%s with origin %q: got %d, want 403", p, bad, rec.Code)
			}
		}
	}
	if fg.started != "" || fg.scanned != 0 || fg.stopped {
		t.Fatalf("a forbidden request reached the receiver manager: %+v", fg)
	}

	// connect
	if rec := do(h, "POST", "/api/gps/connect", addr, origin, []byte(`{"port":"/etc/passwd"}`), "application/json"); rec.Code != http.StatusBadRequest || fg.started != "" {
		t.Fatalf("unknown port: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/gps/connect", addr, origin, []byte(`{}`), "application/json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/gps/connect", addr, origin, []byte(`{"port":"COM9","baud":4800}`), "application/json"); rec.Code != http.StatusAccepted || fg.started != "COM9" || fg.baud != 4800 {
		t.Fatalf("connect: %d %s %+v", rec.Code, rec.Body, fg)
	}
	// scan
	rec := do(h, "POST", "/api/gps/scan", addr, origin, nil, "")
	if rec.Code != http.StatusOK || fg.scanned != 1 || !strings.Contains(rec.Body.String(), `"found"`) || !strings.Contains(rec.Body.String(), `"baud":9600`) {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	// disconnect forgets the receiver
	if rec := do(h, "POST", "/api/gps/disconnect", addr, origin, nil, ""); rec.Code != http.StatusOK || !fg.forgotten || fg.started != "" {
		t.Fatalf("disconnect: %d %+v", rec.Code, fg)
	}
}

// ---- internet time ---------------------------------------------------------------

func gpsAt(offsetMS float64, ageMS int64, status string) gps.Snapshot {
	return gps.Snapshot{Status: status, OffsetMS: &offsetMS, AgeMS: &ageMS}
}

func ntpAt(offsetMS float64, ageMS int64) ntp.State {
	return ntp.State{IntervalS: 600, OffsetMS: &offsetMS, AgeMS: &ageMS}
}

func TestCompareClocks(t *testing.T) {
	cases := []struct {
		name      string
		gps, ntp  float64 // offsets against the system clock, ms
		level     string
		wholeSecs int
		mention   string
	}{
		// the real case from the first u-blox test: system 3.51 s behind NTP, GPS 5.53 s ahead of system
		{"leap seconds, GPS ahead", 5530, 3510, "leap", 2, "ahead of"},
		{"leap seconds, GPS behind", 1510, 3510, "leap", -2, "behind"},
		{"three seconds", 6510, 3510, "leap", 3, "3 seconds"},
		{"agree", 3600, 3510, "agree", 0, "agree"},
		{"agree, GPS slightly behind", 3100, 3510, "agree", 0, "agree"},
		{"half second apart is still within GPS accuracy", 4050, 3510, "agree", 0, "agree"},
		{"between whole seconds", 4950, 3510, "differ", 0, "1.44 s"},
		{"650 ms is not claimed to be a whole second", 4160, 3510, "differ", 0, "0.65 s"},
		{"about one second", 4500, 3510, "leap", 1, "1 second "},
		{"far apart", 60000, 3510, "differ", 0, "56.49 s"},
	}
	for _, c := range cases {
		got := compareClocks(gpsAt(c.gps, 200, gps.StatusFix), ntpAt(c.ntp, 1000))
		if got == nil {
			t.Errorf("%s: no comparison", c.name)
			continue
		}
		if got.Level != c.level || got.WholeSeconds != c.wholeSecs || !strings.Contains(got.Note, c.mention) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	// A corrected GPS reading agrees with internet time, and the note says a correction is included.
	corr := -2000.0
	cg := gpsAt(3540, 200, gps.StatusFix) // 5540 raw minus the 2000 ms correction
	cg.CorrectionMS = &corr
	if got := compareClocks(cg, ntpAt(3510, 1000)); got == nil || got.Level != "agree" || !strings.Contains(got.Note, "-2 s leap-second correction") {
		t.Errorf("corrected GPS vs internet time: %+v", got)
	}
	// no comparison unless both sources are current
	for name, got := range map[string]*Comparison{
		"gps without a fix":    compareClocks(gpsAt(5530, 200, gps.StatusAcquiring), ntpAt(3510, 1000)),
		"gps reading is stale": compareClocks(gpsAt(5530, 9000, gps.StatusFix), ntpAt(3510, 1000)),
		"ntp never checked":    compareClocks(gpsAt(5530, 200, gps.StatusFix), ntp.State{IntervalS: 600}),
		"ntp reading too old":  compareClocks(gpsAt(5530, 200, gps.StatusFix), ntpAt(3510, 3_000_000)),
		"nothing from the gps": compareClocks(gps.Snapshot{Status: gps.StatusFix}, ntpAt(3510, 1000)),
	} {
		if got != nil {
			t.Errorf("%s: unexpected comparison %+v", name, got)
		}
	}
}

type fakeTime struct {
	enabled bool
	checks  int
	state   ntp.State
}

func (f *fakeTime) State() ntp.State { s := f.state; s.Enabled = f.enabled; return s }
func (f *fakeTime) CheckNow(ctx context.Context) (ntp.State, error) {
	f.checks++
	if f.checks > 1 {
		return f.State(), ntp.ErrTooSoon
	}
	return f.State(), nil
}
func (f *fakeTime) SetEnabled(on bool) { f.enabled = on }

func TestNTPEndpoints(t *testing.T) {
	st, _ := library.Open(t.TempDir())
	ft := &fakeTime{state: ntp.State{IntervalS: 600, Servers: []string{"time.example.org"}}}
	fg := &fakeGPS{}
	h, err := New(Config{Store: st, Fetcher: &library.Fetcher{}, GPS: fg, Time: ft, Addr: addr, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + addr

	rec := do(h, "GET", "/api/ntp", addr, "", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	// A request from another web page can neither start a check nor switch it on.
	for _, p := range []string{"/api/ntp/check", "/api/ntp/settings"} {
		for _, bad := range []string{"", "https://evil.example.com", "null"} {
			if rec := do(h, "POST", p, addr, bad, []byte(`{"enabled":true}`), "application/json"); rec.Code != http.StatusForbidden {
				t.Errorf("%s with origin %q: got %d", p, bad, rec.Code)
			}
		}
	}
	if ft.checks != 0 || ft.enabled {
		t.Fatalf("a forbidden request reached the checker: %+v", ft)
	}
	// settings
	if rec := do(h, "POST", "/api/ntp/settings", addr, origin, []byte(`{}`), "application/json"); rec.Code != http.StatusBadRequest {
		t.Errorf("empty settings: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/ntp/settings", addr, origin, []byte(`{"enabled":true}`), "application/json"); rec.Code != http.StatusOK || !ft.enabled || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("enable: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/ntp/settings", addr, origin, []byte(`{"enabled":false}`), "application/json"); rec.Code != http.StatusOK || ft.enabled {
		t.Errorf("disable: %d", rec.Code)
	}
	// check now, then the rate limit
	if rec := do(h, "POST", "/api/ntp/check", addr, origin, nil, ""); rec.Code != http.StatusOK {
		t.Errorf("check: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", "/api/ntp/check", addr, origin, nil, "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("second check: %d %v", rec.Code, rec.Header())
	}
}

func TestNTPDefaultsToOffAndNeverTouchesTheNetwork(t *testing.T) {
	h, _ := newServer(t) // built without a checker
	rec := do(h, "GET", "/api/ntp", addr, "", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestGPSDefaultsToNone(t *testing.T) {
	h, _ := newServer(t) // built without a receiver manager
	rec := do(h, "GET", "/api/gps", addr, "", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stopped"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/gps/ports", addr, "", nil, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ports":[]`) {
		t.Fatalf("ports: %d %s", rec.Code, rec.Body)
	}
}

func TestStaticUIAndQuit(t *testing.T) {
	h, quit := newServer(t)
	rec := do(h, "GET", "/", addr, "", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "groundwλve") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("index: %d %v", rec.Code, rec.Header())
	}
	for path, want := range map[string]string{"/js/app.js": "text/javascript", "/js/antenna.js": "text/javascript", "/style.css": "text/css"} {
		rec := do(h, "GET", path, addr, "", nil, "")
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) {
			t.Errorf("%s: %d %q, want %s", path, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
	if rec := do(h, "POST", "/api/quit", addr, "http://"+addr, nil, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("quit: %d", rec.Code)
	}
	// Quit runs in a goroutine; give it a moment.
	for i := 0; i < 100 && !quit.Load(); i++ {
		testSleep()
	}
	if !quit.Load() {
		t.Fatal("quit callback not called")
	}
}
