// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kd9psm/groundwave/internal/settings"
)

func (s *srv) settingsGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.Settings.Get())
}

// settingsPut changes some or all of the settings. Only the fields sent are changed; an unknown
// field is an error, so a typo is noticed instead of silently ignored.
func (s *srv) settingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CheckUpdatesOnStart *bool     `json:"check_updates_on_start"`
		HiddenPages         *[]string `json:"hidden_pages"`
		CallsignLaunch      *string   `json:"callsign_launch"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<14))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("send a JSON object with the settings to change"))
		return
	}
	got, err := s.Settings.Update(func(c *settings.Settings) {
		if req.CheckUpdatesOnStart != nil {
			c.CheckUpdatesOnStart = *req.CheckUpdatesOnStart
		}
		if req.HiddenPages != nil {
			c.HiddenPages = *req.HiddenPages
		}
		if req.CallsignLaunch != nil {
			c.CallsignLaunch = *req.CallsignLaunch
		}
	})
	var ve *settings.ValidationError
	switch {
	case errors.As(err, &ve):
		writeErr(w, http.StatusBadRequest, err)
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, got)
	}
}
