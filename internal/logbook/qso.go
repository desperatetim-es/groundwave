// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package logbook

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kd9psm/groundwave/internal/geo"
)

// GridSource says where a contact's grid square came from.
const (
	SourceDatabase = "database" // filled from the callsign database (an approximate ZIP-centre grid)
	SourceUser     = "user"     // typed by the operator, or read from an ADIF file
)

// QSO is one contact. Dates and times are UTC.
type QSO struct {
	ID         int64   `json:"id"`
	Call       string  `json:"call"`
	Date       string  `json:"date"` // YYYYMMDD
	Time       string  `json:"time"` // HHMMSS
	FreqMHz    float64 `json:"freq_mhz"`
	Band       string  `json:"band"`
	Mode       string  `json:"mode"`
	Submode    string  `json:"submode,omitempty"`
	RSTSent    string  `json:"rst_sent,omitempty"`
	RSTRcvd    string  `json:"rst_rcvd,omitempty"`
	Name       string  `json:"name,omitempty"`
	QTH        string  `json:"qth,omitempty"`
	Grid       string  `json:"grid,omitempty"`        // the other station's grid
	GridSource string  `json:"grid_source,omitempty"` // SourceDatabase or SourceUser
	MyGrid     string  `json:"my_grid,omitempty"`     // this station's grid for the contact
	POTARef    string  `json:"pota_ref,omitempty"`
	SOTARef    string  `json:"sota_ref,omitempty"`
	MyPOTARef  string  `json:"my_pota_ref,omitempty"`
	MySOTARef  string  `json:"my_sota_ref,omitempty"`
	Comment    string  `json:"comment,omitempty"`

	// Extra holds ADIF fields groundwave does not have a column for, so that an import followed by
	// an export loses nothing.
	Extra map[string]string `json:"extra,omitempty"`
}

var (
	callOK = regexp.MustCompile(`^[A-Z0-9]+(/[A-Z0-9]+){0,2}$`)
	rstOK  = regexp.MustCompile(`^[0-9A-Za-z+\-]{1,6}$`)
)

// ErrInvalid wraps every problem with a QSO the operator can fix.
var ErrInvalid = errors.New("invalid contact")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// NormalizeGrid upper-cases the field and lower-cases the sub-square (en52wd becomes EN52wd) and checks
// it is a real 2, 4, 6 or 8 character grid square.
func NormalizeGrid(g string) (string, error) {
	g = strings.TrimSpace(g)
	if g == "" {
		return "", nil
	}
	if len(g)%2 != 0 || len(g) > 8 {
		return "", invalid("a grid square has 2, 4, 6 or 8 characters, like EN52 or EN52wd")
	}
	if _, _, err := geo.GridCenter(g); err != nil {
		return "", invalid("%q is not a grid square", g)
	}
	out := []byte(strings.ToUpper(g))
	for i := 4; i < 6 && i < len(out); i++ {
		out[i] = byte(strings.ToLower(string(out[i]))[0])
	}
	return string(out), nil
}

