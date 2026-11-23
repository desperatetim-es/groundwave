// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

//go:build windows

package window

import (
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// A custom title bar, in the style of Discord and Spotify: the native caption is
// removed and the page's own header (drag area plus minimize, maximize and close
// buttons) takes its place. The window keeps its normal frame styles, so it still
// has a shadow, rounded corners on Windows 11, edge resizing and Aero Snap.
//
// How: the library's window procedure is wrapped by frameProc, which answers
// WM_NCCALCSIZE so the client area starts at the very top of the window. The page
// finds out it is inside the app window from window.gwApp and calls the gw*
// functions bound below.

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	pGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProc   = user32.NewProc("CallWindowProcW")
	pIsZoomed         = user32.NewProc("IsZoomed")
	pShowWindow       = user32.NewProc("ShowWindow")
	pReleaseCapture   = user32.NewProc("ReleaseCapture")
	pSendMessage      = user32.NewProc("SendMessageW")
	pPostMessage      = user32.NewProc("PostMessageW")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	gwlpWndProc = ^uintptr(3) // -4

	wmClose         = 0x0010
	wmNCCalcSize    = 0x0083
	wmNCLButtonDown = 0x00A1
	htCaption       = 2

	swMaximize = 3
	swMinimize = 6
	swRestore  = 9

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	smCYSizeFrame   = 33
	smCXPaddedBound = 92
)

// frameState is the window being customized. A process shows one app window.
var frameState struct {
	orig uintptr
}

type nccalcsizeParams struct {
	rgrc  [3]windows.Rect
	lppos uintptr
}

// frameProc wraps the library's window procedure.
func frameProc(hwnd, msg, wp, lp uintptr) uintptr {
	if msg == wmNCCalcSize && wp != 0 {
		// lp is a pointer handed to us by Windows for the duration of this call.
		p := (*nccalcsizeParams)(*(*unsafe.Pointer)(unsafe.Pointer(&lp)))
		top := p.rgrc[0].Top
		// Let Windows work out the borders (it also handles the maximized case),
		// then take back the caption it removed from the top.
		r, _, _ := pCallWindowProc.Call(frameState.orig, hwnd, msg, wp, lp)
		p.rgrc[0].Top = top
		if zoomed(hwnd) {
			// A maximized window extends past the screen edge by its frame width.
			fy, _, _ := pGetSystemMetrics.Call(smCYSizeFrame)
			pad, _, _ := pGetSystemMetrics.Call(smCXPaddedBound)
			p.rgrc[0].Top += int32(fy + pad)
		}
		return r
	}
	r, _, _ := pCallWindowProc.Call(frameState.orig, hwnd, msg, wp, lp)
	return r
}

func zoomed(hwnd uintptr) bool {
	r, _, _ := pIsZoomed.Call(hwnd)
	return r != 0
}

// useCustomFrame removes the native caption and binds the functions the page's
// title bar uses. Must run on the window's thread after the window exists.
func useCustomFrame(w webview2.WebView) {
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return
	}
	orig, _, _ := pGetWindowLongPtr.Call(hwnd, gwlpWndProc)
	if orig == 0 {
		return
	}
	frameState.orig = orig
	pSetWindowLongPtr.Call(hwnd, gwlpWndProc, windows.NewCallback(frameProc))
	// Make Windows recompute the frame now so the caption disappears.
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)

	w.SetSize(800, 500, webview2.HintMin)
	w.Init("window.gwApp = true;")

	// These run on the window's thread (the library calls bindings from its message loop).
	w.Bind("gwDrag", func() {
		pReleaseCapture.Call()
		pSendMessage.Call(hwnd, wmNCLButtonDown, htCaption, 0)
	})
	w.Bind("gwMinimize", func() { pShowWindow.Call(hwnd, swMinimize) })
	w.Bind("gwToggleMaximize", func() {
		if zoomed(hwnd) {
			pShowWindow.Call(hwnd, swRestore)
		} else {
			pShowWindow.Call(hwnd, swMaximize)
		}
	})
	w.Bind("gwIsMaximized", func() bool { return zoomed(hwnd) })
	w.Bind("gwClose", func() { pPostMessage.Call(hwnd, wmClose, 0, 0) })
}
