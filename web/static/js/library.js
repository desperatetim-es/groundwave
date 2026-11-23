// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Document library view.
import { h, api } from "./dom.js";

function fmtSize(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(0) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}
const TYPES = { pdf: "PDF", txt: "Text", png: "PNG", jpg: "JPEG" };

export function mount(root) {
  let timer = null;
  const status = h("ul", { class: "status", "aria-live": "polite" });
  const count = h("span", { class: "unit" });
  const rows = h("tbody");
  const empty = h("p", { class: "hint", hidden: true });
  const table = h("table", { hidden: true },
    h("thead", {}, h("tr", {}, ["Title", "Tags", "Type", "Size", "Added", ""].map((t) => h("th", {}, t)))),
    rows);
  const q = h("input", { type: "search", placeholder: "Search title, tags, source", "aria-label": "Search the library" });

  function say(text, cls) {
    status.prepend(h("li", { class: cls }, text));
    while (status.children.length > 8) status.lastChild.remove();
  }

  async function load() {
    const term = q.value.trim();
    let docs = [];
    try {
      docs = await api("/api/docs?q=" + encodeURIComponent(term));
    } catch (e) {
      say("Could not load the library: " + e.message, "err");
    }
    render(docs, term);
  }

  function render(docs, term) {
    rows.replaceChildren();
    count.textContent = docs.length ? `(${docs.length})` : "";
    table.hidden = docs.length === 0;
    empty.hidden = docs.length !== 0;
    empty.textContent = term ? "No documents match that search." : "Nothing here yet. Add your first document above.";
    for (const d of docs) {
      rows.append(h("tr", {},
        h("td", {},
          h("a", { href: "/files/" + encodeURIComponent(d.id), target: "_blank", rel: "noopener" }, d.title),
          d.source_url && h("div", { class: "unit" }, "from " + d.source_url)),
        h("td", {}, (d.tags || []).map((t) => h("span", { class: "tag" }, t))),
        h("td", {}, TYPES[d.ext] || d.ext),
        h("td", {}, fmtSize(d.size)),
        h("td", {}, new Date(d.added).toLocaleDateString()),
        h("td", { class: "actions" },
          h("button", { type: "button", class: "link", onclick: () => rename(d) }, "Rename"),
          h("button", { type: "button", class: "link", onclick: () => retag(d) }, "Tags"),
          h("button", { type: "button", class: "danger", onclick: () => remove(d) }, "Delete")),
      ));
    }
  }

  async function patch(d, body) {
    try {
      await api("/api/docs/" + encodeURIComponent(d.id), {
        method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
      });
      load();
    } catch (e) { say(e.message, "err"); }
  }
  function rename(d) {
    const t = prompt("Title", d.title);
    if (t !== null && t.trim() !== "") patch(d, { title: t });
  }
  function retag(d) {
    const t = prompt("Tags, separated by commas", (d.tags || []).join(", "));
    if (t !== null) patch(d, { tags: t.split(",") });
  }
  async function remove(d) {
    if (!confirm(`Delete "${d.title}" from the library? The file will be removed.`)) return;
    try {
      await api("/api/docs/" + encodeURIComponent(d.id), { method: "DELETE" });
      say(`Deleted "${d.title}".`, "ok");
      load();
    } catch (e) { say(e.message, "err"); }
  }

  async function uploadFiles(files) {
    for (const f of files) {
      const fd = new FormData();
      fd.append("file", f);
      say(`Adding ${f.name}...`);
      try {
        const doc = await api("/api/docs", { method: "POST", body: fd });
        say(`Added "${doc.title}" (${fmtSize(doc.size)}).`, "ok");
      } catch (e) {
        if (e.status === 409 && e.body && e.body.doc) say(`${f.name} is already in the library as "${e.body.doc.title}".`, "err");
        else say(`${f.name}: ${e.message}`, "err");
      }
    }
    load();
  }

  const fileInput = h("input", {
    type: "file", multiple: true,
    accept: ".pdf,.txt,.md,.png,.jpg,.jpeg,application/pdf,text/plain,image/png,image/jpeg",
    onchange: (e) => { uploadFiles(Array.from(e.target.files)); e.target.value = ""; },
  });
  const drop = h("label", { class: "drop" }, fileInput, h("span", {}, "Choose files, or drop them here"));
  for (const ev of ["dragenter", "dragover"]) drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.add("over"); });
  for (const ev of ["dragleave", "drop"]) drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.remove("over"); });
  drop.addEventListener("drop", (e) => uploadFiles(Array.from(e.dataTransfer.files)));

  const urlInput = h("input", { type: "url", id: "url", placeholder: "https://example.org/manual.pdf", required: true });
  const urlForm = h("form", { class: "url", onsubmit: async (e) => {
    e.preventDefault();
    const url = urlInput.value.trim();
    const btn = e.target.querySelector("button");
    btn.disabled = true;
    say("Downloading " + url + " ...");
    try {
      const doc = await api("/api/docs/url", {
        method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ url }),
      });
      say(`Added "${doc.title}" (${fmtSize(doc.size)}).`, "ok");
      urlInput.value = "";
      load();
    } catch (err) {
      if (err.status === 409 && err.body && err.body.doc) say(`That file is already in the library as "${err.body.doc.title}".`, "err");
      else say(err.message, "err");
    } finally { btn.disabled = false; }
  } },
    h("label", { for: "url" }, "Or a direct link to a file"),
    h("div", { class: "row" }, urlInput, h("button", { type: "submit" }, "Download")));

  q.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(load, 150); });

  root.append(
    h("h1", { class: "sr-only" }, "Library"),
    h("section", { class: "panel", "aria-labelledby": "add-h" },
      h("h2", { id: "add-h" }, "Add to Library"),
      h("div", { class: "add" }, drop, urlForm),
      h("p", { class: "hint" }, "PDF, plain text or Markdown, PNG and JPEG. You are responsible for having the right to keep the files you add."),
      status),
    h("section", { class: "panel", "aria-labelledby": "lib-h" },
      h("div", { class: "row spread" }, h("h2", { id: "lib-h" }, "Library ", count), q),
      empty, table),
  );
  load();
  return () => clearTimeout(timer);
}
