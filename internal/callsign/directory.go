// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package callsign

import (
	"errors"
	"time"
)

// ErrUnknownSource is returned for a source ID the program does not have.
var ErrUnknownSource = errors.New("there is no callsign source with that name")

// SourceStatus is the state of one country's database, for the UI.
type SourceStatus struct {
	ID       string `json:"id"`
	Country  string `json:"country"`
	Org      string `json:"org"`
	Download string `json:"download"` // what to expect, for example "about 200 MB"
	Disk     string `json:"disk"`     // free disk space needed while it builds
	Credit   string `json:"credit"`   // attribution to show
	Status
}

// DirOptions configures a Directory. Only Dir is required.
type DirOptions struct {
	Dir string // the callsigns folder: one database file per source
	Now func() time.Time
	// URLs overrides the download address of a source, by ID, and AnyHostForTests lifts the https
	// and host restrictions. Tests only.
	URLs            map[string]string
	AnyHostForTests bool
}

// Directory holds one database per source (country) and searches across all that have
// been downloaded.
type Directory struct {
	stores map[string]*Store
}

// NewDirectory opens the databases that exist in o.Dir.
func NewDirectory(o DirOptions) (*Directory, error) {
	d := &Directory{stores: map[string]*Store{}}
	for _, src := range Sources {
		s, err := NewStore(StoreOptions{Dir: o.Dir, Source: src, URL: o.URLs[src.ID], AnyHostForTests: o.AnyHostForTests, Now: o.Now})
		if err != nil {
			d.Close()
			return nil, err
		}
		d.stores[src.ID] = s
	}
	return d, nil
}

// Close stops any update and closes every database.
func (d *Directory) Close() {
	for _, s := range d.stores {
		s.Close()
	}
}

// Store returns one source's store, or nil.
func (d *Directory) Store(id string) *Store { return d.stores[id] }

// Status summarises every source. The top-level fields are the totals: Available is true
// if any database exists, Records is the sum, and Update is a running update if there is one.
func (d *Directory) Status() Status {
	var all Status
	for _, src := range Sources {
		st := d.stores[src.ID].Status()
		all.Sources = append(all.Sources, SourceStatus{ID: src.ID, Country: src.Country, Org: src.Org, Download: src.Download, Disk: src.Disk, Credit: src.Credit, Status: st})
		if st.Available {
			all.Available = true
			all.Records += st.Records
		}
		if st.Update.Running && !all.Update.Running {
			all.Update = st.Update
		}
	}
	return all
}

// StartUpdate downloads and builds one source's database in the background.
func (d *Directory) StartUpdate(id string) error {
	s := d.stores[id]
	if s == nil {
		return ErrUnknownSource
	}
	return s.StartUpdate()
}

// CancelUpdate stops one source's running update.
func (d *Directory) CancelUpdate(id string) {
	if s := d.stores[id]; s != nil {
		s.CancelUpdate()
	}
}

// each runs f on every source whose database is ready. It reports ErrUpdating if none is
// ready but one is being rebuilt, and ErrNoDatabase if none exists at all.
func (d *Directory) each(f func(*Store) ([]Record, error)) ([][]Record, error) {
	var lists [][]Record
	updating := false
	for _, src := range Sources {
		res, err := f(d.stores[src.ID])
		switch {
		case errors.Is(err, ErrNoDatabase):
			continue
		case errors.Is(err, ErrUpdating):
			updating = true
			continue
		case err != nil:
			return nil, err
		}
		lists = append(lists, res)
	}
	if len(lists) == 0 {
		if updating {
			return nil, ErrUpdating
		}
		return nil, ErrNoDatabase
	}
	return lists, nil
}

// Search finds licenses in every downloaded database; see DB.Search. At most limit
// results come back. An exact call sign match comes first; otherwise the sources take
// turns, so no country crowds out the others.
func (d *Directory) Search(q string, limit int) ([]Record, error) {
	p, err := parseQuery(q)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	lists, err := d.each(func(s *Store) ([]Record, error) { return s.Search(q, limit) })
	if err != nil {
		return nil, err
	}
	return merge(lists, p.call, limit), nil
}

// Lookup returns the licenses for one exact call sign from every downloaded database.
func (d *Directory) Lookup(call string) ([]Record, error) {
	lists, err := d.each(func(s *Store) ([]Record, error) { return s.Lookup(call) })
	if err != nil {
		return nil, err
	}
	return merge(lists, NormalizeCall(call), maxLimit), nil
}

// merge combines each source's results: exact call sign matches first, then one result from
// each source in turn, up to limit.
func merge(lists [][]Record, call string, limit int) []Record {
	out := []Record{}
	if call != "" {
		for i, l := range lists {
			rest := l[:0:0]
			for _, r := range l {
				if r.Call == call {
					out = append(out, r)
				} else {
					rest = append(rest, r)
				}
			}
			lists[i] = rest
		}
	}
	for round := 0; len(out) < limit; round++ {
		added := false
		for _, l := range lists {
			if round < len(l) && len(out) < limit {
				out = append(out, l[round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
