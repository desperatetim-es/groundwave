// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Time & Location page: GPS, internet (NTP) and system time compared, plus GPS receiver setup.
import { h, api } from "./dom.js";
import { subscribe, refresh, gpsNow, dateTime } from "./gpsstate.js";
import * as net from "./ntpstate.js";

const LIMIT_MS = 500; // what FT8 and FT4 comfortably tolerate

// "+2.03 s" / "-3.51 s"; near zero reads as "about 0".
function signed(ms) {
  if (Math.abs(ms) < 50) return "about 0";
  return (ms > 0 ? "+" : "-") + (Math.abs(ms) / 1000).toFixed(2) + " s";
}
const secs = (ms) => (Math.abs(ms) / 1000).toFixed(2) + " s";

const BAUDS = [["0", "Detect automatically"], ["9600", "9600"], ["4800", "4800"], ["38400", "38400"], ["115200", "115200"]];
const QUALITY = { 1: "GPS", 2: "DGPS (corrected)", 4: "RTK fixed", 5: "RTK float" };

const STATUS_TEXT = {
  stopped: ["No receiver chosen", "Plug in a USB GPS receiver and choose it under Receiver Settings, or use Find GPS there."],
  connecting: ["Connecting...", "Opening the receiver's port."],
  error: ["Receiver problem", ""],
  no_data: ["Connected, but no data", ""],
  acquiring: ["Searching for satellites", ""],
  fix: ["GPS fix", "Receiving valid GPS time and position."],
  unavailable: ["GPS service unavailable", "The groundwλve server did not answer."],
};

const coord = (v, pos, neg) => `${Math.abs(v).toFixed(5)}° ${v >= 0 ? pos : neg}`;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

// Whether the receiver's UTC (leap-second) data is trustworthy, in plain words.
function utcText(s) {
  if (s.status !== "fix") return "";
  if (s.utc_status === "verified") {
    return "GPS time is verified: the receiver has confirmed its UTC (leap-second) data.";
  }
  if (s.utc_status === "unverified") {
    if (s.correction_ms) {
      return `GPS time is not yet verified. The receiver is still using a built-in leap-second count of ${s.leap_used} s instead of the current ${s.published_leap} s, so on its own it would read ${secs(s.correction_ms)} fast. The times shown here are corrected by ${signed(s.correction_ms)}. The correction switches off by itself once the receiver has received the real value from the satellites, which can take a while. As the receiver itself reports it, GPS is ${signed(s.raw_offset_ms)} against your system clock.`;
    }
    return "GPS time is not yet verified, but the receiver's leap-second count already matches the current value.";
  }
  return "This receiver cannot say whether its UTC (leap-second) data is verified. Compare with internet time to confirm.";
}

// Plain-language reading of the satellite data while there is no fix.
function diagnose(s) {
  const sats = s.satellites || [];
  if (!sats.length || s.status === "fix") return "";
  const tracked = sats.filter((x) => x.snr != null).length;
  if (tracked === 0) {
    return "The receiver is searching, but it hears no signal from any satellite. Make sure the antenna faces open sky, away from walls, coated glass, and computers.";
  }
  if (tracked < 4) {
    return `The receiver hears ${plural(tracked, "satellite")}. It needs at least four for a position fix, so try a spot with a clearer view of the sky.`;
  }
  return `The receiver hears ${plural(tracked, "satellite")}. A fix should follow soon. After a long time powered off, it can take up to about 15 minutes to download the satellite orbit data.`;
}

