// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Command licenses writes THIRD_PARTY_LICENSES.txt: the license notices of every
// third-party Go module that is linked into a groundwave release, plus the Go
// standard library and the data files embedded in the program.
//
//	go run ./tools/licenses            (from the repository folder) rewrite the file
//	go run ./tools/licenses -check     fail if the file is out of date (used by the test and CI)
//
// "Linked into a release" is worked out with `go list -deps` for every release
// target (Windows amd64; Linux amd64, arm64 and arm), so modules that only exist
// in the build graph (tooling, test-only code) are not listed.
//
// Run it again whenever go.mod changes. Needs the module sources in the module
// cache (`go mod download`).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The release targets; keep in step with build-windows.ps1 and tools/release.
var targets = []struct{ goos, goarch string }{
	{"windows", "amd64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"linux", "arm"},
}

// Notice files looked for in each module's root folder.
func isNoticeFile(name string) bool {
	u := strings.ToUpper(name)
	for _, p := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

type module struct{ path, version, dir string }

const header = `groundwλve third-party notices

groundwλve itself is free software under the GNU General Public License,
version 3 (see LICENSE). It includes the software and data listed below, each
under its own license, reproduced here as those licenses require.

Contents
  1. Data files embedded in the program
  2. The Go standard library
  3. Third-party Go modules
`

const dataSection = `Data files embedded in the program

  ZIP code centre points (internal/callsign/zcta.csv)
    From the U.S. Census Bureau 2020 Gazetteer (ZIP Code Tabulation Areas,
    file dated 2021-01-22). U.S. government data in the public domain.
    Source: https://www2.census.gov/geo/docs/maps-data/data/gazetteer/2020_Gazetteer/2020_Gaz_zcta_national.zip

  The user guide (internal/guide) is groundwλve's own writing, covered by
  the groundwλve license.

  The FCC amateur license database is not included in the program. It is
  downloaded by the user, from the FCC, when they press Update Database.
`

func main() {
	out := flag.String("o", "THIRD_PARTY_LICENSES.txt", "file to write")
	check := flag.Bool("check", false, "report whether the file is up to date instead of writing it")
	flag.Parse()

	text, err := build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "licenses:", err)
		os.Exit(1)
	}
	if *check {
		have, err := os.ReadFile(*out)
		if err != nil || !bytes.Equal(have, text) {
			fmt.Fprintf(os.Stderr, "licenses: %s is out of date; run: go run ./tools/licenses\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, text, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "licenses:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}

func build() ([]byte, error) {
	mods, err := linkedModules()
	if err != nil {
		return nil, err
	}
	goroot, err := goEnv("GOROOT")
	if err != nil {
		return nil, err
	}
	goLicense, err := readText(filepath.Join(goroot, "LICENSE"))
	if err != nil {
		return nil, fmt.Errorf("Go standard library license: %w", err)
	}

	var b strings.Builder
	b.WriteString(header)
	section(&b, "1. "+dataSection)
	section(&b, "2. The Go standard library\n\n  Copyright (c) The Go Authors. The Go version used to build is recorded in\n  the program's build information.\n\n--- LICENSE ---\n"+goLicense)
	b.WriteString("\n" + rule + "\n3. Third-party Go modules\n")
	for _, m := range mods {
		b.WriteString("\n" + rule + "\n")
		fmt.Fprintf(&b, "%s %s\n", m.path, m.version)
		files, err := noticeFiles(m.dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s %s: no license file found; add its notice by hand before releasing", m.path, m.version)
		}
		for _, f := range files {
			text, err := readText(filepath.Join(m.dir, f))
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "\n--- %s ---\n%s", f, text)
		}
	}
	return []byte(b.String()), nil
}

const rule = "================================================================================"

func section(b *strings.Builder, body string) {
	b.WriteString("\n" + rule + "\n" + strings.TrimRight(body, "\n") + "\n")
}

// linkedModules returns the third-party modules compiled into any release target.
func linkedModules() ([]module, error) {
	seen := map[string]module{}
	for _, t := range targets {
		cmd := exec.Command("go", "list", "-deps", "-f",
			"{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}{{end}}",
			"./cmd/groundwave")
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w", t.goos, t.goarch, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) != 3 || f[2] == "" {
				continue
			}
			seen[f[0]] = module{f[0], f[1], f[2]}
		}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].path < mods[j].path })
	return mods, nil
}

func noticeFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && isNoticeFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	return strings.TrimSpace(string(out)), err
}

// readText reads a file with LF line endings and no trailing blank lines, so the
// output is identical on every OS.
func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.TrimRight(s, "\n") + "\n", nil
}
