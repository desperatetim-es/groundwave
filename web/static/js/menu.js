// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// The "?" menu in the header: a small popdown with the version and the license pages.
import { api } from "./dom.js";

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

  // Show which version this is (useful in bug reports).
  api("/api/ping").then((p) => {
    if (p && p.version) about.textContent = "groundwλve " + p.version;
  }).catch(() => {});
}
