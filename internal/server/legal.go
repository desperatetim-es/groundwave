// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"net/http"

	"github.com/kd9psm/groundwave"
)

// legalFiles maps the name in the URL to the embedded file. Only these two can
// be asked for; the name is never used as a path.
var legalFiles = map[string]string{
	"license":     "LICENSE",
	"third-party": "THIRD_PARTY_LICENSES.txt",
}

// legal serves the license texts shown by the Help menu, as plain text.
func (s *srv) legal(w http.ResponseWriter, r *http.Request) {
	file, ok := legalFiles[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := groundwave.Legal.ReadFile(file)
	if err != nil {
		http.Error(w, "license text unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(b)
}
