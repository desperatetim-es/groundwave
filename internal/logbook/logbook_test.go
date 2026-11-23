// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package logbook

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestParseADIF(t *testing.T) {
	in := "header text <ADIF_VER:5>3.1.4 <PROGRAMID:3>abc <EOH>\n" +
		"<CALL:5>W1AW  <QSO_DATE:8>20260102<TIME_ON:4>1304<FREQ:6>14.074<MODE:3>FT8<NAME:5>José <EOR>\n" +
		"<call:4>K9XX<qso_date:8>20260103<time_on:6>010203<band:3>40m<mode:2>CW<comment:12>two\nlines ok<eor>"
	recs, err := ParseADIF([]byte(in))
	if err != nil || len(recs) != 2 {
		t.Fatalf("%v %v", recs, err)
	}
	if recs[0]["call"] != "W1AW " || recs[0]["name"] != "José" || recs[0]["freq"] != "14.074" {
		t.Errorf("%q", recs[0])
	}
	if _, has := recs[0]["adif_ver"]; has {
		t.Error("the header leaked into a record")
	}
	if recs[1]["comment"] != "two\nlines ok" || recs[1]["time_on"] != "010203" {
		t.Errorf("%q", recs[1])
	}
	// damaged input never panics
	for _, bad := range []string{"<", "<call:>x", "<call:99>x", "<call:-1>x<eor>", "<<<>>>", "<eor><eor>", ""} {
		ParseADIF([]byte(bad))
	}
}

func TestADIFRoundTrip(t *testing.T) {
	var b bytes.Buffer
	in := []Fields{{"call": "W1AW", "name": "José Ñ", "comment": "a\nb", "freq": "7.2"}}
	if err := WriteADIF(&b, "groundwave", "1", in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<NAME:8>José Ñ") { // byte length: é and Ñ are two bytes each
		t.Errorf("lengths are not byte counts: %q", b.String())
	}
	out, _ := ParseADIF(b.Bytes())
	if len(out) != 1 || out[0]["name"] != "José Ñ" || out[0]["comment"] != "a b" {
		t.Errorf("%q", out)
	}
}

func TestBands(t *testing.T) {
	for f, want := range map[float64]string{1.9: "160m", 7.074: "40m", 14.074: "20m", 146.52: "2m", 223.5: "1.25m", 446.0: "70cm", 5.357: "60m", 3.0: ""} {
		if got := BandFor(f); got != want {
			t.Errorf("BandFor(%v) = %q, want %q", f, got, want)
		}
	}
	if m, s := NormalizeMode("ft4", ""); m != "MFSK" || s != "FT4" {
		t.Errorf("%s %s", m, s)
	}
	if m, s := NormalizeMode("usb", ""); m != "SSB" || s != "USB" {
		t.Errorf("%s %s", m, s)
	}
	if m, s := NormalizeMode("ft8", ""); m != "FT8" || s != "" {
		t.Errorf("%s %s", m, s)
	}
}

func good() QSO {
	return QSO{Call: "w1aw", Date: "20260102", Time: "1304", FreqMHz: 14.074, Mode: "ft8", Grid: "fn31pr"}
}

