# Development

## Build and test

Requires [Go](https://go.dev/dl/) 1.26 or newer (set by the pure-Go SQLite library).

```text
go test ./...
go run ./cmd/groundwave          # opens http://127.0.0.1:47321 in your browser
go build -o groundwave ./cmd/groundwave
```

Options: `--data <folder>` (library location), `--port`, `--browser` (use a browser tab instead of the app window), `--no-browser` (headless: no window at all), `--gps-sim` (adds a simulated GPS receiver to the list). See [ARCHITECTURE.md](ARCHITECTURE.md)

See [ARCHITECTURE.md](ARCHITECTURE.md) for the layout and security rules.

## Building releases

Both builds are produced on any computer that has Go; the output goes in `dist/` (not tracked).

```text
.\build-windows.ps1 -Version 1.0.0         # Windows: dist\groundwave-windows-amd64.exe (own window, no console)
go run ./tools/release -version 1.0.0      # Linux: three .tar.gz files + SHA256SUMS (works from Windows, Linux or macOS)
```


## User guide

The guide for people who just download and use the program is [user-guide/user-guide.md](user-guide/user-guide.md), with a walkthrough of every feature and screenshots. **It also ships inside the program**: `internal/guide` embeds a PDF of it and adds it to the user's Library the first time the program runs (and replaces it after an upgrade, unless the user deleted it; a `guide.json` marker in the data folder remembers). It is our own writing, so it is allowed under the "content we ship" rule.

The screenshots are made by `py tools/docshots/shoot.py`, which runs the app on a throwaway data folder (made-up contacts, documents and callsigns from `tools/demodata`, so no real person appears) and drives headless Chrome. With no GPS in the loop, the built-in simulator (`--gps-sim`) stands in for a receiver and is rewritten to look like COM3, and the internet-time panel is faked, so nothing is sent to a time server. After changing the guide or the screens, run `py tools/docshots/shoot.py` (if screens changed), then `py tools/docshots/buildguide.py` to regenerate `internal/guide/groundwave-user-guide.pdf`, and commit it. Needs Chrome or Chromium and the Python packages `websockets` and `markdown-it-py`.

## Platform notes


## License headers

The project is GPL-3.0-only. Every `.go`, `.js`, `.css` and `.py` file starts with:

```text
SPDX-License-Identifier: GPL-3.0-only
Copyright (c) 2026 KD9PSM
```

(in that file type's comment syntax). A test fails if a Go file lacks it.
