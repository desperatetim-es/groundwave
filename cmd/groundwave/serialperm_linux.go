// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

//go:build linux

package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// checkSerialAccess prints a warning if USB serial devices are present that this
// user is not allowed to open (usually: not in the dialout/uucp group).
func checkSerialAccess() {
	var paths []string
	for _, pat := range []string{"/dev/ttyUSB*", "/dev/ttyACM*"} {
		m, _ := filepath.Glob(pat)
		paths = append(paths, m...)
	}
	if len(paths) == 0 {
		return
	}
	var devs []serialDev
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		d := serialDev{Path: p, Gid: st.Gid}
		if g, err := user.LookupGroupId(strconv.Itoa(int(st.Gid))); err == nil {
			d.Group = g.Name
		}
		devs = append(devs, d)
	}
	mine := map[uint32]bool{uint32(os.Getegid()): true}
	if gs, err := os.Getgroups(); err == nil {
		for _, g := range gs {
			mine[uint32(g)] = true
		}
	}
	if msg := serialAdvice(devs, mine, os.Geteuid()); msg != "" {
		fmt.Println("Note:", msg)
	}
}
