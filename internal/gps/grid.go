// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import "github.com/kd9psm/groundwave/internal/geo"

// Grid returns the Maidenhead locator for a position; see geo.Grid.
func Grid(lat, lon float64, pairs int) string { return geo.Grid(lat, lon, pairs) }
