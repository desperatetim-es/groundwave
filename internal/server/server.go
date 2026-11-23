// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package server is the local HTTP interface. It must only ever be reachable
// from the user's own machine, and must not be drivable by other web sites.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/desperatetim-es/groundwave/internal/library"
	"github.com/desperatetim-es/groundwave/internal/logbook"
	"github.com/desperatetim-es/groundwave/web"
)

// Config wires the server to its collaborators.
type Config struct {
	Store     *library.Store
	GPS       GPS            // receiver manager; nil = no GPS support
	Time      Time           // internet-time checker; nil = none
	Callsigns Callsigns      // offline callsign database; nil = none
	Logbook   *logbook.Store // contact log; nil = none
	Fetcher   *library.Fetcher
	Addr      string // the listener address, e.g. 127.0.0.1:47321
	Version   string
	Quit      func() // called after POST /api/quit
	MaxUpload int64  // largest accepted file; default library.DefaultMaxBytes
}

type srv struct {
	Config
	hosts   map[string]bool
	origins map[string]bool
}

// New returns the handler for the whole app.
func New(c Config) (http.Handler, error) {
	if c.MaxUpload <= 0 {
		c.MaxUpload = library.DefaultMaxBytes
	}
	if c.GPS == nil {
		c.GPS = noGPS()
	}
	if c.Time == nil {
		c.Time = noTime()
	}
	if c.Callsigns == nil {
		c.Callsigns = noCallsigns{}
	}
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return nil, err
	}
	s := &srv{Config: c, hosts: map[string]bool{}, origins: map[string]bool{}}
	for _, h := range []string{"127.0.0.1", "localhost"} {
		s.hosts[h+":"+port] = true
		s.origins["http://"+h+":"+port] = true
	}

	ui, err := fs.Sub(web.Static, "static")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", s.ping)
	mux.HandleFunc("GET /api/legal/{name}", s.legal)
	mux.HandleFunc("GET /api/docs", s.list)
	mux.HandleFunc("POST /api/docs", s.upload)
	mux.HandleFunc("POST /api/docs/url", s.addURL)
	mux.HandleFunc("PATCH /api/docs/{id}", s.update)
	mux.HandleFunc("DELETE /api/docs/{id}", s.remove)
	mux.HandleFunc("GET /api/calc/{name}", s.calc)
	mux.HandleFunc("GET /api/gps", s.gpsStatus)
	mux.HandleFunc("GET /api/gps/ports", s.gpsPorts)
	mux.HandleFunc("POST /api/gps/connect", s.gpsConnect)
	mux.HandleFunc("POST /api/gps/disconnect", s.gpsDisconnect)
	mux.HandleFunc("POST /api/gps/scan", s.gpsScan)
	mux.HandleFunc("GET /api/ntp", s.ntpStatus)
	mux.HandleFunc("POST /api/ntp/check", s.ntpCheck)
	mux.HandleFunc("POST /api/ntp/settings", s.ntpSettings)
	mux.HandleFunc("GET /api/callsigns/status", s.callsignStatus)
	mux.HandleFunc("GET /api/callsigns/search", s.callsignSearch)
	mux.HandleFunc("POST /api/callsigns/update", s.callsignUpdate)
	mux.HandleFunc("POST /api/callsigns/cancel", s.callsignCancel)
	mux.HandleFunc("GET /api/geo", s.geoDistance)
	mux.HandleFunc("GET /api/logbook", s.logbookList)
	mux.HandleFunc("POST /api/logbook", s.logbookAdd)
	mux.HandleFunc("PUT /api/logbook/{id}", s.logbookUpdate)
	mux.HandleFunc("DELETE /api/logbook/{id}", s.logbookDelete)
	mux.HandleFunc("POST /api/logbook/import", s.logbookImport)
	mux.HandleFunc("GET /api/logbook/export", s.logbookExport)
	mux.HandleFunc("GET /files/{id}", s.file)
	mux.HandleFunc("POST /api/quit", s.quit)
	mux.Handle("GET /", staticHandler(http.FileServer(http.FS(ui))))
	return s.guard(mux), nil
}

// guard rejects requests that are not clearly from our own page:
//   - Host must be one of our loopback names (defeats DNS rebinding).
//   - Anything that changes state must carry our own Origin (defeats
//     cross-site request forgery from other pages the user has open).
func (s *srv) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.origins[r.Header.Get("Origin")] {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-cache")
		// Don't trust the OS mime registry: a wrong type for .js breaks module scripts.
		switch {
		case strings.HasSuffix(r.URL.Path, ".js"):
			h.Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(r.URL.Path, ".css"):
			h.Set("Content-Type", "text/css; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// libraryErr maps store/fetch errors to HTTP statuses.
func libraryErr(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, library.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, library.ErrTooLarge), errors.As(err, &tooBig):
		writeErr(w, http.StatusRequestEntityTooLarge, library.ErrTooLarge)
	case errors.Is(err, library.ErrUnsupported), errors.Is(err, library.ErrWebPage), errors.Is(err, library.ErrInvalid):
		writeErr(w, http.StatusUnprocessableEntity, err)
	default:
		writeErr(w, http.StatusBadGateway, err)
	}
}

func (s *srv) ping(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"app": "groundwave", "version": s.Version})
}

func (s *srv) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Store.List(r.URL.Query().Get("q")))
}

// upload streams a multipart body so large files are not held in memory.
func (s *srv) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUpload+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("expected a file upload"))
		return
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeErr(w, http.StatusBadRequest, errors.New("no file in the upload"))
			return
		}
		if err != nil {
			libraryErr(w, err)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		doc, err := s.Store.Add(part, part.FileName(), "", s.MaxUpload)
		s.respondAdd(w, doc, err)
		return
	}
}

func (s *srv) addURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.URL) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("send a JSON body like {\"url\": \"https://...\"}"))
		return
	}
	f := *s.Fetcher
	f.MaxBytes = s.MaxUpload
	doc, err := f.Fetch(r.Context(), s.Store, req.URL)
	s.respondAdd(w, doc, err)
}

func (s *srv) respondAdd(w http.ResponseWriter, doc library.Doc, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, doc)
	case errors.Is(err, library.ErrDuplicate):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "doc": doc})
	default:
		libraryErr(w, err)
	}
}

func (s *srv) update(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title *string   `json:"title"`
		Tags  *[]string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid JSON"))
		return
	}
	doc, err := s.Store.Update(r.PathValue("id"), req.Title, req.Tags)
	if err != nil {
		libraryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *srv) remove(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Delete(r.PathValue("id")); err != nil {
		libraryErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// file serves a stored document. The content type comes from our own sniffing
// at upload time, never from anything the uploader or remote server said.
func (s *srv) file(w http.ResponseWriter, r *http.Request) {
	doc, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.Store.Path(doc))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", doc.Type)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": safeName(doc.Title) + "." + doc.Ext}))
	http.ServeContent(w, r, "", doc.Added, f)
}

func (s *srv) quit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
	if s.Quit != nil {
		go s.Quit()
	}
}

func safeName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r < 0x20, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	n := strings.TrimSpace(b.String())
	if n == "" {
		return "document"
	}
	return n
}
