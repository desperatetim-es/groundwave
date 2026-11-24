// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Command demodata builds a small, entirely made-up callsign database for taking
// documentation screenshots, so no real person's licence appears in them.
//
//	go run ./tools/demodata -data <folder>
//
// It writes <folder>/callsigns/callsigns.db. The call signs are deliberately in a
// shape the FCC never issues (a one-letter prefix followed by four letters), so none
// can belong to a real operator. W1AW is the ARRL's public club station.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kd9psm/groundwave/internal/callsign"
)

type person struct {
	call, class, first, last, city, state, zip string
}

var people = []person{
	{"K9DEMO", "G", "Pat", "Example", "Cary", "IL", "60013"},
	{"W9SAMPL", "E", "Sam", "Sample", "Chicago", "IL", "60601"},
	{"K0DEMO", "T", "Alex", "Demo", "Lawrence", "KS", "66044"},
	{"W7DEMO", "G", "Jordan", "Sample", "Portland", "OR", "97201"},
	{"N5DEMO", "E", "Casey", "Example", "Austin", "TX", "78701"},
	{"K2DEMO", "T", "Robin", "Demo", "New York", "NY", "10001"},
	{"W0DEMO", "G", "Morgan", "Sample", "Denver", "CO", "80202"},
}

func row(n int, fields map[int]string, size int) string {
	f := make([]string, size)
	for i, v := range fields {
		f[i] = v
	}
	return strings.Join(f, "|")
}

func main() {
	data := flag.String("data", "", "groundwave data folder to write into")
	flag.Parse()
	if *data == "" {
		log.Fatal("give -data <folder>")
	}
	var hds, ens, ams []string
	add := func(usi int, call, status, class, name, first, last, city, state, zip, trusteeName, trusteeCall string) {
		hds = append(hds, row(usi, map[int]string{0: "HD", 1: strconv.Itoa(usi), 4: call, 5: status, 6: "HA", 7: "01/02/2022", 8: "01/02/2032"}, 59))
		etype := "L"
		ens = append(ens, row(usi, map[int]string{0: "EN", 1: strconv.Itoa(usi), 4: call, 5: etype, 7: name, 8: first, 10: last, 16: city, 17: state, 18: zip}, 30))
		ams = append(ams, row(usi, map[int]string{0: "AM", 1: strconv.Itoa(usi), 4: call, 5: class, 8: trusteeCall, 17: trusteeName}, 18))
	}
	for i, p := range people {
		add(i+1, p.call, "A", p.class, p.last+", "+p.first, p.first, p.last, p.city, p.state, p.zip, "", "")
	}
	add(100, "W1AW", "A", "", "ARRL HQ OPERATORS CLUB", "", "", "NEWINGTON", "CT", "06111", "EXAMPLE, TRUSTEE", "K1DEMO")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, lines := range map[string][]string{"HD.dat": hds, "EN.dat": ens, "AM.dat": ams} {
		w, _ := zw.Create(name)
		w.Write([]byte(strings.Join(lines, "\r\n") + "\r\n"))
	}
	zw.Close()

	dir := filepath.Join(*data, "callsigns")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	zp := filepath.Join(dir, "demo.zip")
	if err := os.WriteFile(zp, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	defer os.Remove(zp)
	st, err := callsign.Import(context.Background(), callsign.ImportOptions{
		ZipPath: zp, DBPath: filepath.Join(dir, "callsigns.db"),
		SourceModified: time.Now().UTC().Format(time.RFC1123), Now: time.Now,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("demo callsign database:", st.Records, "licenses")
}
