// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Command groundwave runs the local server and shows the app in its own window
// (Windows, using WebView2) or, where that is unavailable, in the default browser.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/kd9psm/groundwave/internal/browser"
	"github.com/kd9psm/groundwave/internal/callsign"
	"github.com/kd9psm/groundwave/internal/gps"
	"github.com/kd9psm/groundwave/internal/guide"
	"github.com/kd9psm/groundwave/internal/launch"
	"github.com/kd9psm/groundwave/internal/library"
	"github.com/kd9psm/groundwave/internal/logbook"
	"github.com/kd9psm/groundwave/internal/ntp"
	"github.com/kd9psm/groundwave/internal/server"
	"github.com/kd9psm/groundwave/internal/settings"
	"github.com/kd9psm/groundwave/internal/updatecheck"
	"github.com/kd9psm/groundwave/internal/window"
)

var version = "dev"

const defaultPort = 47321

func main() {
	data := flag.String("data", "", "data folder (default: ~/Documents/Groundwave, or set GROUNDWAVE_HOME)")
	port := flag.Int("port", defaultPort, "port on 127.0.0.1 (0 = pick any free port)")
	noBrowser := flag.Bool("no-browser", false, "do not open any window or browser (headless)")
	inBrowser := flag.Bool("browser", false, "use the default browser tab instead of the app window")
	gpsSim := flag.Bool("gps-sim", false, "add a built-in simulated GPS receiver to the list (for trying the app without hardware)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("groundwave", version)
		return
	}

	dir, err := dataDir(*data)
	if err != nil {
		fatal("", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil && *port == defaultPort {
		// The usual port is taken. If it's another groundwave, just show it.
		if running(defaultPort) {
			u := fmt.Sprintf("http://127.0.0.1:%d", defaultPort)
			fmt.Println("groundwave is already running at", u)
			if !*noBrowser {
				// Show it in a window (closing that window leaves the running copy alone).
				if !*inBrowser && window.Available() && window.Run(windowOptions(u, dir, nil)) == nil {
					return
				}
				browser.Open(u)
			}
			return
		}
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		fatal("cannot start the server: ", err)
	}

	store, err := library.Open(dir)
	if err != nil {
		fatal("cannot open the library: ", err)
	}

	// The user guide travels inside the program: put it on the Library shelf the first time (and after an
	// upgrade, unless the user deleted it). A failure here is not worth stopping for.
	if err := guide.Seed(store, dir); err != nil {
		log.Print("could not add the user guide to the library: ", err)
	}

	// Reconnect to the receiver chosen last time. This only ever opens that one
	// port; other ports are touched only when the user clicks "Find GPS".
	gpsMgr := gps.NewManager(gps.DefaultOptions(filepath.Join(dir, "gps.json"), *gpsSim))
	gpsMgr.Resume()
	defer gpsMgr.Stop(false)

	// The offline callsign database lives in the data folder. It is only downloaded when the user
	// presses Update Database, and searching it never touches the network.
	callsigns, err := callsign.NewDirectory(callsign.DirOptions{Dir: filepath.Join(dir, "callsigns")})
	if err != nil {
		fatal("cannot open the callsign database folder: ", err)
	}
	defer callsigns.Close()

	// The logbook is a local SQLite file; nothing in it leaves this computer unless the user exports it.
	qsoLog, err := logbook.Open(filepath.Join(dir, "logbook", "logbook.db"))
	if err != nil {
		fatal("cannot open the logbook: ", err)
	}
	defer qsoLog.Close()

	// Internet time (NTP) stays OFF until the user switches it on: it sends a small
	// request to public time servers, which can see this computer's address.
	timeCheck := ntp.NewChecker(ntp.Options{ConfigPath: filepath.Join(dir, "time.json")})
	timeCheck.Resume()
	defer timeCheck.Close()

	// The user's preferences. Everything that contacts the internet by itself is off until switched on.
	prefs := settings.Open(filepath.Join(dir, "settings.json"))
	updates := updatecheck.New(updatecheck.Options{Version: version})

	quit := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(quit) }) }
	h, err := server.New(server.Config{
		Store:     store,
		GPS:       gpsMgr,
		Time:      timeCheck,
		Callsigns: callsigns,
		Logbook:   qsoLog,
		Settings:  prefs,
		Updates:   updates,
		OpenURL:   browser.Open,
		Fetcher:   &library.Fetcher{},
		Addr:      ln.Addr().String(),
		Version:   version,
		Quit:      stop,
	})
	if err != nil {
		fatal("", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("the server stopped: ", err)
		}
	}()

	// What the user asked for at launch (check for a newer release; check or update the callsign databases
	// already downloaded). With the default settings this does nothing. It waits a few seconds so that
	// starting up is never slowed down by it.
	launchCtx, cancelLaunch := context.WithCancel(context.Background())
	defer cancelLaunch()
	go func() {
		select {
		case <-time.After(3 * time.Second):
		case <-quit:
			return
		}
		go func() { <-quit; cancelLaunch() }()
		launch.Run(launchCtx, launch.Deps{Settings: prefs.Get, Updates: updates, Callsigns: callsigns})
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; stop() }()

	u := "http://" + ln.Addr().String()
	fmt.Println("groundwave", version, "- library folder:", dir)
	checkSerialAccess() // Linux: warn if a USB serial device is present but not openable
	switch {
	case *noBrowser:
		fmt.Println("Serving", u, "(no window). Press Ctrl+C to stop.")
		<-quit
	case !*inBrowser && window.Available():
		// The app's own window. Closing it quits the app; so does Quit inside the app.
		if err := window.Run(windowOptions(u, dir, quit)); err == nil {
			stop()
			break
		} else {
			fmt.Println("(could not open the app window:", err, ")")
		}
		fallthrough
	default:
		fmt.Println("Open", u, "in your browser. Press Ctrl+C or use Quit in the app to stop.")
		if err := browser.Open(u); err != nil {
			fmt.Println("(could not open the browser automatically:", err, ")")
		}
		<-quit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

// windowOptions describes the app window. The web engine's cache goes in the
// user's cache folder (not the data folder, which people back up and sync).
func windowOptions(url, dataDir string, quit <-chan struct{}) window.Options {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = dataDir
	}
	return window.Options{
		URL:      url,
		Title:    "groundwave",
		DataPath: filepath.Join(cache, "groundwave", "webview"),
		Width:    1200,
		Height:   800,
		Quit:     quit,
	}
}

// fatal reports a startup error where the user will see it (a message box in the
// Windows build, which has no console) and exits.
func fatal(what string, err error) {
	msg := what + err.Error()
	log.Print(msg)
	window.ShowError("groundwave", msg)
	os.Exit(1)
}

// running reports whether a groundwave server answers on the given port.
func running(port int) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		App string `json:"app"`
	}
	return json.NewDecoder(resp.Body).Decode(&v) == nil && v.App == "groundwave"
}

func dataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if env := os.Getenv("GROUNDWAVE_HOME"); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(filepath.Join(home, "Documents")); err == nil && st.IsDir() {
		return filepath.Join(home, "Documents", "Groundwave"), nil
	}
	return filepath.Join(home, "Groundwave"), nil
}
