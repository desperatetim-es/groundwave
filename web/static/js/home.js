// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Home page: one tile per function. Unfinished functions are listed but not clickable.
import { h, api } from "./dom.js";

const TILES = [
  { route: "library", title: "Library", blurb: "Your own manuals, charts and notes. Add files or links, search, tag and open them." },
  { route: "antenna", title: "Tools", blurb: "Dipole, vertical and J-pole sizes, wavelength, SWR, feedline loss, EIRP and ERP." },
  { route: "callsigns", title: "Callsigns", blurb: "Look up any US amateur callsign offline: name, class, location, and distance from you." },
  { route: "clocks", title: "Time & Location", blurb: "GPS and internet (NTP) time compared with your computer's clock, plus your position and grid square." },
  { route: "logbook", title: "Logbook", blurb: "Log contacts offline with live distance and bearing, and import or export ADIF files." },
];

export function mount(root) {
  const count = h("span", { class: "unit" });
  const tiles = TILES.map((t) => {
    const body = [h("h2", {}, t.title, t.title === "Library" ? " " : "", t.title === "Library" ? count : ""), h("p", {}, t.blurb)];
    if (t.soon) return h("div", { class: "tile soon", "aria-disabled": "true" }, ...body, h("p", { class: "badge" }, "Coming later"));
    return h("a", { class: "tile", href: "#/" + t.route }, ...body);
  });
  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "page-title" }, "What do you need?"),
      h("div", { class: "tiles" }, ...tiles)),
  );
  api("/api/docs").then((docs) => {
    count.textContent = docs.length ? `(${docs.length} document${docs.length === 1 ? "" : "s"})` : "";
  }).catch(() => {});
}
