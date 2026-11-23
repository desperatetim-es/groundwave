// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

//go:build !linux

package main

// checkSerialAccess does nothing off Linux: Windows has no serial-port groups.
func checkSerialAccess() {}
