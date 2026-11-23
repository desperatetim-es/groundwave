// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/desperatetim-es/groundwave/internal/gps"
)

// GPS is what the server needs from the receiver manager.
type GPS interface {
	Snapshot() gps.Snapshot
	Ports() ([]gps.PortInfo, error)
	Start(port string, baud int) error
	Stop(forget bool)
	Scan(ctx context.Context) ([]gps.Found, error)
}

// noGPS is used when the server is built without a receiver manager (tests).
func noGPS() GPS {
	return gps.NewManager(gps.Options{
		Open: func(string, int) (io.ReadCloser, error) { return nil, errors.New("no serial support") },
		List: func() ([]gps.PortInfo, error) { return nil, nil },
	})
}

func (s *srv) gpsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.GPS.Snapshot())
}

func (s *srv) gpsPorts(w http.ResponseWriter, r *http.Request) {
	ports, err := s.GPS.Ports()
	if err != nil {
		writeErr(w, http.StatusBadGateway, errors.New("could not list serial ports: "+err.Error()))
		return
	}
	if ports == nil {
		ports = []gps.PortInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}

func (s *srv) gpsConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port string `json:"port"`
		Baud int    `json:"baud"` // 0 = detect
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil || req.Port == "" {
		writeErr(w, http.StatusBadRequest, errors.New("send a JSON body like {\"port\": \"COM5\", \"baud\": 0}"))
		return
	}
	if err := s.GPS.Start(req.Port, req.Baud); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.GPS.Snapshot())
}

func (s *srv) gpsDisconnect(w http.ResponseWriter, r *http.Request) {
	s.GPS.Stop(true) // the choice is forgotten, so it won't reconnect next launch
	writeJSON(w, http.StatusOK, s.GPS.Snapshot())
}

// gpsScan listens briefly on every serial port for GPS data. It is only ever
// run from an explicit user click because opening other devices' ports can
// reset some boards.
func (s *srv) gpsScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	found, err := s.GPS.Scan(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		writeErr(w, http.StatusConflict, err)
		return
	}
	if found == nil {
		found = []gps.Found{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": found, "complete": err == nil})
}
