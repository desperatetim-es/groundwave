// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package guide

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/desperatetim-es/groundwave/internal/library"
)

// pdfWith returns a tiny file the library accepts as a PDF.
func pdfWith(tag string) []byte { return []byte("%PDF-1.4\n% " + tag + "\n%%EOF\n") }

func open(t *testing.T) (*library.Store, string) {
	t.Helper()
	dir := t.TempDir()
	lib, err := library.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lib, dir
}

func TestEmbeddedGuideIsAPDF(t *testing.T) {
	if len(embedded) < 10_000 || string(embedded[:5]) != "%PDF-" {
		t.Fatalf("the embedded guide is not a usable PDF (%d bytes)", len(embedded))
	}
}

func TestSeedAddsOnceAndTitlesIt(t *testing.T) {
	lib, dir := open(t)
	if err := seed(lib, dir, pdfWith("v1")); err != nil {
		t.Fatal(err)
	}
	docs := lib.List("")
	if len(docs) != 1 || docs[0].Title != Title {
		t.Fatalf("docs = %+v", docs)
	}
	if err := seed(lib, dir, pdfWith("v1")); err != nil || len(lib.List("")) != 1 {
		t.Fatalf("second run added another copy: %v", lib.List(""))
	}
}

func TestSeedRespectsDeletion(t *testing.T) {
	lib, dir := open(t)
	seed(lib, dir, pdfWith("v1"))
	if err := lib.Delete(lib.List("")[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := seed(lib, dir, pdfWith("v1")); err != nil || len(lib.List("")) != 0 {
		t.Fatalf("deleted guide came back: %v", lib.List(""))
	}
	// Even a newer guide does not come back once the user deleted the old one.
	if err := seed(lib, dir, pdfWith("v2")); err != nil || len(lib.List("")) != 0 {
		t.Fatalf("deleted guide came back with an upgrade: %v", lib.List(""))
	}
}

func TestSeedReplacesAnOlderGuide(t *testing.T) {
	lib, dir := open(t)
	seed(lib, dir, pdfWith("v1"))
	if err := seed(lib, dir, pdfWith("v2")); err != nil {
		t.Fatal(err)
	}
	docs := lib.List("")
	if len(docs) != 1 || docs[0].Title != Title {
		t.Fatalf("after upgrade: %+v", docs)
	}
}

func TestSeedSurvivesADamagedMarker(t *testing.T) {
	lib, dir := open(t)
	os.WriteFile(filepath.Join(dir, "guide.json"), []byte("not json"), 0o644)
	if err := seed(lib, dir, pdfWith("v1")); err != nil || len(lib.List("")) != 1 {
		t.Fatalf("err=%v docs=%v", err, lib.List(""))
	}
}

func TestSeedWhenTheUserAlreadyHasThisFile(t *testing.T) {
	lib, dir := open(t)
	if _, err := lib.Add(bytes.NewReader(pdfWith("v1")), "mine.pdf", "", library.DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}
	if err := seed(lib, dir, pdfWith("v1")); err != nil || len(lib.List("")) != 1 {
		t.Fatalf("err=%v docs=%v", err, lib.List(""))
	}
}