export function mount(root) {
  // ---- live readouts (updated in place, never rebuilt) ----
  const stText = h("strong", { class: "st-title" });
  const stMsg = h("p", { class: "hint st-msg" });
  const stPort = h("p", { class: "hint" });
  const warn = h("p", { class: "err", hidden: true });
  const utcLine = h("p", { class: "hint", hidden: true });
  const posRows = h("dl", { class: "kv" });

  // ---- time comparison table: cells are updated in place every 250 ms ----
  const gpsLabel = h("th", { scope: "row" }, "GPS");
  const cell = () => h("td", {}, "--");
  const cells ={ ntp: [cell(), cell(), cell()], gps: [cell(), cell(), cell()], sys: [cell(), cell(), cell()] };
  const timeTable = h("table", { class: "times" },
    h("thead", {}, h("tr", {}, ["Source", "Time now (UTC)", "Compared with internet time", "Compared with system clock"].map((t) => h("th", {}, t)))),
    h("tbody", {},
      h("tr", {}, h("th", { scope: "row" }, "Internet time (NTP)"), ...cells.ntp),
      h("tr", {}, gpsLabel, ...cells.gps),
      h("tr", {}, h("th", { scope: "row" }, "System clock"), ...cells.sys)));
  const verdict = h("p", { class: "callout" });
  const crossNote = h("p", { class: "callout", hidden: true });

  // ---- internet time (NTP) controls ----
  const ntpMsg = h("p", { class: "hint", "aria-live": "polite" }); // feedback from the buttons
  const ntpNote = h("p", { class: "hint err", hidden: true }); // a message from the checker itself
  const ntpSummary = h("p", {});
  const ntpBody = h("tbody");
  const ntpTable = h("table", { class: "sats", hidden: true },
    h("thead", {}, h("tr", {}, ["Server", "Result", "Offset", "Round trip", "Stratum"].map((t) => h("th", {}, t)))), ntpBody);
  const ntpToggle = h("input", { type: "checkbox", id: "ntp-on", onchange: async (e) => {
    try { await net.setEnabled(e.target.checked); ntpMsg.textContent = ""; } catch (err) { ntpMsg.textContent = err.message; e.target.checked = !e.target.checked; }
  } });
  const ntpBtn = h("button", { type: "button", onclick: runCheck }, "Check Now");

  async function runCheck() {
    ntpMsg.className = "hint";
    ntpMsg.textContent = "Checking the time servers...";
    ntpBtn.disabled = true;
    try {
      await net.checkNow();
      ntpMsg.textContent = "";
    } catch (e) {
      ntpMsg.className = "hint err";
      ntpMsg.textContent = e.status === 429 ? "Please wait a few seconds between checks." : e.message;
    } finally { ntpBtn.disabled = false; }
  }

  function renderNtp(s) {
    if (document.activeElement !== ntpToggle) ntpToggle.checked = !!s.enabled;
    ntpBtn.disabled = !!s.checking;
    ntpBody.replaceChildren();
    const results = s.results || [];
    ntpTable.hidden = results.length === 0;
    for (const r of results) {
      ntpBody.append(h("tr", {},
        h("td", {}, r.server),
        h("td", {}, r.ok ? "answered" : h("span", { class: "err" }, r.error || "no answer")),
        h("td", {}, r.ok ? signed(r.offset_ms) : "-"),
        h("td", {}, r.ok ? Math.round(r.delay_ms) + " ms" : "-"),
        h("td", {}, r.ok ? String(r.stratum) : "-")));
    }
    const age = net.ageMs();
    if (s.unavailable) ntpSummary.textContent = "Internet time is unavailable.";
    else if (s.offset_ms == null) ntpSummary.textContent = age == null ? "No check has been made yet." : "No time server answered.";
    else {
      const behind = s.offset_ms > 0;
      ntpSummary.textContent = Math.abs(s.offset_ms) < 50
        ? "Your computer's clock agrees with internet time."
        : `Internet time says your computer's clock is ${secs(s.offset_ms)} ${behind ? "slow" : "fast"}.`;
      ntpSummary.textContent += ` (${s.answered} of ${(s.servers || []).length} servers answered${s.spread_ms != null && s.answered > 1 ? ", within " + Math.round(s.spread_ms) + " ms of each other" : ""}; checked ${age == null ? "" : Math.round(age / 1000) + " s ago"}.)`;
    }
    ntpNote.textContent = s.message || "";
    ntpNote.hidden = !s.message;
    const cmp = s.vs_gps;
    crossNote.hidden = !cmp;
    crossNote.textContent = cmp ? cmp.note : "";
  }

  // ---- satellites ----
  const satSummary = h("p", { class: "hint" });
  const satHint = h("p", { class: "callout", hidden: true });
  const satBody = h("tbody");
  const satTable = h("table", { class: "sats", hidden: true },
    h("thead", {}, h("tr", {}, ["System", "PRN", "Elevation", "Azimuth", "Signal (dB-Hz)", "Used in fix"].map((t) => h("th", {}, t)))),
    satBody);
  const devInfo = h("div", { class: "hint", hidden: true });

  function renderSats(s) {
    const sats = (s.satellites || []).slice().sort((a, b) => (b.snr || 0) - (a.snr || 0) || a.sys.localeCompare(b.sys) || a.prn - b.prn);
    satBody.replaceChildren();
    satTable.hidden = sats.length === 0;
    if (sats.length === 0) {
      satSummary.textContent = s.status === "stopped" ? "Connect a receiver to see satellites." : "This receiver has not reported any satellites yet.";
    } else {
      const tracked = sats.filter((x) => x.snr != null).length;
      const used = sats.filter((x) => x.used).length;
      satSummary.textContent = `${sats.length} reported, ${tracked} with a signal, ${used} used in the fix.` +
        (s.sats_source === "ublox" ? " This receiver does not print satellite data, so it was asked directly (a read-only status request)." : "");
    }
    for (const x of sats) {
      satBody.append(h("tr", {},
        h("td", {}, x.sys),
        h("td", {}, String(x.prn)),
        h("td", {}, x.elev != null ? x.elev + "°" : "-"),
        h("td", {}, x.azim != null ? x.azim + "°" : "-"),
        h("td", {}, x.snr != null
          ? [h("meter", { min: "0", max: "50", low: "20", high: "30", optimum: "45", value: String(Math.min(x.snr, 50)), "aria-label": x.snr + " dB-Hz" }), " " + x.snr]
          : h("span", { class: "unit" }, "searching")),
        h("td", {}, x.used ? "yes" : "")));
    }
    const hint = diagnose(s);
    satHint.hidden = !hint;
    satHint.textContent = hint;

    devInfo.replaceChildren();
    const lines = [];
    if (s.version) {
      lines.push(`u-blox receiver: firmware ${s.version.firmware}, hardware ${s.version.hardware}` +
        (s.version.protocol ? `, protocol ${s.version.protocol}` : "") + (s.version.systems ? `, supports ${s.version.systems}` : "") + ".");
    }
    if (s.hardware) {
      const hw = s.hardware;
      lines.push(`Antenna: ${hw.antenna} (power ${hw.antenna_power}). Interference indicator: ${hw.jamming} of 255. Noise level: ${hw.noise}. Gain control: ${hw.agc} of 8191.`);
      // The interference indicator proved unreliable on a real dongle (it read 247 while tracking
      // satellites at 40 dB-Hz), so only mention it when nothing is being heard at all.
      const hearing = (s.satellites || []).some((x) => x.snr != null);
      if (hw.jamming >= 128 && !hearing) lines.push("A high, steady interference indicator can mean nearby electronics (laptops, USB 3 ports, monitors) are drowning out the satellite signals. Try moving the receiver away on a USB extension cable.");
    }
    devInfo.hidden = lines.length === 0;
    for (const l of lines) devInfo.append(h("p", {}, l));
  }

  function renderPosition(s) {
    posRows.replaceChildren();
    if (s.lat == null) { posRows.append(h("dt", {}, "Position"), h("dd", {}, "--")); return; }
    const rows = [
      ["Latitude", coord(s.lat, "N", "S")],
      ["Longitude", coord(s.lon, "E", "W")],
      ["Grid square", s.grid],
    ];
    if (s.alt_m != null) rows.push(["Altitude", `${s.alt_m.toFixed(0)} m  (${(s.alt_m * 3.28084).toFixed(0)} ft)`]);
    if (s.sats != null) rows.push(["Satellites used", String(s.sats)]);
    if (s.hdop != null) rows.push(["HDOP (lower is better)", s.hdop.toFixed(1)]);
    if (s.quality != null) rows.push(["Fix type", QUALITY[s.quality] || String(s.quality)]);
    for (const [k, v] of rows) posRows.append(h("dt", {}, k), h("dd", {}, v));
  }

  function renderState(s) {
    const [title, msg] = STATUS_TEXT[s.status] || [s.status, ""];
    stText.textContent = title;
    stMsg.textContent = s.message || msg;
    stPort.textContent = s.port ? `Receiver: ${s.port}${s.baud ? " at " + s.baud + " baud" : ""}` : "";
    stPort.hidden = !s.port;
    renderPosition(s);
    renderSats(s);
    const ut = utcText(s);
    utcLine.textContent = ut;
    utcLine.hidden = !ut;
    gpsLabel.textContent = s.correction_ms ? "GPS (leap-corrected)" : "GPS";
    const bad = s.date_suspect;
    warn.hidden = !bad;
    warn.textContent = bad ? "The date from this receiver differs from the computer's clock by more than a day. Some older receivers report a wrong date (GPS week-number rollover). The time of day is probably still right." : "";
    controlsState(s);
  }

  function renderClocks() {
    const sys = Date.now();
    const g = gpsNow();
    const n = net.ntpNow();
    cells.sys[0].textContent = dateTime(sys);
    cells.gps[0].textContent = g == null ? "no current fix" : dateTime(g);
    cells.ntp[0].textContent = n == null ? "not checked" : dateTime(n);
    // column 2: error against internet time (the reference); column 3: against the system clock
    cells.ntp[1].textContent = n == null ? "-" : "reference";
    cells.gps[1].textContent = g == null || n == null ? "-" : signed(g - n);
    cells.sys[1].textContent = n == null ? "-" : signed(sys - n);
    cells.ntp[2].textContent = n == null ? "-" : signed(n - sys);
    cells.gps[2].textContent = g == null ? "-" : signed(g - sys);
    cells.sys[2].textContent = "-";

    let text;
    if (n != null) {
      const err = sys - n; // positive: the system clock is fast
      text = Math.abs(err) <= LIMIT_MS
        ? "Your system clock is within half a second of internet time. That is good enough for FT8 and FT4."
        : `Your system clock is ${secs(err)} ${err < 0 ? "slow" : "fast"} compared with internet time. That is too far off for FT8 and FT4: resync it in your operating system's date and time settings.`;
    } else if (g != null) {
      const d = g - sys;
      text = Math.abs(d) <= LIMIT_MS
        ? "Your system clock agrees with GPS to within half a second."
        : `Your system clock differs from GPS by ${secs(d)}. Run an internet time check (under Internet Time) to see which one is right.`;
    } else {
      text = "No reference yet. Open Internet Time below and press Check Now to compare with internet time, or connect a GPS receiver.";
    }
    verdict.textContent = text;
  }

  // ---- receiver setup ----
  const portSel = h("select", { id: "port", "aria-label": "Serial port" });
  const baudSel = h("select", { id: "baud", "aria-label": "Baud rate" }, BAUDS.map(([v, l]) => h("option", { value: v }, l)));
  const msg = h("p", { class: "hint", "aria-live": "polite" });
  const found = h("ul", { class: "found" });
  const connectBtn = h("button", { type: "button", onclick: connect }, "Connect");
  const disconnectBtn = h("button", { type: "button", class: "quiet", onclick: disconnect, hidden: true }, "Disconnect");
  const refreshBtn = h("button", { type: "button", class: "quiet", onclick: loadPorts }, "Refresh list");
  const scanBtn = h("button", { type: "button", class: "quiet", onclick: scan }, "Find GPS");

  function say(text, isErr) { msg.textContent = text; msg.className = "hint" + (isErr ? " err" : ""); }

  function controlsState(s) {
    disconnectBtn.hidden = !s.port;
    if (s.port && portSel.value !== s.port && [...portSel.options].some((o) => o.value === s.port) && document.activeElement !== portSel) {
      portSel.value = s.port;
    }
  }

  async function loadPorts() {
    try {
      const { ports } = await api("/api/gps/ports");
      const keep = portSel.value;
      portSel.replaceChildren(...(ports.length
        ? ports.map((p) => h("option", { value: p.name }, `${p.name} - ${p.description}${p.usb ? " (USB)" : ""}`))
        : [h("option", { value: "" }, "No serial ports found")]));
      if (keep && ports.some((p) => p.name === keep)) portSel.value = keep;
      say(ports.length ? "" : "No serial ports were found. Plug in the receiver, then press Refresh list.");
    } catch (e) { say(e.message, true); }
  }

  async function connect(port, baud) {
    port = typeof port === "string" ? port : portSel.value;
    baud = typeof baud === "number" ? baud : Number(baudSel.value);
    if (!port) { say("Choose a port first.", true); return; }
    try {
      await api("/api/gps/connect", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ port, baud }) });
      say("");
      refresh();
    } catch (e) { say(e.message, true); }
  }

  async function disconnect() {
    try { await api("/api/gps/disconnect", { method: "POST" }); say("Disconnected. The receiver will not reconnect next time."); refresh(); }
    catch (e) { say(e.message, true); }
  }

  async function scan() {
    if (!confirm("Find GPS listens briefly on every serial port on this computer, including ports used by other devices.\n\nSome boards (for example many ESP32 boards) restart when a program opens their port. Continue?")) return;
    scanBtn.disabled = true;
    found.replaceChildren();
    say("Scanning serial ports. This can take up to 20 seconds...");
    try {
      const r = await api("/api/gps/scan", { method: "POST" });
      if (r.found.length === 0) say("No GPS receiver was found. Check that it is plugged in and has had a moment to start, then try again.", true);
      else say(r.complete ? "Found:" : "Found so far (the scan was cut short):");
      for (const f of r.found) {
        found.append(h("li", {}, h("button", { type: "button", onclick: () => connect(f.port, f.baud) }, `Use ${f.port} at ${f.baud} baud`), " " + f.description));
      }
    } catch (e) { say(e.message, true); }
    finally { scanBtn.disabled = false; }
  }

  // Time first, then location. Everything after Position folds away until opened.
  const fold = (title, ...body) => h("details", { class: "panel fold" }, h("summary", {}, title), ...body);
  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "sr-only" }, "Time & Location"),
      h("h2", {}, "Time Comparison"),
      timeTable, verdict, crossNote,
      h("p", { class: "hint" }, "Internet time is the most accurate reference when you are online (typically within a few hundredths of a second). A plain serial GPS receiver does not mark the exact top of each second, so GPS time here is good to roughly a third of a second. That is fine for FT8 and FT4, and it works with no internet. This page only displays times; it never changes your computer's clock.")),
    h("section", { class: "panel" }, h("h2", {}, "Position"), posRows),
    fold("Internet Time (NTP)",
      h("div", { class: "row wrap" }, ntpBtn,
        h("label", { class: "check" }, ntpToggle, " Also check automatically every 10 minutes")),
      ntpSummary, ntpMsg, ntpNote, ntpTable,
      h("p", { class: "hint" }, "Checking sends a small request to public time servers, which can see this computer's internet address. Nothing is sent unless you press Check Now or turn on automatic checks (off by default). The servers used are time.cloudflare.com, time.nist.gov and pool.ntp.org; they can be changed in time.json in your data folder.")),
    fold("GPS Receiver Status", stText, stMsg, stPort, utcLine, warn),
    fold("Satellites", satSummary, satHint, satTable, devInfo),
    fold("Receiver Settings",
      h("div", { class: "row wrap" }, portSel, baudSel, connectBtn, disconnectBtn, refreshBtn),
      h("div", { class: "row wrap" }, scanBtn, h("span", { class: "hint" }, "Not sure which port? Find GPS tries each one.")),
      msg, found,
      h("p", { class: "hint" }, "Any USB or Bluetooth GPS receiver that sends standard NMEA text works (most u-blox-based \"GPS dongles\" do). It shows up here as a serial port. On Linux your user must be in the \"dialout\" group to open it. The app remembers your choice and reconnects on the next launch.")),
  );

  const unsub = subscribe(renderState);
  const unsubNet = net.subscribe(renderNtp);
  loadPorts();
  renderClocks();
  const t = setInterval(renderClocks, 250);
  const t2 = setInterval(() => renderNtp(net.current()), 1000); // keeps "checked N s ago" current
  return () => { unsub(); unsubNet(); clearInterval(t); clearInterval(t2); };
}
