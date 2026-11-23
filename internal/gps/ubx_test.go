// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"
)

// Checksums of the two polls, as published in the u-blox protocol documentation.
func TestUbxPollFramesMatchKnownBytes(t *testing.T) {
	if got, want := ubxPoll(ubxClassNAV, ubxIDSVInfo), []byte{0xB5, 0x62, 0x01, 0x30, 0x00, 0x00, 0x31, 0x94}; !bytes.Equal(got, want) {
		t.Errorf("NAV-SVINFO poll = % X, want % X", got, want)
	}
	if got, want := ubxPoll(ubxClassMON, ubxIDMonHW), []byte{0xB5, 0x62, 0x0A, 0x09, 0x00, 0x00, 0x13, 0x43}; !bytes.Equal(got, want) {
		t.Errorf("MON-HW poll = % X, want % X", got, want)
	}
}

// ---- payloads modelled on a real u-blox 7 USB receiver with no sky view ----------

func realSVInfo() []byte {
	p := make([]byte, 8)
	p[4] = 4 // four channels, all "searching" with no signal (quality 1, C/N0 0, elevation -91)
	for _, c := range []struct{ chn, svid byte }{{4, 2}, {0, 127}, {1, 129}, {2, 133}} {
		blk := make([]byte, 12)
		blk[0], blk[1], blk[3], blk[4], blk[5] = c.chn, c.svid, 1, 0, 0xA5 // 0xA5 = int8(-91)
		p = append(p, blk...)
	}
	return p
}

func realMonHW() []byte {
	p := make([]byte, 68)
	binary.LittleEndian.PutUint16(p[16:], 114)  // noise per ms
	binary.LittleEndian.PutUint16(p[18:], 1387) // AGC
	p[20], p[21], p[53] = 2, 1, 247             // antenna OK, powered, jamming indicator
	return p
}

func realMonVer() []byte {
	p := make([]byte, 40, 100)
	copy(p[0:], "1.00 (59842)")
	copy(p[30:], "00070000")
	for _, ext := range []string{"PROTVER 14.00", "GPS;SBAS;GLO;QZSS"} {
		e := make([]byte, 30)
		copy(e, ext)
		p = append(p, e...)
	}
	return p
}

func TestParseSVInfo(t *testing.T) {
	sats, ok := parseSVInfo(realSVInfo())
	if !ok || len(sats) != 4 {
		t.Fatalf("got %v %v", sats, ok)
	}
	want := []struct {
		prn int
		sys string
	}{{2, "GPS"}, {127, "SBAS"}, {129, "SBAS"}, {133, "SBAS"}}
	for i, w := range want {
		s := sats[i]
		if s.PRN != w.prn || s.Sys != w.sys || s.SNR != nil || s.Elev != nil || s.Azim != nil || s.Used {
			t.Errorf("sat %d = %+v", i, s)
		}
	}
	// a tracked, used satellite
	p := make([]byte, 8, 20)
	p[4] = 1
	blk := make([]byte, 12)
	blk[1], blk[2], blk[3], blk[4], blk[5] = 13, 0x01|0x04, 7, 41, 55
	binary.LittleEndian.PutUint16(blk[6:], 200)
	sats, _ = parseSVInfo(append(p, blk...))
	s := sats[0]
	if s.SNR == nil || *s.SNR != 41 || s.Elev == nil || *s.Elev != 55 || s.Azim == nil || *s.Azim != 200 || !s.Used {
		t.Errorf("tracked sat = %+v", s)
	}
	// truncated payloads are rejected, not misread
	if _, ok := parseSVInfo(realSVInfo()[:20]); ok {
		t.Error("truncated payload accepted")
	}
	if _, ok := parseSVInfo(nil); ok {
		t.Error("nil payload accepted")
	}
}

