// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package main

import (
	"fmt"
	"sort"
	"strings"
)

// serialDev is a serial device file and the group that owns it.
type serialDev struct {
	Path  string
	Gid   uint32
	Group string // name of Gid, if known
}

// serialAdvice returns a warning when this user could not open any of the
// devices, or "" when all is well. A user can open a device if they are root or
// belong to its owning group. (Devices whose group is open to everyone are rare
// enough to ignore: the warning there is simply extra caution.)
//
// It only looks at ownership and group membership; it never opens a device,
// because opening a serial port can reset some boards (many ESP32 boards restart).
func serialAdvice(devs []serialDev, myGids map[uint32]bool, euid int) string {
	if euid == 0 {
		return ""
	}
	var blocked []string
	groups := map[string]bool{}
	for _, d := range devs {
		if myGids[d.Gid] {
			continue
		}
		blocked = append(blocked, d.Path)
		g := d.Group
		if g == "" {
			g = fmt.Sprintf("gid %d", d.Gid)
		}
		groups[g] = true
	}
	if len(blocked) == 0 {
		return ""
	}
	var names []string
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	g := names[0]
	return fmt.Sprintf("Serial device(s) found (%s) but you are not in the %q group, so a GPS receiver there cannot be opened.\n"+
		"  Fix, then log out and back in:  sudo usermod -aG %s $USER",
		strings.Join(blocked, ", "), strings.Join(names, `" or "`), g)
}
