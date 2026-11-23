// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Logbook page: log contacts offline, see distance from you live, import and export ADIF.
// Everything is stored in a file on this computer; nothing is sent anywhere.
import { h, api } from "./dom.js";
import { gpsNow } from "./gpsstate.js";
import { GRID_RE, getHomeGrid, setHomeGrid, gpsGrid } from "./homegrid.js";

const PAGE = 50;
const MODES = ["SSB", "CW", "FM", "AM", "FT8", "FT4", "JS8", "RTTY", "PSK31", "DMR", "DSTAR", "C4FM", "SSTV", "OLIVIA", "MFSK", "JT65", "JT9"];
const NOTE_ERR = "hint err";

const pad = (n) => String(n).padStart(2, "0");
function nowParts() {
  const iso = new Date(gpsNow() ?? Date.now()).toISOString(); // GPS time when there is a fix, else this computer's clock
  return { date: iso.slice(0, 10), time: iso.slice(11, 19) };
}
const showDate = (d) => (d.length === 8 ? `${d.slice(0, 4)}-${d.slice(4, 6)}-${d.slice(6)}` : d);
const showTime = (t) => (t.length === 6 ? `${t.slice(0, 2)}:${t.slice(2, 4)}:${t.slice(4)}` : t);
const digits = (s) => s.replace(/[^0-9]/g, "");

function distText(d) {
  if (d.distance_mi == null) return "";
  const mi = d.distance_mi < 10 ? d.distance_mi.toFixed(1) : Math.round(d.distance_mi);
  return `${mi} mi (${Math.round(d.distance_km)} km) ${d.compass}`;
}

// The FCC lists names as "Last, First M": a person is greeted by first name.
function firstName(r) {
  if (r.club) return r.name || "";
  const i = (r.name || "").indexOf(",");
  return i < 0 ? r.name || "" : r.name.slice(i + 1).trim().split(/\s+/)[0] || "";
}

