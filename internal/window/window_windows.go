// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

//go:build windows

package window

import (
	"reflect"
	"runtime"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The WebView2 Runtime registers its version ("pv") under this product id.
const runtimeKey = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// Available reports whether the WebView2 Runtime is installed. Creating a
// window without it would leave a blank window behind, so check first.
func Available() bool {
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`},
		{registry.LOCAL_MACHINE, runtimeKey},
		{registry.CURRENT_USER, runtimeKey},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}

// Run shows the window and blocks until it is closed (by the user, or because
// o.Quit was closed). It must be called from the goroutine that will own the
// window; it locks that goroutine to its OS thread.
func Run(o Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  o.DataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  o.Title,
			Width:  o.Width,
			Height: o.Height,
			Center: true,
		},
	})
	if w == nil {
		return ErrUnavailable
	}

	hideStatusBar(w)
	useCustomFrame(w)

	done := make(chan struct{})
	defer close(done)
	if o.Quit != nil {
		go func() {
			select {
			case <-o.Quit:
				w.Destroy() // posts WM_CLOSE; safe from any thread
			case <-done:
			}
		}()
	}

	w.SetTitle(o.Title)
	w.Navigate(o.URL)
	w.Run()
	return nil
}

// hideStatusBar turns off WebView2's status bar, the little box that shows a
// link's address when the pointer is over it (it appears over our nav buttons,
// which are links). The library does not expose this setting and keeps the engine
// in an unexported field, so reach it by reflection. Every step is checked: if a
// library update changes the layout, the status bar simply comes back.
func hideStatusBar(w webview2.WebView) {
	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	f := v.Elem().FieldByName("browser")
	if !f.IsValid() || !f.CanAddr() {
		return
	}
	c, ok := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(*edge.Chromium)
	if !ok || c == nil {
		return
	}
	if s, err := c.GetSettings(); err == nil && s != nil {
		_ = s.PutIsStatusBarEnabled(false)
	}
}

// ShowError shows a fatal startup error in a message box. The Windows build has
// no console window, so a printed message would never be seen.
func ShowError(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONERROR)
}
