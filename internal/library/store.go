// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package library stores the user's own documents in a plain folder with a
// human-readable JSON index. Files are untrusted data: they are never parsed
// here beyond type sniffing, size limiting and hashing.
package library

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrDuplicate   = errors.New("this file is already in the library")
	ErrUnsupported = errors.New("unsupported file type (supported: PDF, plain text or Markdown, PNG, JPEG)")
	ErrTooLarge    = errors.New("file is too large")
	ErrNotFound    = errors.New("document not found")
	ErrInvalid     = errors.New("invalid value")
)

const (
	maxTitleRunes = 200
	maxTags       = 20
	maxTagRunes   = 40
)

// Doc is one library entry. The file on disk is <library>/<ID>.<Ext>; names
// supplied by users or remote servers are never used as paths.
type Doc struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Ext          string    `json:"ext"`
	Type         string    `json:"type"`
	Tags         []string  `json:"tags"`
	SourceURL    string    `json:"source_url,omitempty"`
	OriginalName string    `json:"original_name,omitempty"`
	Added        time.Time `json:"added"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
}

// Store is safe for concurrent use.
type Store struct {
	dir  string // data folder
	lib  string // dir/library
	mu   sync.Mutex
	docs []Doc
}

type index struct {
	Version int   `json:"version"`
	Docs    []Doc `json:"docs"`
}

// Open creates the folders if needed and loads the index.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, lib: filepath.Join(dir, "library")}
	if err := os.MkdirAll(s.lib, 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.indexPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var ix index
		if err := json.Unmarshal(b, &ix); err != nil {
			return nil, errors.New("library.json is damaged: " + err.Error())
		}
		s.docs = ix.Docs
	}
	return s, nil
}

func (s *Store) indexPath() string { return filepath.Join(s.dir, "library.json") }

// Path returns the file path of a document.
func (s *Store) Path(d Doc) string { return filepath.Join(s.lib, d.ID+"."+d.Ext) }

// Add copies r into the library. It returns ErrDuplicate (with the existing
// document) when a file with the same SHA-256 is already present.
func (s *Store) Add(r io.Reader, originalName, sourceURL string, maxBytes int64) (Doc, error) {
	tmp, err := os.CreateTemp(s.lib, ".incoming-*")
	if err != nil {
		return Doc{}, err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Doc{}, err
	}
	head = head[:n]
	ext, mime, ok := detect(head)
	if !ok {
		return Doc{}, ErrUnsupported
	}
	w := io.MultiWriter(tmp, h)
	if _, err := w.Write(head); err != nil {
		return Doc{}, err
	}
	size := int64(n)
	if int64(n) == 512 { // there may be more
		rest, err := io.Copy(w, io.LimitReader(r, maxBytes-size+1))
		if err != nil {
			return Doc{}, err
		}
		size += rest
	}
	if size > maxBytes {
		return Doc{}, ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		return Doc{}, err
	}

	sum := hex.EncodeToString(h.Sum(nil))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.docs {
		if d.SHA256 == sum {
			return d, ErrDuplicate
		}
	}
	doc := Doc{
		ID:           newID(),
		Title:        titleFromName(originalName),
		Ext:          ext,
		Type:         mime,
		Tags:         []string{},
		SourceURL:    sourceURL,
		OriginalName: limitRunes(baseName(originalName), maxTitleRunes),
		Added:        time.Now().UTC(),
		SHA256:       sum,
		Size:         size,
	}
	if err := os.Rename(tmpName, s.Path(doc)); err != nil {
		return Doc{}, err
	}
	done = true
	s.docs = append(s.docs, doc)
	if err := s.saveLocked(); err != nil {
		s.docs = s.docs[:len(s.docs)-1]
		os.Remove(s.Path(doc))
		return Doc{}, err
	}
	return doc, nil
}

// List returns documents newest first, filtered by a case-insensitive query
// matched against title, tags, original name and source URL.
func (s *Store) List(q string) []Doc {
	q = strings.ToLower(strings.TrimSpace(q))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Doc, 0, len(s.docs))
	for _, d := range s.docs {
		if q == "" || matches(d, q) {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Added.After(out[j].Added) })
	return out
}

func matches(d Doc, q string) bool {
	hay := strings.ToLower(d.Title + "\n" + strings.Join(d.Tags, "\n") + "\n" + d.OriginalName + "\n" + d.SourceURL)
	for _, term := range strings.Fields(q) {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

// Get returns one document.
func (s *Store) Get(id string) (Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.docs {
		if d.ID == id {
			return d, nil
		}
	}
	return Doc{}, ErrNotFound
}

// Update changes the title and/or tags. A nil argument leaves that field alone.
func (s *Store) Update(id string, title *string, tags *[]string) (Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.docs {
		if s.docs[i].ID != id {
			continue
		}
		old := s.docs[i]
		if title != nil {
			t := limitRunes(strings.TrimSpace(*title), maxTitleRunes)
			if t == "" {
				return Doc{}, ErrInvalid
			}
			s.docs[i].Title = t
		}
		if tags != nil {
			s.docs[i].Tags = cleanTags(*tags)
		}
		if err := s.saveLocked(); err != nil {
			s.docs[i] = old
			return Doc{}, err
		}
		return s.docs[i], nil
	}
	return Doc{}, ErrNotFound
}

// Delete removes a document and its file.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.docs {
		if d.ID != id {
			continue
		}
		rest := append(append([]Doc{}, s.docs[:i]...), s.docs[i+1:]...)
		prev := s.docs
		s.docs = rest
		if err := s.saveLocked(); err != nil {
			s.docs = prev
			return err
		}
		if err := os.Remove(s.Path(d)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return ErrNotFound
}

// saveLocked writes the index atomically. The caller holds s.mu.
func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(index{Version: 1, Docs: s.docs}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".library-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.indexPath()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// detect decides the file type from its content, never from its name.
func detect(head []byte) (ext, mime string, ok bool) {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "pdf", "application/pdf", true
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "png", "image/png", true
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg", "image/jpeg", true
	case isText(head):
		return "txt", "text/plain; charset=utf-8", true
	}
	return "", "", false
}

func isText(b []byte) bool {
	if len(b) == 0 || bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1] // tolerate a rune cut off at the sniff boundary
	}
	return utf8.Valid(b)
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func baseName(name string) string {
	return path.Base(strings.ReplaceAll(name, "\\", "/"))
}

func titleFromName(name string) string {
	b := baseName(name)
	if b == "." || b == "/" {
		b = ""
	}
	b = strings.TrimSuffix(b, path.Ext(b))
	b = strings.TrimSpace(limitRunes(b, maxTitleRunes))
	if b == "" {
		return "Untitled"
	}
	return b
}

func limitRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func cleanTags(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		t = limitRunes(t, maxTagRunes)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxTags {
			break
		}
	}
	return out
}
