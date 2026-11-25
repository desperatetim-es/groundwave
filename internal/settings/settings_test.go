// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package settings

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsAreAllOff(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "settings.json")).Get()
	if s.CheckUpdatesOnStart || len(s.HiddenPages) != 0 || s.CallsignLaunch != LaunchOff {
		t.Fatalf("defaults are not all off: %+v", s)
	}
	if s.HiddenPages == nil {
		t.Error("HiddenPages should be an empty list (it becomes JSON [] not null)")
	}
}

func TestChoicesPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json") // the folder does not exist yet
	st := Open(path)
	got, err := st.Update(func(s *Settings) {
		s.CheckUpdatesOnStart = true
		s.HiddenPages = []string{"logbook", "library", "logbook"} // out of order, with a duplicate
		s.CallsignLaunch = LaunchUpdate
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{CheckUpdatesOnStart: true, HiddenPages: []string{"library", "logbook"}, CallsignLaunch: LaunchUpdate}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if again := Open(path).Get(); !reflect.DeepEqual(again, want) {
		t.Fatalf("after a restart: %+v, want %+v", again, want)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("the temporary file was left behind")
	}
}

func TestInvalidChoicesAreRefusedAndChangeNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	st := Open(path)
	st.Update(func(s *Settings) { s.CheckUpdatesOnStart = true })
	for name, change := range map[string]func(*Settings){
		"unknown page":   func(s *Settings) { s.HiddenPages = []string{"settings"} },
		"empty page":     func(s *Settings) { s.HiddenPages = []string{""} },
		"path-like page": func(s *Settings) { s.HiddenPages = []string{"../library"} },
		"unknown launch": func(s *Settings) { s.CallsignLaunch = "always" },
	} {
		_, err := st.Update(change)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: got %v, want a ValidationError", name, err)
		}
		if g := st.Get(); !g.CheckUpdatesOnStart || len(g.HiddenPages) != 0 || g.CallsignLaunch != LaunchOff {
			t.Errorf("%s changed the settings: %+v", name, g)
		}
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "always") || strings.Contains(string(b), "../") {
		t.Error("an invalid value reached the file")
	}
}

func TestADamagedOrHostileFileMeansDefaultsNotACrash(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"not json":        "{{{{",
		"empty":           "",
		"wrong types":     `{"check_updates_on_start": "yes"}`,
		"unknown launch":  `{"callsign_launch": "always"}`,
		"unknown page":    `{"hidden_pages": ["../../etc"]}`,
		"null list":       `{"hidden_pages": null}`,
		"huge nonsense":   strings.Repeat("x", 1<<20),
		"valid but extra": `{"check_updates_on_start": true, "future_option": 1}`,
	} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(content), 0o644)
		s := Open(path).Get()
		if s.CallsignLaunch != LaunchOff || len(s.HiddenPages) != 0 {
			t.Errorf("%s: %+v", name, s)
		}
		if name == "valid but extra" && !s.CheckUpdatesOnStart {
			t.Errorf("%s: a file with an extra, unknown key should still be read", name)
		}
		if name != "valid but extra" && s.CheckUpdatesOnStart {
			t.Errorf("%s: a bad file must give the defaults, got %+v", name, s)
		}
	}
}

func TestGetReturnsACopy(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), "settings.json"))
	st.Update(func(s *Settings) { s.HiddenPages = []string{"library"} })
	g := st.Get()
	g.HiddenPages[0] = "logbook"
	if st.Get().HiddenPages[0] != "library" {
		t.Fatal("changing the returned value changed the store")
	}
}

func TestAFailedSaveChangesNothing(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("a file where a folder is needed"), 0o644)
	st := Open(filepath.Join(blocker, "settings.json"))
	if _, err := st.Update(func(s *Settings) { s.CheckUpdatesOnStart = true }); err == nil || strings.Contains(err.Error(), blocker) {
		t.Fatalf("got %v, want a friendly error that does not leak the path", err)
	}
	if st.Get().CheckUpdatesOnStart {
		t.Fatal("a failed save still changed the settings in memory")
	}
}
