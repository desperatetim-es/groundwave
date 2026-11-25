# Changelog

All notable changes to groundwλve are recorded here.

The format follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0-beta.1] - 2026-11-25

**A pre-release for testing.** It is not offered by **Check for updates** (which only looks at full releases) and will not replace the full 1.1.0. Please report problems at https://github.com/kd9psm/groundwave/issues. Things worth trying: download the Canada callsign list and search it; open **Settings** (the gear) and switch the options on and off; press **Check for updates** in the **?** menu; and, if you use the Windows app, check that the GitHub link opens your browser. Your data folder is compatible with 1.0.0.

### Added

- **Settings:** a gear button next to the **?** opens a Settings page (saved as you change it, kept in `settings.json`). All of its internet options are off by default: **check for updates when groundwλve starts** (a dot on the **?** button if a newer version exists; nothing is installed), **hide pages** you do not use from the top menu and home page (links to them still work), **check internet time automatically every 10 minutes** (moved here from the Time & Location page, which now shows the setting and where to change it), and **callsign databases at launch**: do nothing, check whether a publisher has a newer list, or update out-of-date lists automatically. Only databases you have already downloaded are ever checked or updated.
- **Callsigns:** Canada. Download ISED Canada's amateur call sign list (about 3 MB) alongside, or instead of, the US database and search both at once: call signs (`ve3`, `va7xyz`), names, `Ottawa, ON`, provinces and postal areas (`postal:K1A`). Results show the holder's qualifications, city, province and the postal district (the first three characters of the postal code), with an approximate distance and bearing from your grid square. Clubs are listed under their own name. Canada's list has no expiry dates, so entries show **Listed**. Street addresses are never read. Each database has its own **Update Database** button and credit line. The program still ships no licence data: each list is downloaded from its authority when you press the button.
- **App:** the **?** menu now has a link to the project on GitHub and a **Check for updates** button. The button sends one anonymous request to GitHub, only when you press it, and says whether a newer release exists, with a link to its page. Nothing is downloaded or installed automatically. In the Windows app the GitHub link opens in your browser.

## [1.0.0] - 2026-11-23

First release.

### Added

- **App:** one program with no installer and no setup. It runs a server on this computer only (127.0.0.1), has no telemetry, and uses the internet only when you press a button that says so. Windows opens it in its own window, with the menu bar as the title bar. Linux (x86_64, 64-bit and 32-bit Raspberry Pi) opens it in your default browser.
- **Library:** keep your own manuals and notes (PDF, plain text, Markdown, PNG, JPEG). Add by upload or from a web address, then search, rename, tag, open and delete. Searches cover titles and tags.
- **License information:** a **?** menu next to the clocks shows the version and opens the GPL license and the third-party notices inside the program. The notices are also included in each Linux download.
- **User guide:** a walkthrough of every feature with screenshots, built into the program and added to the Library the first time it runs. After an upgrade the Library copy is replaced, unless you deleted it. The guide also lives in the source as `docs/user-guide/`.
- **Tools:** an Antenna Calculator with dipole, vertical, J-pole, wavelength, SWR / match, feedline loss, and power / EIRP / ERP tabs.
- **Callsigns:** an offline search of the FCC's US amateur license file. Search by call sign, name, or place (City, ST), with class, standing, an approximate grid square, and distance and bearing from you. The database downloads once, when you press Update Database.
- **Logbook:** a local contact log. It fills in details from the callsign database, shows live distance, and supports mobile, POTA and SOTA stations. It imports and exports ADIF 3.1.4. Nothing leaves your computer unless you export it.
- **Time & Location:** compares GPS time, internet time (NTP) and your computer's clock, and says whether the clock is good enough for FT8 and FT4. Shows position, grid square and fix details from a USB GPS receiver, with satellites, receiver status and port settings in sections that fold away. The receiver's leap-second data is checked and GPS time is corrected while it is unverified. Internet time is off until you ask for it. The page only displays times and never changes your clock.
- **Header clocks:** GPS and system time, always visible.
- **GPS receivers:** any USB GPS that sends NMEA text. It reconnects to your last choice on start-up. Serial ports are opened only when you press a button (Connect or Find GPS). A `--gps-sim` option adds a simulated receiver for trying the app with no hardware.
- **Linux:** a warning at start-up when a USB serial device is present but your user is not in the group that can open it.
- **Releases:** a Windows executable, and Linux tarballs with SHA256 checksums.
- **Data folder:** everything is stored in `Documents/Groundwave` (or `~/Groundwave`), chosen with `--data` or `GROUNDWAVE_HOME`.

[Unreleased]: https://github.com/kd9psm/groundwave/compare/v1.1.0-beta.1...HEAD
[1.1.0-beta.1]: https://github.com/kd9psm/groundwave/releases/tag/v1.1.0-beta.1
[1.0.0]: https://github.com/kd9psm/groundwave/releases/tag/v1.0.0
