// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// The "?" menu in the header: a small popdown with the version, the license pages, a link to the project
// on GitHub and a "Check for updates" button. Nothing here contacts the internet until that button is pressed.
import { api, h } from "./dom.js";

export function initHelpMenu() {
  const btn = document.getElementById("help-btn");
  const pop = document.getElementById("help-pop");
  const about = document.getElementById("about-line");
  if (!btn || !pop) return;

  const items = () => [...pop.querySelectorAll("[role=menuitem]")];
  const isOpen = () => !pop.hidden;

  function open() {
    pop.hidden = false;
    btn.setAttribute("aria-expanded", "true");
  }
  function close(returnFocus) {
    if (!isOpen()) return;
    pop.hidden = true;
    showResult(""); // a stale answer should not greet the next opening
    btn.setAttribute("aria-expanded", "false");
    if (returnFocus) btn.focus();
  }

  btn.addEventListener("click", () => (isOpen() ? close(false) : open()));

  // Close on: a click elsewhere, choosing an item, navigating, Escape, or tabbing away.
  document.addEventListener("click", (e) => { if (!e.target.closest("#help-menu")) close(false); });
  pop.addEventListener("click", (e) => { if (e.target.closest("a")) close(false); });
  window.addEventListener("hashchange", () => close(false));
  document.getElementById("help-menu").addEventListener("focusout", (e) => {
    if (e.relatedTarget && !e.relatedTarget.closest("#help-menu")) close(false);
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && isOpen()) { e.preventDefault(); close(true); }
  });

  // Arrow keys move through the items once the menu is open.
  const move = (step) => {
    const list = items();
    if (!list.length) return;
    const i = list.indexOf(document.activeElement);
    list[(i + step + list.length) % list.length].focus();
  };
  btn.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); open(); items()[0]?.focus(); }
  });
  pop.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
    else if (e.key === "Home") { e.preventDefault(); items()[0]?.focus(); }
    else if (e.key === "End") { e.preventDefault(); items().at(-1)?.focus(); }
  });

  // ---- project link and update check ----
  // In the Windows app window there are no tabs: a link would navigate the program's own window away. So
  // there the program opens the (fixed, server-side) address in the system browser instead.
  const inApp = () => !!(window.gwApp && window.gwDrag);
  pop.addEventListener("click", (e) => {
    const a = e.target.closest("a[data-external]");
    if (a && inApp()) {
      e.preventDefault();
      api("/api/open/" + encodeURIComponent(a.dataset.external), { method: "POST" }).catch(() => {});
    }
  });

  const check = document.getElementById("update-check");
  const result = document.getElementById("update-result");
  function showResult(text, kind, link) {
    result.replaceChildren();
    result.className = "menu-note" + (kind ? " " + kind : "");
    result.hidden = !text;
    if (!text) return;
    result.append(text);
    if (link) result.append(h("a", { role: "menuitem", href: link, target: "_blank", rel: "noopener noreferrer", "data-external": "releases" }, "Open the release page"));
  }
  check.addEventListener("click", async () => {
    if (check.getAttribute("aria-busy") === "true") return;
    check.setAttribute("aria-busy", "true");
    showResult("Checking github.com…");
    try {
      const r = await api("/api/update-check", { method: "POST" });
      const link = typeof r.url === "string" && r.url.startsWith("https://github.com/") ? r.url : "";
      if (r.newer) showResult(`Version ${r.latest} is available. You have ${r.current}.`, "ok", link);
      else if (r.dev) showResult(`This is a development build. The newest release is ${r.latest}.`, "", link);
      else showResult(`You have the newest version (${r.current}).`, "ok");
    } catch (e) {
      showResult(e.message || "The update check failed.", "err");
    } finally {
      check.removeAttribute("aria-busy");
    }
  });

  // Show which version this is (useful in bug reports).
  api("/api/ping").then((p) => {
    if (p && p.version) about.textContent = "groundwλve " + p.version;
  }).catch(() => {});
}
