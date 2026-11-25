// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Callsigns page: search the offline databases (one per country, each downloaded from the country's
// licensing authority on request), with distance and bearing from your grid square.
import { h, api } from "./dom.js";
import { current as gpsCurrent } from "./gpsstate.js";
import { GRID_RE, getHomeGrid, setHomeGrid } from "./homegrid.js";

// The home grid is shared with the Logbook (see homegrid.js).
const store = { get: getHomeGrid, set: setHomeGrid };

const nf = new Intl.NumberFormat("en-US");
const COUNTRIES = { us: "United States", ca: "Canada" };

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
  const wasRunning = {}; // source id -> its update was running at the last status
  let installed = 0;     // how many country databases exist (results are labelled by country when more than one)

  // ---- search ----
  const q = h("input", { type: "search", id: "cs-q", placeholder: "Call sign, name, City, ST, or ZIP / postal code", "aria-label": "Search callsigns", autocomplete: "off" });
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
      const expired = r.standing !== "active" && r.standing !== "listed"; // worked out by the server (Canada has no expiry: its records are "listed")
      const nameCell = h("td", {}, r.name,
        r.club && h("span", { class: "tag" }, "club"),
        r.club && r.trustee_name && h("div", { class: "unit" }, `trustee ${r.trustee_name}${r.trustee_call ? " (" + r.trustee_call + ")" : ""}`),
        r.prev_call && h("div", { class: "unit" }, "previously " + r.prev_call));
      rows.append(h("tr", { class: expired ? "dim" : "" },
        h("td", {}, h("strong", {}, r.call), installed > 1 && r.country && h("span", { class: "tag", title: COUNTRIES[r.country] || r.country }, r.country.toUpperCase())),
        nameCell,
        h("td", {}, r.class_name || (r.club ? "Club" : "-")),
        h("td", {}, r.standing_name || r.status_name, expired && r.expires && h("div", { class: "unit" }, "term ended " + r.expires)),
        h("td", {}, [r.city, [r.state, r.zip].filter(Boolean).join(" ")].filter(Boolean).join(", ") || "-"),
        h("td", { title: "Approximate: the centre of the licensee's ZIP or postal-code area" }, r.grid || "-"),
        h("td", {}, distanceText(r))));
    }
  }

  const EXAMPLES = "Try a call sign (kd9, w1aw, ve3), a name (smith), a city and state or province (cary, il or Ottawa, ON), a ZIP code (60013), or filters (call:W9, st:IL, zip:60013, postal:K1A, name:smith).";
  q.addEventListener("input", () => schedule());
  grid.addEventListener("input", () => {
    gridHint.textContent = "";
    if (validGrid()) store.set(validGrid());
    schedule(150);
  });

  // ---- the databases: one block per country ----
  const dbBox = h("div", {});
  const blocks = {}; // source id -> its elements

  function makeBlock(src) {
    const b = {
      line: h("p", {}),
      bar: h("progress", { max: "100", value: "0", hidden: true, "aria-label": `${src.country} update progress` }),
      text: h("p", { class: "hint", "aria-live": "polite" }),
      err: h("p", { class: "hint err", hidden: true }),
      credit: h("p", { class: "hint" }, src.credit),
    };
    b.update = h("button", { type: "button", onclick: () => startUpdate(src) }, "Update Database");
    b.cancel = h("button", { type: "button", class: "quiet", hidden: true, onclick: () => cancelUpdate(src) }, "Cancel");
    b.root = h("div", { class: "dbblock" }, h("h3", {}, `${src.country} (${src.org})`), b.line, h("div", { class: "row wrap" }, b.update, b.cancel), b.bar, b.text, b.err, b.credit);
    return b;
  }

  async function startUpdate(src) {
    const b = blocks[src.id];
    if (!confirm(`Download the ${src.org} amateur license list for ${src.country} (${src.download}) and build the database?\n\nThis needs ${src.disk || "some"} of free disk space while it runs. Searching afterwards works without the internet.`)) return;
    b.err.hidden = true;
    try { renderStatus(await api("/api/callsigns/update?source=" + encodeURIComponent(src.id), { method: "POST" })); } catch (e) { b.err.hidden = false; b.err.textContent = e.message; }
  }
  async function cancelUpdate(src) {
    const b = blocks[src.id];
    try { renderStatus(await api("/api/callsigns/cancel?source=" + encodeURIComponent(src.id), { method: "POST" })); } catch (e) { b.err.hidden = false; b.err.textContent = e.message; }
  }

  function renderSource(src) {
    if (!blocks[src.id]) { blocks[src.id] = makeBlock(src); dbBox.append(blocks[src.id].root); }
    const b = blocks[src.id];
    const u = src.update || {};
    b.update.disabled = !!u.running;
    b.update.textContent = src.available ? "Update Database" : "Download Database";
    b.cancel.hidden = !u.running;
    b.bar.hidden = !u.running;
    if (u.running) {
      b.bar.value = Math.round((u.fraction || 0) * 100);
      b.text.textContent = `${u.phase || "Working"}${u.bytes_total ? ` (${mb(u.bytes_done)} of ${mb(u.bytes_total)} MB)` : ""}: ${Math.round((u.fraction || 0) * 100)}%`;
    } else if (u.cancelled) b.text.textContent = "The update was cancelled. Your existing database is unchanged.";
    else if (u.phase === "Done" && wasRunning[src.id]) b.text.textContent = "The database was updated.";
    else if (!u.error) b.text.textContent = "";
    b.err.hidden = !u.error;
    b.err.textContent = u.error ? u.error + " Your existing database, if any, is unchanged." : "";
    if (src.available) {
      b.line.textContent = `${nf.format(src.records)} licenses, ${ageText(src)}` + (src.source_modified ? `, from ${src.org} data published ${src.source_modified.replace(/ \d\d:\d\d:\d\d GMT/, "")}.` : ".") +
        (src.id === "us" && src.age_days != null && src.age_days > 14 ? " The FCC updates its file weekly, so this one is getting old." : "");
    } else b.line.textContent = `No ${src.country} database yet. It is downloaded from ${src.org} (${src.download}) when you press the button, then searched here without the internet.`;
    const finished = wasRunning[src.id] && !u.running && src.available;
    wasRunning[src.id] = !!u.running;
    return finished;
  }

  function renderStatus(s) {
    let refresh = false;
    installed = (s.sources || []).filter((x) => x.available).length;
    for (const src of s.sources || []) if (renderSource(src)) refresh = true;
    if (refresh) schedule(0); // refresh results from the new data
  }

  async function loadStatus() {
    try { renderStatus(await api("/api/callsigns/status")); } catch (_) { /* keep what is shown */ }
  }

  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "sr-only" }, "Callsigns"),
      h("h2", {}, "Callsigns"),
      h("p", { class: "hint" }, "Search amateur licenses with no internet connection. Download a country's list below to search it."),
      h("div", { class: "row wrap" }, q),
      h("div", { class: "row wrap" },
        h("label", { class: "check", for: "cs-grid" }, "Your grid square ", grid), useGps, gridHint),
      note, table,
      h("p", { class: "hint" }, "Locations are approximate: each is the centre of the licensee's ZIP code or postal-code area, so distances mean \"about how far\", not exact. Grid squares and distances can be corrected for mobile, POTA and SOTA stations when you log a contact.")),
    h("section", { class: "panel" },
      h("h2", {}, "Databases"),
      h("p", { class: "hint" }, "Each country's list is downloaded from its licensing authority only when you press the button; searching never uses the internet. Street addresses, phone numbers and email addresses are never saved."),
      dbBox),
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
