// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package web holds the browser front end, embedded into the binary.
package web

import "embed"

// Static is the front end: plain HTML, CSS and JavaScript, no build step.
//
//go:embed static
var Static embed.FS
