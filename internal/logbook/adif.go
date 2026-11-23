// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package logbook is the offline contact log: a local SQLite file plus ADIF 3 import and export.
package logbook

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// A Fields map is one ADIF record: lower-case field name to value.
type Fields map[string]string

// ParseADIF reads ADI text (the .adi / .adif format every logging program writes) and
// returns its records. Header fields are skipped. Lengths in ADI are counted in bytes,
// so a name with an accented letter is read correctly. Anything that is not inside a
// <tag:length>value is ignored, as the standard says.
func ParseADIF(data []byte) ([]Fields, error) {
	var out []Fields
	cur := Fields{}
	i := 0
	for i < len(data) {
		lt := bytes.IndexByte(data[i:], '<')
		if lt < 0 {
			break
		}
		i += lt + 1
		gt := bytes.IndexByte(data[i:], '>')
		if gt < 0 {
			break
		}
		tag := string(data[i : i+gt])
		i += gt + 1
		parts := strings.Split(tag, ":")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		switch name {
		case "eoh":
			cur = Fields{} // everything before is the header
			continue
		case "eor":
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = Fields{}
			continue
		}
		if len(parts) < 2 {
			continue // a tag without a length carries no data
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || n < 0 {
			return out, fmt.Errorf("bad field length in <%s>", tag)
		}
		if i+n > len(data) {
			n = len(data) - i // a truncated last field: keep what is there
		}
		if name != "" {
			cur[name] = string(data[i : i+n])
		}
		i += n
	}
	return out, nil
}

// WriteADIF writes records as ADIF 3 text. Field names are written upper-case, in a stable
// order, and lengths are byte counts.
func WriteADIF(w io.Writer, program, version string, recs []Fields) error {
	var b bytes.Buffer
	b.WriteString("groundwave log export\n")
	put := func(name, val string) {
		fmt.Fprintf(&b, "<%s:%d>%s", strings.ToUpper(name), len(val), val)
	}
	put("adif_ver", "3.1.4")
	put("programid", program)
	put("programversion", version)
	b.WriteString("<EOH>\n\n")
	for _, r := range recs {
		names := make([]string, 0, len(r))
		for k, v := range r {
			if v != "" {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, k := range names {
			// a line break inside a value is legal ADIF (it counts toward the length) but
			// would trip up simple readers, so keep values on one line
			put(k, strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(r[k]))
		}
		b.WriteString("<EOR>\n")
	}
	_, err := w.Write(b.Bytes())
	return err
}
