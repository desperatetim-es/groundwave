// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// groundwλve front end: router, header clocks and Quit. Plain ES modules, no build step.
import { api } from "./dom.js";
import * as home from "./home.js";
import * as library from "./library.js";
import * as antenna from "./antenna.js";
import * as clocks from "./clocks.js";
import * as callsigns from "./callsigns.js";
import * as logbook from "./logbook.js";
import * as legal from "./legal.js";
import * as settings from "./settings.js";
import * as prefs from "./prefs.js";
import { initHelpMenu } from "./menu.js";
import { start as startGPS, gpsNow, shortNote, hms } from "./gpsstate.js";
import { start as startNTP, ntpNow, current as ntpState } from "./ntpstate.js";

const routes = { "": home, library, antenna, callsigns, logbook, clocks, gps: clocks, legal, settings }; // #/gps is the old name for Clocks
const titles = { library: "Library", antenna: "Tools", callsigns: "Callsigns", logbook: "Logbook", clocks: "Time & Location", gps: "Time & Location", legal: "License", settings: "Settings" };
const view = document.getElementById("view");
let cleanup = null;

function navigate() {
  if (cleanup) { try { cleanup(); } catch (_) { /* ignore */ } cleanup = null; }
  const parts = location.hash.replace(/^#\/?/, "").split("/");
  const name = routes[parts[0]] ? parts[0] : "";
  view.replaceChildren();
  cleanup = routes[name].mount(view, parts.slice(1)) || null;
  for (const a of document.querySelectorAll("nav a")) {
    if (a.dataset.route === (name === "gps" ? "clocks" : name)) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  const gear = document.getElementById("settings-btn");
  if (name === "settings") gear.setAttribute("aria-current", "page"); else gear.removeAttribute("aria-current");
  document.title = (titles[name] ? titles[name] + " - " : "") + "groundwλve";
}
window.addEventListener("hashchange", navigate);
navigate();

// Pages the user chose to hide (Settings) leave the top menu; the home page does the same for its tiles.
// A hidden page is only unlisted: a link to it still works.
function applyHidden() {
  const hidden = new Set(prefs.hiddenPages());
  for (const a of document.querySelectorAll("nav a[data-route]")) a.hidden = hidden.has(a.dataset.route);
}
prefs.subscribe(applyHidden);
prefs.load();

// ---- header clocks: GPS time above system time ---------------------------------
const clockGps = document.getElementById("clock-gps");
const clockGpsNote = document.getElementById("clock-gps-note");
const clockSys = document.getElementById("clock-sys");
const clockSysNote = document.getElementById("clock-sys-note");
const clock = document.getElementById("clock");

function tick() {
  clockSys.textContent = hms(Date.now());
  const g = gpsNow();
  if (g == null) {
    clockGps.textContent = "--:--:--";
    clock.classList.add("nogps");
  } else {
    clockGps.textContent = hms(g);
    clock.classList.remove("nogps");
  }
  clockGpsNote.textContent = shortNote();

  // SYS badge: how the computer's clock compares with a fresh internet-time check.
  // (Internet time is a measurement, not a third clock, so it has no row of its own.)
  const n = ntpNow();
  if (n == null) {
    clockSysNote.textContent = "";
    clockSysNote.className = "cnote";
  } else {
    const err = Date.now() - n; // positive: the system clock is fast
    if (Math.abs(err) <= 500) {
      clockSysNote.textContent = "in sync";
      clockSysNote.className = "cnote ok";
    } else {
      clockSysNote.textContent = (Math.abs(err) / 1000).toFixed(1) + " s " + (err < 0 ? "slow" : "fast");
      clockSysNote.className = "cnote warn";
    }
  }
}
setInterval(tick, 250);
tick();
startGPS();
startNTP();
initHelpMenu();

// ---- app window title bar (Windows app only) -----------------------------------
// The Go side sets window.gwApp and binds gwDrag, gwMinimize, gwToggleMaximize,
// gwIsMaximized and gwClose. In a browser tab none of this runs.
if (window.gwApp && window.gwDrag) {
  document.documentElement.classList.add("app-window");
  document.getElementById("wincontrols").hidden = false;
  const bar = document.querySelector(".bar");
  const onBackground = (e) => e.button === 0 && !e.target.closest("a, button, input, select, textarea, .menu-pop");
  bar.addEventListener("mousedown", (e) => { if (onBackground(e) && e.detail === 1) { e.preventDefault(); window.gwDrag(); } });
  bar.addEventListener("dblclick", (e) => { if (onBackground(e)) window.gwToggleMaximize(); });
  const maxBtn = document.getElementById("win-max");
  const syncMax = async () => {
    try {
      const m = await window.gwIsMaximized();
      maxBtn.classList.toggle("is-max", m);
      maxBtn.title = maxBtn.ariaLabel = m ? "Restore" : "Maximize";
    } catch (_) { /* ignore */ }
  };
  window.addEventListener("resize", syncMax);
  syncMax();
  document.getElementById("win-min").addEventListener("click", () => window.gwMinimize());
  maxBtn.addEventListener("click", () => window.gwToggleMaximize());
  document.getElementById("win-close").addEventListener("click", () => window.gwClose());
}

// ---- quit --------------------------------------------------------------------
document.getElementById("quit").addEventListener("click", async () => {
  if (!confirm("Stop groundwλve? You can start it again any time.")) return;
  try { await api("/api/quit", { method: "POST" }); } catch (_) { /* server may close first */ }
  document.body.textContent = "groundwλve has stopped. You can close this tab.";
});