func TestParseMonHWAndVer(t *testing.T) {
	h, ok := parseMonHW(realMonHW())
	if !ok || h.Antenna != "OK" || h.AntennaPower != "on" || h.Noise != 114 || h.AGC != 1387 || h.Jamming != 247 {
		t.Errorf("hw = %+v %v", h, ok)
	}
	bad := realMonHW()
	bad[20] = 200 // unknown code must not panic
	if h, _ := parseMonHW(bad); h.Antenna != "unknown" {
		t.Errorf("unknown antenna code = %q", h.Antenna)
	}
	if _, ok := parseMonHW(make([]byte, 10)); ok {
		t.Error("short MON-HW accepted")
	}
	v, ok := parseMonVer(realMonVer())
	if !ok || v.Firmware != "1.00 (59842)" || v.Hardware != "00070000" || v.Protocol != "14.00" || v.Systems != "GPS;SBAS;GLO;QZSS" {
		t.Errorf("ver = %+v %v", v, ok)
	}
}

func TestIsUblox(t *testing.T) {
	for vid, want := range map[string]bool{"1546": true, "1546 ": false, "": false, "10C4": false, "0403": false} {
		if isUblox(vid) != want {
			t.Errorf("isUblox(%q) = %v", vid, !want)
		}
	}
}

// ---- frame reader -----------------------------------------------------------------

func collect(t *testing.T, data []byte, chunk int) []frame {
	t.Helper()
	// deliver the data in small chunks, like a slow serial port
	r := &chunked{data: data, size: chunk}
	fr := newFrameReader(r, time.Now)
	var out []frame
	for {
		f, err := fr.next(context.Background(), time.Now().Add(200*time.Millisecond))
		if err != nil {
			return out
		}
		out = append(out, f)
	}
}

type chunked struct {
	data []byte
	size int
}

func (c *chunked) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		time.Sleep(time.Millisecond)
		return 0, nil // behaves like a read timeout; the deadline ends the test
	}
	n := min(c.size, len(c.data), len(p))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func TestFrameReaderMixedStream(t *testing.T) {
	// A UBX payload containing 0x0A ('\n') and '$' must not be split like text.
	tricky := ubxFrame(ubxClassMON, ubxIDMonHW, append([]byte{0x0A, '$', 0x0A, 0xB5, 0x62}, make([]byte, 20)...))
	bad := append([]byte(nil), ubxFrame(ubxClassNAV, ubxIDSVInfo, realSVInfo())...)
	bad[len(bad)-1] ^= 0xFF // corrupt the checksum

	var stream []byte
	stream = append(stream, "junk\x00\xff\r\n"...)
	stream = append(stream, nmea(rmcFix)...)
	stream = append(stream, tricky...)
	stream = append(stream, bad...)
	stream = append(stream, nmea(ggaFix)...)
	stream = append(stream, ubxFrame(ubxClassNAV, ubxIDSVInfo, realSVInfo())...)

	for _, chunk := range []int{1, 3, 7, 512} {
		frames := collect(t, stream, chunk)
		var nmeas int
		var ubxs []frame
		for _, f := range frames {
			if f.isUBX {
				ubxs = append(ubxs, f)
			} else {
				nmeas++
			}
		}
		if nmeas != 2 || len(ubxs) != 2 {
			t.Errorf("chunk %d: %d NMEA + %d UBX frames from %+v", chunk, nmeas, len(ubxs), frames)
			continue
		}
		// the tricky frame came through intact and in order, then the good SVINFO
		if ubxs[0].class != ubxClassMON || len(ubxs[0].payload) != 25 || ubxs[0].payload[0] != 0x0A {
			t.Errorf("chunk %d: embedded newline broke the frame: %+v", chunk, ubxs[0])
		}
		if ubxs[1].class != ubxClassNAV || ubxs[1].id != ubxIDSVInfo || !bytes.Equal(ubxs[1].payload, realSVInfo()) {
			t.Errorf("chunk %d: last UBX frame wrong: %+v", chunk, ubxs[1])
		}
	}
}

func TestFrameReaderSurvivesGarbage(t *testing.T) {
	junk := bytes.Repeat([]byte{0xB5, 0x62, 0xFF, 0xFF, '$'}, 500) // hostile: fake headers and a huge claimed length
	frames := collect(t, append(junk, []byte(nmea(rmcFix))...), 64)
	var found bool
	for _, f := range frames {
		if !f.isUBX && f.nmea != "" {
			if _, err := ParseSentence(f.nmea); err == nil {
				found = true
			}
		}
	}
	if !found {
		t.Error("a valid sentence after garbage was lost")
	}
}
