// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

//go:build !windows

package window

import (
	"fmt"
	"os"
)

// Available reports whether a native window can be shown. Not yet on this OS.
func Available() bool { return false }

// Run is not supported on this OS; the caller opens the default browser instead.
func Run(Options) error { return ErrUnavailable }

// ShowError prints a fatal startup error (there is no console-less mode here).
func ShowError(title, msg string) { fmt.Fprintln(os.Stderr, title+": "+msg) }