// Normalize checks a contact and puts every field in its canonical form. The band is worked out
// from the frequency when it was not given.
func (q *QSO) Normalize() error {
	q.Call = strings.ToUpper(strings.TrimSpace(q.Call))
	if !callOK.MatchString(q.Call) || len(q.Call) > 20 {
		return invalid("enter the other station's call sign")
	}
	d, err := time.Parse("20060102", strings.TrimSpace(q.Date))
	if err != nil || d.Year() < 1930 {
		return invalid("the date must be a real date, as YYYYMMDD")
	}
	q.Date = d.Format("20060102")
	t := strings.TrimSpace(q.Time)
	if len(t) == 4 {
		t += "00"
	}
	if _, err := time.Parse("150405", t); err != nil || len(t) != 6 {
		return invalid("the time must be a UTC time, as HHMM or HHMMSS")
	}
	q.Time = t
	if q.FreqMHz < 0 || q.FreqMHz > 1e7 {
		return invalid("the frequency must be in MHz")
	}
	if q.Band != "" {
		if q.Band = bandName(q.Band); q.Band == "" {
			return invalid("unknown band")
		}
	} else if q.FreqMHz > 0 {
		q.Band = BandFor(q.FreqMHz)
	}
	if q.Band == "" && q.FreqMHz == 0 {
		return invalid("enter a frequency or a band")
	}
	q.Mode, q.Submode = NormalizeMode(q.Mode, q.Submode)
	if q.Mode == "" {
		return invalid("enter a mode, such as SSB, CW, FM or FT8")
	}
	for _, f := range []*string{&q.RSTSent, &q.RSTRcvd} {
		*f = strings.TrimSpace(*f)
		if *f != "" && !rstOK.MatchString(*f) {
			return invalid("a signal report is 1 to 6 letters or digits, like 59 or 599")
		}
	}
	if q.Grid, err = NormalizeGrid(q.Grid); err != nil {
		return err
	}
	if q.MyGrid, err = NormalizeGrid(q.MyGrid); err != nil {
		return err
	}
	switch {
	case q.Grid == "":
		q.GridSource = ""
	case q.GridSource != SourceDatabase:
		q.GridSource = SourceUser
	}
	for _, f := range []*string{&q.Name, &q.QTH, &q.Comment} {
		*f = oneLine(*f)
	}
	for _, f := range []*string{&q.POTARef, &q.SOTARef, &q.MyPOTARef, &q.MySOTARef} {
		*f = strings.ToUpper(strings.TrimSpace(*f))
	}
	if len(q.Name) > 100 || len(q.QTH) > 100 || len(q.Comment) > 2000 {
		return invalid("a name or place is limited to 100 characters, a note to 2000")
	}
	return nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// FromFields converts an ADIF record to a contact. Fields without a column are kept in Extra.
func FromFields(f Fields) QSO {
	q := QSO{Extra: map[string]string{}}
	for k, v := range f {
		switch k {
		case "call":
			q.Call = v
		case "qso_date":
			q.Date = v
		case "time_on":
			q.Time = v
		case "freq":
			q.FreqMHz, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
		case "band":
			q.Band = v
		case "mode":
			q.Mode = v
		case "submode":
			q.Submode = v
		case "rst_sent":
			q.RSTSent = v
		case "rst_rcvd":
			q.RSTRcvd = v
		case "name":
			q.Name = v
		case "qth":
			q.QTH = v
		case "gridsquare":
			q.Grid = v
		case "my_gridsquare":
			q.MyGrid = v
		case "pota_ref":
			q.POTARef = v
		case "sota_ref":
			q.SOTARef = v
		case "my_pota_ref":
			q.MyPOTARef = v
		case "my_sota_ref":
			q.MySOTARef = v
		case "comment":
			q.Comment = v
		default:
			q.Extra[k] = v
		}
	}
	if q.Grid != "" {
		q.GridSource = SourceUser
	}
	if len(q.Extra) == 0 {
		q.Extra = nil
	}
	return q
}

// ToFields converts a contact to an ADIF record. A grid that was only filled in from the callsign
// database is an approximation, not something the other operator told us, so it is not exported.
func (q QSO) ToFields() Fields {
	f := Fields{}
	for k, v := range q.Extra {
		f[k] = v
	}
	set := func(k, v string) {
		if v != "" {
			f[k] = v
		}
	}
	set("call", q.Call)
	set("qso_date", q.Date)
	set("time_on", q.Time)
	if q.FreqMHz > 0 {
		f["freq"] = strconv.FormatFloat(q.FreqMHz, 'f', -1, 64)
	}
	set("band", q.Band)
	set("mode", q.Mode)
	set("submode", q.Submode)
	set("rst_sent", q.RSTSent)
	set("rst_rcvd", q.RSTRcvd)
	set("name", q.Name)
	set("qth", q.QTH)
	if q.GridSource != SourceDatabase {
		set("gridsquare", q.Grid)
	}
	set("my_gridsquare", q.MyGrid)
	set("pota_ref", q.POTARef)
	set("sota_ref", q.SOTARef)
	set("my_pota_ref", q.MyPOTARef)
	set("my_sota_ref", q.MySOTARef)
	set("comment", q.Comment)
	return f
}
