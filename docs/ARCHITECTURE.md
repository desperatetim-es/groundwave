# Architecture

Cross-cutting design: the shape of the program, principles, data folder and security model.

## Shape

One self-contained Go program. It starts a web server on `127.0.0.1`, shows the UI in a native WebView2 window on Windows (default browser elsewhere), and serves a small web UI embedded in the binary. Nothing else is installed.

```
cmd/groundwave/        main(): flags, data folder, start server, show window or browser
internal/server/       HTTP server, routes, security middleware (localhost-only, Host/Origin checks)
internal/library/      Document library: storage, metadata index, add-by-upload, add-by-URL
internal/calc/         Antenna and RF math (pure functions, unit-tested)
internal/browser/      Open the default browser (Windows, Linux)
legal.go               Embeds LICENSE and THIRD_PARTY_LICENSES.txt (served at /api/legal/<name>)
tools/licenses/        Generates THIRD_PARTY_LICENSES.txt from the modules linked per target
tools/release/         Builds the Linux release tarballs (static, cross-compiled; any OS)
tools/demodata/        Builds a small made-up callsign database for documentation screenshots
tools/docshots/        shoot.py takes the user-guide screenshots; buildguide.py renders the guide PDF (docs/user-guide -> internal/guide)
internal/window/       Native window: WebView2 on Windows (pure Go, no cgo; frame_windows.go draws no native caption and binds gwDrag/gwMinimize/gwToggleMaximize/gwClose for the page header), stub elsewhere
internal/guide/        The user guide as an embedded PDF; Seed() puts it in the Library on first run (see DEVELOPMENT.md, User guide)
web/static/            Embedded front end: index.html, style.css, js/ (ES modules, no build step)
docs/                  Design notes
```
Front end modules (`web/static/js/`): `app.js` (hash router, header clock, Quit), `home.js` (tiles), `library.js`, `antenna.js`, `dom.js` (safe DOM helper and `api()`). Each view exports `mount(root, args)` and may return a cleanup function. Routes: `#/`, `#/library`, `#/antenna/<calculator>`.

`internal/gps/` is the GPS clock, `internal/ntp/` the internet-time checker, `internal/callsign/` the callsign database and `internal/logbook/` the contact log. Their design is documented with each feature (see below).

## Principles

- **No build step for the web UI.** Plain files, embedded with `go:embed`. Contributors and the build need only the Go toolchain.
- **Standard library first.** Add a dependency only when it clearly earns its place, it is GPL-compatible, and a permissive license is preferred.
- **Everything the user owns is a plain folder.** Backup is copying a folder.
- **Cross-platform from the start.** `path/filepath`, no OS-specific code outside `internal/browser`, `internal/window` and (later) serial access.
- **Untrusted input.** Uploaded and downloaded files are untrusted; never parse them in the server beyond type sniffing, size, and hashing.

## Data folder

Default: `~/Documents/Groundwave` if `~/Documents` exists, else `~/Groundwave`. Override with `--data <dir>` or `GROUNDWAVE_HOME`.

```
<data>/
  library/            the user's files, stored as <id>-<safe-name>.<ext>
  library.json        metadata index (title, tags, source URL, added date, sha256, size)
```
`library.json` is human-readable and written atomically (temp file then rename). If it is lost, the library folder is still intact. SQLite is deliberately not used yet; revisit when full-text search needs it.

## Security model

- Listen on `127.0.0.1` only, on a free port chosen at startup.
- Reject any request whose `Host` is not the exact `127.0.0.1:<port>` / `localhost:<port>` (DNS-rebinding defense).
- Reject state-changing requests (anything but GET/HEAD) whose `Origin` is missing or not our own origin (cross-site request defense).
- Set `Content-Security-Policy` (self only), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`.
- Serve library files with a type chosen by our own content sniffing at upload time, never one supplied by the uploader or remote server, plus `nosniff` and `Content-Disposition: inline` with a sanitized name. (No `Content-Security-Policy: sandbox` on files: Chromium refuses to render PDFs under it.)
- Stored files are named `<random id>.<ext>`; user- or server-supplied names are never used as paths.
