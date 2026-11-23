// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// License pages, reached from the "?" menu: #/legal/license and #/legal/third-party.
// Shown inside the app (not as a separate page) so it works in the Windows app
// window, which has no browser back button.
import { h } from "./dom.js";

const PAGES = {
  license: { title: "License", blurb: "groundwλve is free software under the GNU General Public License, version 3." },
  "third-party": { title: "Third-party notices", blurb: "The software and data included in groundwλve, and the licenses they come under." },
};

export function mount(root, args) {
  const name = PAGES[args[0]] ? args[0] : "license";
  const page = PAGES[name];
  const text = h("pre", { class: "legal-text", tabindex: "0", "aria-label": page.title + " text" }, "Loading...");
  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "page-title" }, page.title),
      h("p", { class: "hint" }, page.blurb),
      h("nav", { class: "tabs", "aria-label": "License pages" },
        ...Object.entries(PAGES).map(([key, p]) =>
          h("a", { href: "#/legal/" + key, "aria-selected": String(key === name) }, p.title))),
      text),
  );
  let live = true;
  fetch("/api/legal/" + name)
    .then((r) => (r.ok ? r.text() : Promise.reject(new Error("The server answered " + r.status))))
    .then((t) => { if (live) text.textContent = t; })
    .catch((e) => { if (live) text.textContent = "Could not load this page: " + e.message; });
  return () => { live = false; };
}
