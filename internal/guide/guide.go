// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package guide ships the user guide inside the program and puts a copy in the
// user's Library, so the help is on the computer even when there is no internet.
//
// The guide is our own writing (docs/user-guide), rendered to a PDF by
// tools/docshots/buildguide.py and embedded here.
package guide

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/desperatetim-es/groundwave/internal/library"
)

//go:embed groundwave-user-guide.pdf
var embedded []byte

// Title is the name the guide has in the Library.
const Title = "groundwλve User Guide"

const fileName = "groundwave-user-guide.pdf"

// marker records which guide was last put in the Library, so that a guide the user
// deleted is not put back, and a newer guide replaces an older one.
type marker struct {
	SHA256 string `json:"sha256"`
	ID     string `json:"id"`
}

// Seed adds the guide to the Library the first time it runs, and replaces it with the
// newer one after an upgrade. A guide that the user has deleted stays deleted.
// dir is the data folder (where the marker file lives).
func Seed(lib *library.Store, dir string) error { return seed(lib, dir, embedded) }

func seed(lib *library.Store, dir string, pdf []byte) error {
	sum := sha256.Sum256(pdf)
	hash := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, "guide.json")

	var m marker
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m) // a damaged marker is treated as "never added"
	}
	if m.SHA256 == hash {
		return nil
	}
	if m.ID != "" {
		if _, err := lib.Get(m.ID); err != nil {
			// The user deleted it: respect that, but remember this guide so we don't ask again.
			return save(path, marker{SHA256: hash, ID: m.ID})
		}
		if err := lib.Delete(m.ID); err != nil { // replace the older guide
			return err
		}
	}
	doc, err := lib.Add(bytes.NewReader(pdf), fileName, "", library.DefaultMaxBytes)
	if err != nil && !errors.Is(err, library.ErrDuplicate) { // a duplicate: the user already has this exact file
		return err
	}
	if err == nil {
		title, tags := Title, []string{"groundwave", "guide", "help"}
		if _, err := lib.Update(doc.ID, &title, &tags); err != nil {
			return err
		}
	}
	return save(path, marker{SHA256: hash, ID: doc.ID})
}

func save(path string, m marker) error {
	b, _ := json.Marshal(m)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
