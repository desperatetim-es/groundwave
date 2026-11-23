// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/desperatetim-es/groundwave/internal/geo"
	"github.com/desperatetim-es/groundwave/internal/logbook"
)

const maxImportBytes = 64 << 20 // an ADIF file of about 200,000 contacts

// qsoView is a contact plus what is worked out when it is shown. None of the extra fields is stored:
// a mobile or portable station's grid can be corrected, and the operator's own position changes.
type qsoView struct {
	logbook.QSO
	// ShownGrid is the grid used for distance: the stored one, or (when none is stored) the one the
	// callsign database gives for the call sign. ShownGridFrom says which.
	ShownGrid     string   `json:"shown_grid,omitempty"`
	ShownGridFrom string   `json:"shown_grid_from,omitempty"` // "log" or "database"
	DistanceKM    *float64 `json:"distance_km,omitempty"`
	DistanceMI    *float64 `json:"distance_mi,omitempty"`
	BearingDeg    *float64 `json:"bearing_deg,omitempty"`
	Compass       string   `json:"compass,omitempty"`
}

func (s *srv) logbookReady(w http.ResponseWriter) bool {
	if s.Logbook == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("the logbook is not available"))
		return false
	}
	return true
}

func logbookErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, logbook.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, logbook.ErrInvalid):
		writeErr(w, http.StatusBadRequest, errors.New(strings.TrimPrefix(err.Error(), logbook.ErrInvalid.Error()+": ")))
	default:
		writeErr(w, http.StatusInternalServerError, errors.New("the logbook could not be read or written"))
	}
}

// view adds the grid in use and the distance and bearing from the given point (or, when none is
// given, from the grid recorded with the contact).
func (s *srv) view(q logbook.QSO, hasFrom bool, lat, lon float64) qsoView {
	v := qsoView{QSO: q}
	var tlat, tlon float64
	switch {
	case q.Grid != "":
		if la, lo, err := geo.GridCenter(q.Grid); err == nil {
			tlat, tlon, v.ShownGrid, v.ShownGridFrom = la, lo, q.Grid, "log"
		}
	default:
		if recs, err := s.Callsigns.Lookup(q.Call); err == nil && len(recs) > 0 && recs[0].Lat != nil && recs[0].Lon != nil {
			tlat, tlon, v.ShownGrid, v.ShownGridFrom = *recs[0].Lat, *recs[0].Lon, recs[0].Grid, "database"
		}
	}
	if v.ShownGrid == "" {
		return v
	}
	if !hasFrom && q.MyGrid != "" {
		if la, lo, err := geo.GridCenter(q.MyGrid); err == nil {
			lat, lon, hasFrom = la, lo, true
		}
	}
	if hasFrom {
		km := geo.DistanceKM(lat, lon, tlat, tlon)
		mi := geo.KMToMiles(km)
		br := geo.Bearing(lat, lon, tlat, tlon)
		v.DistanceKM, v.DistanceMI, v.BearingDeg, v.Compass = &km, &mi, &br, geo.Compass(br)
	}
	return v
}

// geoDistance answers /api/geo?from=<grid>&to=<grid>: the distance and bearing between two grid
// squares, for the logbook form to show as the operator types.
func (s *srv) geoDistance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	la1, lo1, err1 := geo.GridCenter(q.Get("from"))
	la2, lo2, err2 := geo.GridCenter(q.Get("to"))
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, errors.New("from and to must both be grid squares, like EN52 or EN52wd"))
		return
	}
	km := geo.DistanceKM(la1, lo1, la2, lo2)
	br := geo.Bearing(la1, lo1, la2, lo2)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"distance_km": km, "distance_mi": geo.KMToMiles(km), "bearing_deg": br, "compass": geo.Compass(br)})
}

func (s *srv) logbookList(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	q := r.URL.Query()
	lat, lon, has, err := fromPoint(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := s.Logbook.List(q.Get("q"), limit, offset)
	if err != nil {
		logbookErr(w, err)
		return
	}
	out := make([]qsoView, len(list))
	for i, c := range list {
		out[i] = s.view(c, has, lat, lon)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "qsos": out})
}

func readQSO(w http.ResponseWriter, r *http.Request) (logbook.QSO, bool) {
	var q logbook.QSO
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("expected a contact as JSON"))
		return q, false
	}
	q.ID = 0
	return q, true
}

func (s *srv) logbookAdd(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	q, ok := readQSO(w, r)
	if !ok {
		return
	}
	q, err := s.Logbook.Add(q)
	if err != nil {
		logbookErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.view(q, false, 0, 0))
}

func (s *srv) logbookUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad contact id"))
		return
	}
	q, ok := readQSO(w, r)
	if !ok {
		return
	}
	q, err = s.Logbook.Update(id, q)
	if err != nil {
		logbookErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(q, false, 0, 0))
}

func (s *srv) logbookDelete(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad contact id"))
		return
	}
	if err := s.Logbook.Delete(id); err != nil {
		logbookErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// logbookImport takes the raw ADIF file as the request body.
func (s *srv) logbookImport(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("that file is too large to be a log"))
		return
	}
	res, err := s.Logbook.ImportADIF(data)
	if err != nil {
		logbookErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *srv) logbookExport(w http.ResponseWriter, r *http.Request) {
	if !s.logbookReady(w) {
		return
	}
	all, err := s.Logbook.All()
	if err != nil {
		logbookErr(w, err)
		return
	}
	recs := make([]logbook.Fields, len(all))
	for i, q := range all {
		recs[i] = q.ToFields()
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="groundwave-log-%s.adi"`, time.Now().UTC().Format("20060102")))
	h.Set("Cache-Control", "no-store")
	logbook.WriteADIF(w, "groundwave", s.Version, recs)
}
