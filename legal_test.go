// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package groundwave

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every module go.mod requires is compiled into a release, so each must have a
// notice in THIRD_PARTY_LICENSES.txt. If this fails, run: go run ./tools/licenses
func TestThirdPartyNoticesCoverEveryModule(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	notices, err := Legal.ReadFile("THIRD_PARTY_LICENSES.txt")
	if err != nil {
		t.Fatal(err)
	}
	require := regexp.MustCompile(`(?m)^\s+(\S+)\s+v\S+`)
	n := 0
	for _, m := range require.FindAllStringSubmatch(string(mod), -1) {
		n++
		if !strings.Contains(string(notices), "\n"+m[1]+" v") {
			t.Errorf("go.mod requires %s but THIRD_PARTY_LICENSES.txt has no notice for it; run: go run ./tools/licenses", m[1])
		}
	}
	if n == 0 {
		t.Fatal("found no requirements in go.mod; the test needs updating")
	}
}

func TestLicenseIsTheGPL(t *testing.T) {
	b, err := Legal.ReadFile("LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "GNU GENERAL PUBLIC LICENSE") {
		t.Error("LICENSE does not look like the GPL")
	}
}

// Every Go source file starts with the SPDX license line (the project is GPL-3.0-only).
func TestSourceFilesHaveLicenseHeader(t *testing.T) {
	const want = "// SPDX-License-Identifier: GPL-3.0-only"
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if first, _, _ := strings.Cut(string(b), "\n"); strings.TrimSpace(first) != want {
			t.Errorf("%s: first line must be %q", path, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
