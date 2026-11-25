// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package launch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/settings"
	"github.com/kd9psm/groundwave/internal/updatecheck"
)

type fakeUpdates struct {
	calls int
	res   updatecheck.Result
	err   error
}

func (f *fakeUpdates) Check(context.Context) (updatecheck.Result, error) {
	f.calls++
	return f.res, f.err
}

type fakeCallsigns struct {
	checks    int
	remote    map[string]callsign.RemoteState
	updated   []string
	updateErr error
}

func (f *fakeCallsigns) CheckRemote(context.Context) map[string]callsign.RemoteState {
	f.checks++
	return f.remote
}
func (f *fakeCallsigns) StartUpdate(id string) error {
	f.updated = append(f.updated, id)
	return f.updateErr
}

func run(s settings.Settings, u *fakeUpdates, c *fakeCallsigns) []string {
	var log []string
	Run(context.Background(), Deps{
		Settings: func() settings.Settings { return s }, Updates: u, Callsigns: c,
		Logf: func(f string, a ...any) { log = append(log, fmt.Sprintf(f, a...)) },
	})
	return log
}

func TestDefaultSettingsContactNothing(t *testing.T) {
	u := &fakeUpdates{}
	c := &fakeCallsigns{remote: map[string]callsign.RemoteState{"us": {Newer: true}}}
	if log := run(settings.Default(), u, c); len(log) != 0 {
		t.Fatalf("logged %v", log)
	}
	if u.calls != 0 || c.checks != 0 || len(c.updated) != 0 {
		t.Fatalf("with the defaults something ran: updates=%d checks=%d updated=%v", u.calls, c.checks, c.updated)
	}
}

func TestUpdateCheckOnlyWhenAskedFor(t *testing.T) {
	u := &fakeUpdates{res: updatecheck.Result{Current: "1.0.0", Latest: "1.1.0", Newer: true}}
	c := &fakeCallsigns{}
	log := run(settings.Settings{CheckUpdatesOnStart: true, CallsignLaunch: settings.LaunchOff}, u, c)
	if u.calls != 1 || c.checks != 0 {
		t.Fatalf("updates=%d checks=%d, want 1 and 0", u.calls, c.checks)
	}
	if len(log) != 1 || !strings.Contains(log[0], "1.1.0") {
		t.Errorf("log: %v", log)
	}
	// a failed check is logged and does not stop anything else
	u = &fakeUpdates{err: errors.New("Could not reach GitHub. You may be offline.")}
	c = &fakeCallsigns{remote: map[string]callsign.RemoteState{"ca": {Newer: false}}}
	log = run(settings.Settings{CheckUpdatesOnStart: true, CallsignLaunch: settings.LaunchCheck}, u, c)
	if c.checks != 1 || len(log) == 0 || !strings.Contains(log[0], "offline") {
		t.Fatalf("a failed update check should not stop the callsign check: checks=%d log=%v", c.checks, log)
	}
}

func TestCallsignCheckNeverUpdates(t *testing.T) {
	c := &fakeCallsigns{remote: map[string]callsign.RemoteState{"us": {Newer: true, Modified: "Mon, 23 Nov 2026 07:00:00 GMT"}}}
	run(settings.Settings{CallsignLaunch: settings.LaunchCheck}, &fakeUpdates{}, c)
	if c.checks != 1 || len(c.updated) != 0 {
		t.Fatalf("check mode: checks=%d updated=%v, want 1 check and no update", c.checks, c.updated)
	}
}

func TestCallsignUpdateOnlyOutOfDateDatabases(t *testing.T) {
	c := &fakeCallsigns{remote: map[string]callsign.RemoteState{
		"us": {Newer: true, Modified: "Mon, 23 Nov 2026 07:00:00 GMT"},
		"ca": {Newer: false, Modified: "Tue, 24 Nov 2026 07:00:00 GMT"},
	}}
	run(settings.Settings{CallsignLaunch: settings.LaunchUpdate}, &fakeUpdates{}, c)
	if len(c.updated) != 1 || c.updated[0] != "us" {
		t.Fatalf("updated %v, want only us", c.updated)
	}
}

func TestACheckThatCouldNotTellDoesNotUpdate(t *testing.T) {
	c := &fakeCallsigns{remote: map[string]callsign.RemoteState{
		"us": {Newer: true, Error: "FCC does not say when its list was last changed."}, // an error wins: never download on a guess
		"ca": {Error: "Could not reach ISED's server. You may be offline."},
	}}
	log := run(settings.Settings{CallsignLaunch: settings.LaunchUpdate}, &fakeUpdates{}, c)
	if len(c.updated) != 0 {
		t.Fatalf("updated %v after a check that could not tell", c.updated)
	}
	if len(log) != 2 {
		t.Errorf("both problems should be logged: %v", log)
	}
}

func TestAFailedUpdateStartIsLoggedAndOthersStillRun(t *testing.T) {
	c := &fakeCallsigns{updateErr: errors.New("busy"), remote: map[string]callsign.RemoteState{"us": {Newer: true}, "ca": {Newer: true}}}
	log := run(settings.Settings{CallsignLaunch: settings.LaunchUpdate}, &fakeUpdates{}, c)
	sort.Strings(c.updated)
	if strings.Join(c.updated, ",") != "ca,us" {
		t.Fatalf("both should be attempted: %v", c.updated)
	}
	if n := strings.Count(strings.Join(log, "\n"), "could not be updated"); n != 2 {
		t.Errorf("both failures should be logged: %v", log)
	}
}

func TestNothingRunsOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &fakeCallsigns{remote: map[string]callsign.RemoteState{"us": {Newer: true}}}
	Run(ctx, Deps{Settings: func() settings.Settings { return settings.Settings{CallsignLaunch: settings.LaunchUpdate} }, Callsigns: c, Logf: func(string, ...any) {}})
	if c.checks != 0 || len(c.updated) != 0 {
		t.Fatalf("a cancelled run still did something: checks=%d updated=%v", c.checks, c.updated)
	}
}

func TestMissingCollaboratorsAreFine(t *testing.T) {
	Run(context.Background(), Deps{Settings: func() settings.Settings {
		return settings.Settings{CheckUpdatesOnStart: true, CallsignLaunch: settings.LaunchUpdate}
	}, Logf: func(string, ...any) {}}) // no Updates, no Callsigns: must not panic
}
