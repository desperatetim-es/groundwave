// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package main

import (
	"strings"
	"testing"
)

func TestSerialAdvice(t *testing.T) {
	dial := serialDev{Path: "/dev/ttyUSB0", Gid: 20, Group: "dialout"}
	acm := serialDev{Path: "/dev/ttyACM0", Gid: 20, Group: "dialout"}
	uucp := serialDev{Path: "/dev/ttyACM1", Gid: 14, Group: "uucp"}

	if got := serialAdvice(nil, map[uint32]bool{}, 1000); got != "" {
		t.Errorf("no devices: %q", got)
	}
	if got := serialAdvice([]serialDev{dial}, map[uint32]bool{20: true}, 1000); got != "" {
		t.Errorf("member of the group: %q", got)
	}
	if got := serialAdvice([]serialDev{dial}, map[uint32]bool{}, 0); got != "" {
		t.Errorf("root: %q", got)
	}
	got := serialAdvice([]serialDev{dial, acm}, map[uint32]bool{1000: true}, 1000)
	if !strings.Contains(got, "/dev/ttyUSB0") || !strings.Contains(got, "/dev/ttyACM0") ||
		!strings.Contains(got, `"dialout"`) || !strings.Contains(got, "usermod -aG dialout") {
		t.Errorf("not in group: %q", got)
	}
	// Only the devices that are actually blocked are listed.
	got = serialAdvice([]serialDev{dial, uucp}, map[uint32]bool{20: true}, 1000)
	if strings.Contains(got, "ttyUSB0") || !strings.Contains(got, "ttyACM1") || !strings.Contains(got, `"uucp"`) {
		t.Errorf("partial: %q", got)
	}
	// An unknown group name falls back to the number.
	got = serialAdvice([]serialDev{{Path: "/dev/ttyUSB1", Gid: 77}}, map[uint32]bool{}, 1000)
	if !strings.Contains(got, "gid 77") {
		t.Errorf("unnamed group: %q", got)
	}
}
