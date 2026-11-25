// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// The user's preferences (see internal/settings): loaded once, saved on every change, and
// announced to the parts of the page that care (the menu and the home page hide pages).
import { api } from "./dom.js";

let current = { check_updates_on_start: false, hidden_pages: [], callsign_launch: "off" };
let loading = null;
const subs = new Set();

function emit() { for (const fn of subs) { try { fn(current); } catch (_) { /* a broken listener must not stop the others */ } } }

export function get() { return current; }
export function hiddenPages() { return current.hidden_pages || []; }
export function subscribe(fn) { subs.add(fn); return () => subs.delete(fn); }

export function load() {
  if (!loading) loading = api("/api/settings").then((s) => { current = s; emit(); return s; }).catch(() => current);
  return loading;
}

// Saves some of the settings; the server checks them and answers with all of them.
export async function save(change) {
  current = await api("/api/settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(change) });
  emit();
  return current;
}
