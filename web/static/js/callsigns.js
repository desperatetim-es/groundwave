// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Callsigns page: search the offline FCC database, with distance and bearing
// from your grid square, and download or update the database.
import { h, api } from "./dom.js";
import { current as gpsCurrent } from "./gpsstate.js";
import { GRID_RE, getHomeGrid, setHomeGrid } from "./homegrid.js";

// The home grid is shared with the Logbook (see homegrid.js).
const store = { get: getHomeGrid, set: setHomeGrid };

const nf = new Intl.NumberFormat("en-US");

function distanceText(r) {
  if (r.distance_mi == null) return "-";
  const mi = r.distance_mi < 10 ? r.distance_mi.toFixed(1) : Math.round(r.distance_mi);
  return `${mi} mi (${Math.round(r.distance_km)} km) ${r.compass}`;
}

function mb(bytes) { return (bytes / 1e6).toFixed(0); }

function ageText(s) {
  if (s.age_days == null) return "";
  if (s.age_days < 1) return "built today";
  return `built ${s.age_days} day${s.age_days === 1 ? "" : "s"} ago`;
}

// #/callsigns/<search>/<grid> opens the page with that search and your grid filled in.
export function mount(root, args = []) {
  let timer = null;
  let seq = 0;
  let statusTimer = null;
  let lastUpdateRunning = false;

  // ---- search ----
  const q = h("input", { type: "search", id: "cs-q", placeholder: "Call sign, name, City, ST, or ZIP", "aria-label": "Search callsigns", autocomplete: "off" });
  const grid = h("input", { type: "text", id: "cs-grid", maxlength: "8", placeholder: "e.g. EN52vf", "aria-label": "Your grid square", value: store.get() });
  const gridHint = h("span", { class: "hint" });
  const useGps = h("button", { type: "button", class: "quiet", onclick: () => fillFromGps(true) }, "Use GPS");
  const note = h("p", { class: "hint", "aria-live": "polite" });
  const rows = h("tbody");
  const table = h("table", { class: "sats", hidden: true },
    h("thead", {}, h("tr", {}, ["Call", "Name", "Class", "Status", "Location", "Grid", "From you"].map((t) => h("th", {}, t)))), rows);

  function validGrid() { return GRID_RE.test(grid.value.trim()) ? grid.value.trim() : ""; }

  function fillFromGps(force) {
    const g = gpsCurrent();
    if (g && g.status === "fix" && g.grid) {
      if (force || grid.value.trim() === "") { grid.value = g.grid; store.set(g.grid); gridHint.textContent = "from your GPS"; schedule(0); }
    } else if (force) {
      gridHint.textContent = "no GPS fix right now";
    }
  }

  function schedule(delay = 250) { clearTimeout(timer); timer = setTimeout(search, delay); }

  async function search() {
    const text = q.value.trim();
    gridHint.textContent = grid.value.trim() && !validGrid() ? "not a valid grid square (4, 6 or 8 characters like EN52vf)" : gridHint.textContent;
    if (!text) { rows.replaceChildren(); table.hidden = true; note.className = "hint"; note.textContent = EXAMPLES; return; }
    const mine = ++seq;
    const params = new URLSearchParams({ q: text, limit: "50" });
    if (validGrid()) params.set("from", validGrid());
    try {
      const { results } = await api("/api/callsigns/search?" + params);
      if (mine !== seq) return;
      render(results, text);
    } catch (e) {
      if (mine !== seq) return;
      rows.replaceChildren();
      table.hidden = true;
      note.className = "hint err";
      note.textContent = e.body && e.body.needs_download ? "There is no callsign database yet. Download it below, then search." : e.message;
    }
  }

  function render(results, text) {
    rows.replaceChildren();
    table.hidden = results.length === 0;
    note.className = "hint";
    note.textContent = results.length === 0 ? `No licenses match "${text}".` :
      `${results.length}${results.length === 50 ? " or more" : ""} result${results.length === 1 ? "" : "s"}.` + (validGrid() ? "" : " Enter your grid square to see distances.");
    for (const r of results) {
      const expired = r.standing !== "active"; // worked out by the server from the expiry date
      const nameCell = h("td", {}, r.name,
        r.club && h("span", { class: "tag" }, "club"),
        r.club && r.trustee_name && h("div", { class: "unit" }, `trustee ${r.trustee_name}${r.trustee_call ? " (" + r.trustee_call + ")" : ""}`),
        r.prev_call && h("div", { class: "unit" }, "previously " + r.prev_call));
      rows.append(h("tr", { class: expired ? "dim" : "" },
        h("td", {}, h("strong", {}, r.call)),
        nameCell,
        h("td", {}, r.class_name || (r.club ? "Club" : "-")),
        h("td", {}, r.standing_name || r.status_name, expired && r.expires && h("div", { class: "unit" }, "term ended " + r.expires)),
        h("td", {}, [r.city, [r.state, r.zip].filter(Boolean).join(" ")].filter(Boolean).join(", ") || "-"),
        h("td", { title: "Approximate: the centre of the licensee's ZIP code" }, r.grid || "-"),
        h("td", {}, distanceText(r))));
    }
  }

  const EXAMPLES = "Try a call sign (kd9, w1aw), a name (smith), a city and state (cary, il or Cary, Illinois), a ZIP code (60013), or filters (call:W9, st:IL, name:smith).";
  q.addEventListener("input", () => schedule());
  grid.addEventListener("input", () => {
    gridHint.textContent = "";
    if (validGrid()) store.set(validGrid());
    schedule(150);
  });

  // ---- the database ----
  const dbLine = h("p", {});
  const bar = h("progress", { max: "100", value: "0", hidden: true, "aria-label": "Update progress" });
  const barText = h("p", { class: "hint", "aria-live": "polite" });
  const updErr = h("p", { class: "hint err", hidden: true });
  const updateBtn = h("button", { type: "button", onclick: startUpdate }, "Update Database");
  const cancelBtn = h("button", { type: "button", class: "quiet", hidden: true, onclick: cancelUpdate }, "Cancel");

  async function startUpdate() {
    if (!confirm("Download the FCC amateur license file (about 200 MB) and build the callsign database?\n\nThis takes a few minutes and needs roughly 500 MB of free disk space while it runs. Searching afterwards works without the internet.")) return;
    updErr.hidden = true;
    try { renderStatus(await api("/api/callsigns/update", { method: "POST" })); } catch (e) { updErr.hidden = false; updErr.textContent = e.message; }
  }
  async function cancelUpdate() {
    try { renderStatus(await api("/api/callsigns/cancel", { method: "POST" })); } catch (e) { updErr.hidden = false; updErr.textContent = e.message; }
  }

  function renderStatus(s) {
    const u = s.update || {};
    updateBtn.disabled = !!u.running;
    updateBtn.textContent = s.available ? "Update Database" : "Download Database";
    cancelBtn.hidden = !u.running;
    bar.hidden = !u.running;
    if (u.running) {
      bar.value = Math.round((u.fraction || 0) * 100);
      barText.textContent = `${u.phase || "Working"}${u.bytes_total ? ` (${mb(u.bytes_done)} of ${mb(u.bytes_total)} MB)` : ""}: ${Math.round((u.fraction || 0) * 100)}%`;
    } else if (u.cancelled) barText.textContent = "The update was cancelled. Your existing database is unchanged.";
    else if (u.phase === "Done" && lastUpdateRunning) barText.textContent = "The database was updated.";
    else if (!u.error) barText.textContent = "";
    updErr.hidden = !u.error;
    updErr.textContent = u.error ? u.error + " Your existing database, if any, is unchanged." : "";
    if (s.available) {
      dbLine.textContent = `${nf.format(s.records)} licenses, ${ageText(s)}` + (s.source_modified ? `, from FCC data published ${s.source_modified.replace(/ \d\d:\d\d:\d\d GMT/, "")}.` : ".") +
        (s.age_days != null && s.age_days > 14 ? " The FCC updates its file weekly, so this one is getting old." : "");
    } else dbLine.textContent = "No database yet. It is downloaded from the FCC when you press the button, then searched here without the internet.";
    if (lastUpdateRunning && !u.running && s.available) schedule(0); // refresh results from the new data
    lastUpdateRunning = !!u.running;
  }

  async function loadStatus() {
    try { renderStatus(await api("/api/callsigns/status")); } catch (_) { /* keep what is shown */ }
  }

  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "sr-only" }, "Callsigns"),
      h("h2", {}, "Callsigns"),
      h("p", { class: "hint" }, "Search every US amateur license with no internet connection."),
      h("div", { class: "row wrap" }, q),
      h("div", { class: "row wrap" },
        h("label", { class: "check", for: "cs-grid" }, "Your grid square ", grid), useGps, gridHint),
      note, table,
      h("p", { class: "hint" }, "Locations are approximate: each is the centre of the licensee's ZIP code, so distances mean \"about how far\", not exact. Grid squares and distances can be corrected for mobile, POTA and SOTA stations when you log a contact.")),
    h("section", { class: "panel" },
      h("h2", {}, "Database"),
      dbLine,
      h("div", { class: "row wrap" }, updateBtn, cancelBtn),
      bar, barText, updErr,
      h("p", { class: "hint" }, "Source: the FCC's public amateur license file, which the FCC refreshes weekly. Only call sign, name, class, status, dates and city, state and ZIP are kept. Street addresses, phone numbers and email addresses are never saved. The download happens only when you press the button; searching never uses the internet.")),
  );

  note.textContent = EXAMPLES;
  const safe = (s) => { try { return decodeURIComponent(s || ""); } catch (_) { return ""; } };
  if (args[1] && GRID_RE.test(safe(args[1]))) { grid.value = safe(args[1]); store.set(grid.value); }
  if (args[0]) { q.value = safe(args[0]); schedule(0); }
  loadStatus();
  statusTimer = setInterval(loadStatus, 1500);
  setTimeout(() => fillFromGps(false), 1200); // by then the GPS state has loaded
  q.focus();

  return () => { clearTimeout(timer); clearInterval(statusTimer); seq++; };
}