func TestNormalizeAndValidate(t *testing.T) {
	q := good()
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if q.Call != "W1AW" || q.Time != "130400" || q.Band != "20m" || q.Mode != "FT8" || q.Grid != "FN31pr" || q.GridSource != SourceUser {
		t.Errorf("%+v", q)
	}
	for name, mut := range map[string]func(*QSO){
		"no call":    func(q *QSO) { q.Call = "" },
		"bad call":   func(q *QSO) { q.Call = "W1 AW" },
		"bad date":   func(q *QSO) { q.Date = "20260230" },
		"old date":   func(q *QSO) { q.Date = "19000101" },
		"bad time":   func(q *QSO) { q.Time = "2561" },
		"no mode":    func(q *QSO) { q.Mode = "" },
		"bad grid":   func(q *QSO) { q.Grid = "ZZ99" },
		"odd grid":   func(q *QSO) { q.Grid = "EN5" },
		"no freq":    func(q *QSO) { q.FreqMHz, q.Band = 0, "" },
		"bad band":   func(q *QSO) { q.Band = "99m" },
		"bad report": func(q *QSO) { q.RSTSent = "5 9" },
	} {
		q := good()
		mut(&q)
		if err := q.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a portable call and a band without a frequency are fine
	q = QSO{Call: "w1aw/p", Date: "20260102", Time: "1304", Band: "20M", Mode: "cw"}
	if err := q.Normalize(); err != nil || q.Call != "W1AW/P" || q.Band != "20m" {
		t.Errorf("%+v %v", q, err)
	}
}

func TestStoreCRUD(t *testing.T) {
	s := openTest(t)
	a, err := s.Add(good())
	if err != nil || a.ID == 0 {
		t.Fatal(a, err)
	}
	b := good()
	b.Call, b.Date, b.Name, b.Comment = "K9XX", "20260105", "Pat", "100% sure_thing"
	b, _ = s.Add(b)

	list, total, err := s.List("", 10, 0)
	if err != nil || total != 2 || list[0].Call != "K9XX" {
		t.Fatalf("newest first: %+v %v", list, err)
	}
	for q, want := range map[string]int{"w1": 1, "pat": 1, "100%": 1, "sure_": 1, "%": 1, "_": 1, "fn31": 2, "zzz": 0, "9XX": 0, "": 2} {
		if _, n, _ := s.List(q, 10, 0); n != want {
			t.Errorf("List(%q) matched %d, want %d", q, n, want)
		}
	}
	if l, total, _ := s.List("", 1, 1); len(l) != 1 || total != 2 || l[0].Call != "W1AW" {
		t.Errorf("paging: %+v", l)
	}

	a.RSTRcvd = "57"
	if u, err := s.Update(a.ID, a); err != nil || u.RSTRcvd != "57" {
		t.Fatal(u, err)
	}
	if g, _ := s.Get(a.ID); g.RSTRcvd != "57" {
		t.Errorf("%+v", g)
	}
	bad := a
	bad.Call = ""
	if _, err := s.Update(a.ID, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("%v", err)
	}
	if _, err := s.Update(999, a); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.Get(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	// hostile text is stored as text
	h := good()
	h.Call, h.Comment = "K1HK", `'); DROP TABLE qso; --`
	s.Add(h)
	if _, n, _ := s.List("", 10, 0); n != 2 {
		t.Errorf("the table was damaged: %d", n)
	}
}

func TestImportExport(t *testing.T) {
	s := openTest(t)
	file := "<ADIF_VER:5>3.1.4<EOH>\n" +
		"<CALL:4>W1AW<QSO_DATE:8>20260102<TIME_ON:6>130455<FREQ:6>14.074<MODE:3>FT8<GRIDSQUARE:4>FN31<APP_X_FOO:3>bar<QSL_SENT:1>Y<EOR>\n" +
		"<CALL:4>W1AW<QSO_DATE:8>20260102<TIME_ON:6>130410<FREQ:6>14.075<MODE:3>FT8<EOR>\n" + // same minute, band and mode: a duplicate
		"<CALL:4>K9XX<QSO_DATE:8>20260102<TIME_ON:4>1400<BAND:3>40m<MODE:3>USB<EOR>\n" +
		"<CALL:0><QSO_DATE:8>20260102<TIME_ON:4>1400<BAND:3>40m<MODE:2>CW<EOR>\n" // no call
	res, err := s.ImportADIF([]byte(file))
	if err != nil || res.Records != 4 || res.Added != 2 || res.Duplicates != 1 || res.Skipped != 1 || len(res.Problems) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	// importing the same file again adds nothing
	if res, _ := s.ImportADIF([]byte(file)); res.Added != 0 || res.Duplicates != 3 {
		t.Errorf("%+v", res)
	}
	all, _ := s.All()
	if len(all) != 2 || all[1].Mode != "SSB" || all[1].Submode != "USB" || all[0].Grid != "FN31" || all[0].Extra["app_x_foo"] != "bar" {
		t.Fatalf("%+v", all)
	}

	// export and import into a fresh log: nothing is lost, including fields groundwave has no column for
	var b bytes.Buffer
	var recs []Fields
	for _, q := range all {
		recs = append(recs, q.ToFields())
	}
	if err := WriteADIF(&b, "groundwave", "t", recs); err != nil {
		t.Fatal(err)
	}
	s2 := openTest(t)
	if res, err := s2.ImportADIF(b.Bytes()); err != nil || res.Added != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	back, _ := s2.All()
	if back[0].Extra["qsl_sent"] != "Y" || back[0].Extra["app_x_foo"] != "bar" || back[0].FreqMHz != 14.074 {
		t.Errorf("%+v", back[0])
	}
}

func TestDatabaseGridIsNotExported(t *testing.T) {
	q := good()
	q.GridSource = SourceDatabase
	q.Normalize()
	if q.GridSource != SourceDatabase {
		t.Fatal("source lost")
	}
	if _, has := q.ToFields()["gridsquare"]; has {
		t.Error("an approximate database grid was exported as if the other operator had given it")
	}
	q.GridSource = SourceUser
	if q.ToFields()["gridsquare"] != "FN31pr" {
		t.Error("a typed grid was not exported")
	}
}
