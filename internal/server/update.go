// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kd9psm/groundwave/internal/updatecheck"
)

// UpdateChecker asks whether a newer release exists (see package updatecheck).
type UpdateChecker interface {
	Check(ctx context.Context) (updatecheck.Result, error)
}

// noUpdates is used when the program is built without a checker (tests): it
// never contacts anything.
type noUpdates struct{}

func (noUpdates) Check(context.Context) (updatecheck.Result, error) {
	return updatecheck.Result{}, &updatecheck.Error{Msg: "Checking for updates is not available."}
}

// updateCheck answers the Help menu's "Check for updates" button. It is a POST so
// that the Host and Origin checks apply: another web page cannot trigger it.
func (s *srv) updateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, err := s.Updates.Check(ctx)
	if err != nil {
		var ue *updatecheck.Error
		if errors.As(err, &ue) {
			writeErr(w, http.StatusBadGateway, ue) // the text is written to be shown to the user
			return
		}
		writeErr(w, http.StatusBadGateway, errors.New("The update check failed."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
}

// externalLinks are the only addresses the program will open in the system
// browser on request. The name comes from the page; the address never does.
var externalLinks = map[string]string{
	"repo":     updatecheck.RepoURL,
	"releases": updatecheck.ReleasesURL,
}

// openExternal opens one of the fixed links in the default browser. The Windows app
// window has no tabs, so its Help menu links must leave the app through here instead
// of navigating the window away from the program.
func (s *srv) openExternal(w http.ResponseWriter, r *http.Request) {
	link, ok := externalLinks[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.OpenURL == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("Opening links is not available."))
		return
	}
	if err := s.OpenURL(link); err != nil {
		writeErr(w, http.StatusInternalServerError, errors.New("Could not open your browser."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