export function mount(root) {
  let editingId = null;
  let limit = PAGE;
  let loadSeq = 0, lookupSeq = 0, distSeq = 0;
  let lookupTimer = null, distTimer = null;
  let listFrom = null; // the grid the table's distances were last worked out from
  let gridSource = "";
  const touched = { grid: false, name: false, qth: false, when: false };

  const input = (attrs) => h("input", { autocomplete: "off", spellcheck: "false", ...attrs });
  const fld = (label, el, cls = "") => h("label", { class: "fld " + cls }, h("span", {}, label), el);

  // ---- the form ----
  const call = input({ type: "text", id: "lb-call", maxlength: "20", autocapitalize: "characters", "aria-label": "Call sign" });
  const date = input({ type: "text", maxlength: "10", placeholder: "YYYY-MM-DD", "aria-label": "Date, UTC" });
  const time = input({ type: "text", maxlength: "8", placeholder: "HH:MM:SS", "aria-label": "Time, UTC" });
  const nowBtn = h("button", { type: "button", class: "quiet", title: "Set the date and time to now (GPS time when there is a fix)", onclick: () => { touched.when = false; setNow(); } }, "Now");
  const freq = input({ type: "text", inputmode: "decimal", placeholder: "14.074", "aria-label": "Frequency in MHz" });
  const mode = input({ type: "text", list: "lb-modes", maxlength: "20", placeholder: "SSB", "aria-label": "Mode" });
  const rstS = input({ type: "text", maxlength: "6", placeholder: "59", "aria-label": "Signal report sent" });
  const rstR = input({ type: "text", maxlength: "6", placeholder: "59", "aria-label": "Signal report received" });
  const name = input({ type: "text", maxlength: "100", "aria-label": "Name" });
  const qth = input({ type: "text", maxlength: "100", "aria-label": "Place" });
  const grid = input({ type: "text", maxlength: "8", placeholder: "EN52wd", "aria-label": "Their grid square" });
  const myGrid = input({ type: "text", maxlength: "8", placeholder: "EN52vf", "aria-label": "Your grid square", value: getHomeGrid() });
  const pota = input({ type: "text", maxlength: "40", placeholder: "US-1234", "aria-label": "POTA reference of the other station" });
  const sota = input({ type: "text", maxlength: "40", placeholder: "W9/NC-001", "aria-label": "SOTA reference of the other station" });
  const myPota = input({ type: "text", maxlength: "40", placeholder: "US-1234", "aria-label": "Your POTA reference" });
  const mySota = input({ type: "text", maxlength: "40", placeholder: "W9/NC-001", "aria-label": "Your SOTA reference" });
  const comment = h("textarea", { rows: "2", maxlength: "2000", "aria-label": "Notes" });
  const lookupLine = h("p", { class: "hint", "aria-live": "polite" });
  const gridLine = h("p", { class: "hint", "aria-live": "polite" });
  const msg = h("p", { class: "hint", "aria-live": "polite" });
  const saveBtn = h("button", { type: "submit" }, "Save Contact");
  const clearBtn = h("button", { type: "button", class: "quiet", onclick: () => reset() }, "Clear");
  const formTitle = h("h2", {}, "Log a Contact");
  const validGrid = (el) => (GRID_RE.test(el.value.trim()) ? el.value.trim() : "");

  function setNow() {
    const n = nowParts();
    date.value = n.date;
    time.value = n.time;
  }

  // ---- callsign lookup: fills in the grid, name and place, unless you have typed your own ----
  function scheduleLookup() {
    clearTimeout(lookupTimer);
    lookupTimer = setTimeout(lookup, 350);
  }
  async function lookup() {
    const c = call.value.trim().toUpperCase();
    const mine = ++lookupSeq;
    if (!/^[A-Z0-9]{3,}(\/[A-Z0-9]+)*$/.test(c) || !/[0-9]/.test(c)) { apply(null, ""); return; }
    try {
      const { results } = await api("/api/callsigns/search?" + new URLSearchParams({ call: c }));
      if (mine === lookupSeq) apply(results[0] || null, results.length ? "" : "Not in the callsign database.");
    } catch (e) {
      if (mine === lookupSeq) apply(null, e.body && e.body.needs_download ? "No callsign database yet (Callsigns page), so grid and name are not filled in." : "");
    }
  }
  function apply(r, note) {
    lookupLine.textContent = r
      ? [r.name, [r.city, r.state].filter(Boolean).join(", "), r.class_name, r.standing && r.standing !== "active" ? r.standing_name : ""].filter(Boolean).join(" - ")
      : note;
    if (!touched.name) name.value = r ? firstName(r) : "";
    if (!touched.qth) qth.value = r ? r.city || "" : "";
    if (!touched.grid) {
      grid.value = r && r.grid ? r.grid : "";
      gridSource = grid.value ? "database" : "";
    }
    updateGridLine();
    scheduleDist();
  }

  function updateGridLine() {
    gridLine.textContent = !grid.value ? "" : gridSource === "database"
      ? "From the callsign database: the centre of their ZIP code, so only approximate. Type a grid square to replace it (for a mobile, POTA or SOTA station)."
      : "";
  }

  // ---- live distance for the contact being entered ----
  const distLine = h("p", { class: "dist", "aria-live": "polite" });
  function fromGrid() { return validGrid(myGrid) || gpsGrid() || getHomeGrid(); }
  function scheduleDist() {
    clearTimeout(distTimer);
    distTimer = setTimeout(showDist, 200);
  }
  async function showDist() {
    const to = validGrid(grid), from = fromGrid();
    const mine = ++distSeq;
    if (!to || !from) { distLine.textContent = to && !from ? "Enter your own grid square to see the distance." : ""; return; }
    try {
      const d = await api("/api/geo?" + new URLSearchParams({ from, to }));
      if (mine === distSeq) distLine.textContent = `Distance ${distText(d)} from ${from}`;
    } catch (_) { if (mine === distSeq) distLine.textContent = ""; }
  }

  call.addEventListener("input", () => {
    if (!touched.when && !editingId) setNow(); // the time is when you start the contact
    scheduleLookup();
  });
  for (const el of [date, time]) el.addEventListener("input", () => { touched.when = true; });
  name.addEventListener("input", () => { touched.name = true; });
  qth.addEventListener("input", () => { touched.qth = true; });
  grid.addEventListener("input", () => {
    touched.grid = true;
    gridSource = grid.value.trim() ? "user" : "";
    if (!grid.value.trim()) touched.grid = false; // emptied: go back to the database's grid
    updateGridLine();
    scheduleDist();
    if (!touched.grid) scheduleLookup();
  });
  myGrid.addEventListener("input", () => {
    if (validGrid(myGrid)) setHomeGrid(validGrid(myGrid));
    scheduleDist();
  });

  function reset(keep = false) {
    editingId = null;
    formTitle.textContent = "Log a Contact";
    saveBtn.textContent = "Save Contact";
    clearBtn.textContent = "Clear";
    for (const k of Object.keys(touched)) touched[k] = false;
    for (const el of [call, rstS, rstR, name, qth, grid, pota, sota, comment]) el.value = "";
    gridSource = "";
    if (!keep) { freq.value = ""; mode.value = ""; }
    setNow();
    lookupLine.textContent = gridLine.textContent = distLine.textContent = "";
    if (!validGrid(myGrid)) myGrid.value = gpsGrid() || getHomeGrid();
    call.focus();
  }

  function edit(q) {
    editingId = q.id;
    formTitle.textContent = "Edit Contact";
    saveBtn.textContent = "Save Changes";
    clearBtn.textContent = "Cancel";
    for (const k of Object.keys(touched)) touched[k] = true;
    call.value = q.call;
    date.value = showDate(q.date);
    time.value = showTime(q.time);
    freq.value = q.freq_mhz ? String(q.freq_mhz) : "";
    mode.value = q.submode || q.mode; // FT4 is stored as mode MFSK, submode FT4; shown as typed
    rstS.value = q.rst_sent || ""; rstR.value = q.rst_rcvd || "";
    name.value = q.name || ""; qth.value = q.qth || "";
    grid.value = q.grid || ""; gridSource = q.grid_source || "";
    myGrid.value = q.my_grid || myGrid.value;
    pota.value = q.pota_ref || ""; sota.value = q.sota_ref || "";
    myPota.value = q.my_pota_ref || ""; mySota.value = q.my_sota_ref || "";
    comment.value = q.comment || "";
    lookupLine.textContent = "";
    updateGridLine();
    scheduleDist();
    msg.textContent = "";
    window.scrollTo({ top: 0, behavior: "smooth" });
    call.focus();
  }

  const form = h("form", { class: "panel", novalidate: true },
    formTitle,
    h("div", { class: "fgrid" },
      fld("Call sign", call), fld("Date (UTC)", date), fld("Time (UTC)", h("span", { class: "row" }, time, nowBtn)),
      fld("Frequency (MHz)", freq), fld("Mode", mode), fld("Sent", rstS), fld("Received", rstR)),
    lookupLine,
    h("div", { class: "fgrid" },
      fld("Name", name), fld("Place", qth), fld("Their grid", grid), fld("Your grid", myGrid)),
    gridLine, distLine,
    h("details", {}, h("summary", {}, "Parks, summits and notes"),
      h("div", { class: "fgrid" }, fld("Their POTA park", pota), fld("Their SOTA summit", sota), fld("Your POTA park", myPota), fld("Your SOTA summit", mySota)),
      fld("Notes", comment, "wide")),
    h("div", { class: "row wrap" }, saveBtn, clearBtn),
    msg,
    h("datalist", { id: "lb-modes" }, MODES.map((m) => h("option", { value: m }))));

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const body = {
      call: call.value.trim(), date: digits(date.value), time: digits(time.value),
      freq_mhz: parseFloat(freq.value) || 0, mode: mode.value.trim(),
      rst_sent: rstS.value, rst_rcvd: rstR.value, name: name.value, qth: qth.value,
      grid: grid.value.trim(), grid_source: gridSource, my_grid: myGrid.value.trim(),
      pota_ref: pota.value, sota_ref: sota.value, my_pota_ref: myPota.value, my_sota_ref: mySota.value,
      comment: comment.value,
    };
    saveBtn.disabled = true;
    try {
      const saved = await api(editingId ? `/api/logbook/${editingId}` : "/api/logbook", {
        method: editingId ? "PUT" : "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const wasEdit = !!editingId;
      reset(true);
      msg.className = "hint ok";
      msg.textContent = `${wasEdit ? "Updated" : "Saved"} ${saved.call}.`;
      await load();
    } catch (e) {
      msg.className = NOTE_ERR;
      msg.textContent = e.message;
    } finally { saveBtn.disabled = false; }
  });

  // ---- the list ----
  const search = h("input", { type: "search", placeholder: "Search call, name, place, grid, park or note", "aria-label": "Search the log" });
  const rows = h("tbody");
  const table = h("table", { class: "sats log", hidden: true },
    h("thead", {}, h("tr", {}, ["Date (UTC)", "Call", "Freq / Band", "Mode", "RST", "Name / Place", "Grid", "From you", ""].map((t) => h("th", {}, t)))), rows);
  const summary = h("p", { class: "hint", "aria-live": "polite" });
  const more = h("button", { type: "button", class: "quiet", hidden: true, onclick: () => { limit += PAGE; load(); } }, "Show More");
  let searchTimer = null;
  search.addEventListener("input", () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { limit = PAGE; load(); }, 250); });

  async function load() {
    const mine = ++loadSeq;
    const from = gpsGrid() || getHomeGrid();
    listFrom = from;
    const p = new URLSearchParams({ limit: String(limit) });
    if (search.value.trim()) p.set("q", search.value.trim());
    if (GRID_RE.test(from)) p.set("from", from);
    let data;
    try { data = await api("/api/logbook?" + p); } catch (e) {
      if (mine !== loadSeq) return;
      summary.className = NOTE_ERR; summary.textContent = e.message; return;
    }
    if (mine !== loadSeq) return;
    summary.className = "hint";
    rows.replaceChildren();
    table.hidden = data.qsos.length === 0;
    more.hidden = data.total <= data.qsos.length;
    summary.textContent = data.total === 0
      ? (search.value.trim() ? "No contacts match." : "No contacts yet. Log one above, or import an ADIF file below.")
      : `${data.total} contact${data.total === 1 ? "" : "s"}${search.value.trim() ? " match" : " in your log"}.` + (GRID_RE.test(from) ? "" : " Enter your grid square to see distances.");
    for (const q of data.qsos) rows.append(row(q));
  }

  function row(q) {
    const approx = q.shown_grid_from === "database";
    const d = distText(q);
    const modeText = q.submode ? q.submode : q.mode;
    return h("tr", {},
      h("td", {}, showDate(q.date), h("div", { class: "unit" }, showTime(q.time).slice(0, 5))),
      h("td", {}, h("strong", {}, q.call), (q.pota_ref || q.sota_ref) && h("div", { class: "unit" }, [q.pota_ref, q.sota_ref].filter(Boolean).join(" "))),
      h("td", {}, q.freq_mhz ? q.freq_mhz : "", h("div", { class: "unit" }, q.band)),
      h("td", {}, modeText),
      h("td", {}, [q.rst_sent, q.rst_rcvd].filter(Boolean).join(" / ")),
      h("td", {}, q.name, h("div", { class: "unit" }, q.qth)),
      h("td", { title: approx ? "From the callsign database: the centre of their ZIP code" : "" }, q.shown_grid ? (approx ? "~" : "") + q.shown_grid : ""),
      h("td", { title: approx ? "Approximate: based on the centre of their ZIP code" : "" }, d ? (approx ? "~" : "") + d : ""),
      h("td", { class: "actions" },
        h("button", { type: "button", class: "quiet", onclick: () => edit(q) }, "Edit"),
        h("button", { type: "button", class: "danger", onclick: () => remove(q) }, "Delete")));
  }

  async function remove(q) {
    if (!confirm(`Delete the contact with ${q.call} on ${showDate(q.date)} at ${showTime(q.time).slice(0, 5)} UTC?\n\nThis cannot be undone. Export your log first if you are not sure.`)) return;
    try {
      await api(`/api/logbook/${q.id}`, { method: "DELETE" });
      if (editingId === q.id) reset(true);
      await load();
    } catch (e) { summary.className = NOTE_ERR; summary.textContent = e.message; }
  }

  // ---- import and export ----
  const importMsg = h("div", { "aria-live": "polite" });
  const file = h("input", { type: "file", accept: ".adi,.adif,.txt", hidden: true });
  file.addEventListener("change", async () => {
    const f = file.files[0];
    file.value = "";
    if (!f) return;
    importMsg.replaceChildren(h("p", { class: "hint" }, "Reading " + f.name + "..."));
    try {
      // the raw bytes are sent as they are: ADIF lengths count bytes, so the text must not be re-encoded
      const r = await api("/api/logbook/import", { method: "POST", headers: { "Content-Type": "text/plain" }, body: f });
      importMsg.replaceChildren(
        h("p", { class: "hint ok" }, `Read ${r.records} record${r.records === 1 ? "" : "s"}: ${r.added} added, ${r.duplicates} already in your log, ${r.skipped} skipped.`),
        r.problems && h("ul", { class: "hint" }, r.problems.map((p) => h("li", {}, p))));
      await load();
    } catch (e) { importMsg.replaceChildren(h("p", { class: NOTE_ERR }, e.message)); }
  });

  root.append(
    h("h1", { class: "sr-only" }, "Logbook"),
    form,
    h("section", { class: "panel" },
      h("h2", {}, "Contacts"),
      h("div", { class: "row wrap" }, search),
      summary,
      h("div", { class: "scroll" }, table),
      more),
    h("section", { class: "panel" },
      h("h2", {}, "Import and Export"),
      h("div", { class: "row wrap" },
        h("button", { type: "button", onclick: () => file.click() }, "Import ADIF File"),
        h("a", { class: "btn", href: "/api/logbook/export", download: "" }, "Export ADIF File"),
        file),
      importMsg,
      h("p", { class: "hint" }, "ADIF (.adi) is the standard log file every logging program reads and writes, so this is how you move a log in or out, and how you keep a backup. Importing skips contacts already in the log (same call sign, day, minute, band and mode). Your log is a single file, logbook/logbook.db, in your groundwave data folder. A grid square that was only filled in from the callsign database is approximate, so it is not written to exported files.")));

  reset();
  load();
  // distances follow you: when the GPS grid (or the one you typed) changes, the table is refreshed
  const follow = setInterval(() => {
    const g = gpsGrid() || getHomeGrid();
    if (g !== listFrom) load();
    if (!myGrid.value.trim() && g) { myGrid.value = g; scheduleDist(); }
  }, 2000);

  return () => { clearInterval(follow); clearTimeout(lookupTimer); clearTimeout(distTimer); clearTimeout(searchTimer); loadSeq++; lookupSeq++; distSeq++; };
}
