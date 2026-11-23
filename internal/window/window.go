// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package window shows the app in its own native window.
//
// On Windows it embeds Microsoft's WebView2 (the Edge engine, preinstalled on
// Windows 11 and most up-to-date Windows 10), through a pure-Go library: no
// cgo and nothing extra to install. On other systems it reports that it is
// unavailable and the caller falls back to the default browser.
package window

import "errors"

// ErrUnavailable means a native window cannot be shown on this computer (an
// unsupported OS, or the WebView2 runtime is not installed).
var ErrUnavailable = errors.New("a native window is not available")

// Options describes the window to show.
type Options struct {
	URL      string // page to load
	Title    string // initial window title (the page's own title replaces it)
	DataPath string // folder for the web engine's cache and cookies
	Width    uint   // initial size in pixels (0 = default)
	Height   uint
	Quit     <-chan struct{} // when closed, the window is closed too (nil = never)
}
