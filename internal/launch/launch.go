// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package launch does the things the user has asked the program to do when it starts:
// check whether a newer release exists, and check or update the callsign databases that are
// already downloaded. Every one of them is off by default, so with the default settings
// Run does nothing and contacts nothing.
package launch

import (
	"context"
	"log"
	"time"

	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/settings"
	"github.com/kd9psm/groundwave/internal/updatecheck"
)

// Updates asks whether a newer release exists.
type Updates interface {
	Check(ctx context.Context) (updatecheck.Result, error)
}

// Callsigns is what the launch tasks need from the callsign databases.
type Callsigns interface {
	// CheckRemote asks the publisher of each downloaded database whether it has a newer list.
	CheckRemote(ctx context.Context) map[string]callsign.RemoteState
	// StartUpdate downloads and rebuilds one source's database in the background.
	StartUpdate(source string) error
}

// Deps are what Run works with.
type Deps struct {
	Settings  func() settings.Settings
	Updates   Updates
	Callsigns Callsigns
	Logf      func(format string, args ...any) // default log.Printf
}

// Run performs the launch tasks the settings ask for, one after another. It reads the settings
// once, so a change made on the Settings page takes effect the next time the program starts. It
// returns when the tasks have been started (an update it starts keeps running in the background)
// or when ctx is cancelled.
func Run(ctx context.Context, d Deps) {
	logf := d.Logf
	if logf == nil {
		logf = log.Printf
	}
	s := d.Settings()

	if s.CheckUpdatesOnStart && d.Updates != nil {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if res, err := d.Updates.Check(cctx); err != nil {
			logf("update check at launch: %v", err)
		} else if res.Newer {
			logf("a newer version is available: %s (you have %s)", res.Latest, res.Current)
		}
		cancel()
	}

	if s.CallsignLaunch != settings.LaunchOff && d.Callsigns != nil && ctx.Err() == nil {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		checks := d.Callsigns.CheckRemote(cctx)
		cancel()
		for id, st := range checks {
			switch {
			case st.Error != "":
				logf("callsign list %q: %s", id, st.Error)
			case st.Newer && s.CallsignLaunch == settings.LaunchUpdate:
				logf("callsign list %q has a newer list (%s): updating", id, st.Modified)
				if err := d.Callsigns.StartUpdate(id); err != nil {
					logf("callsign list %q could not be updated: %v", id, err)
				}
			case st.Newer:
				logf("callsign list %q has a newer list (%s)", id, st.Modified)
			}
		}
	}
}
