// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Shared GPS state. Polls the server once a second; the header clock and the
// GPS page both read from here.
import { api } from "./dom.js";

const STALE_MS = 5000;
let state = { status: "stopped" };
let fetchedAt = 0;
const subs = new Set();

export function current() { return state; }

export function subscribe(fn) {
  subs.add(fn);
  fn(state);
  return () => subs.delete(fn);
}

export async function refresh() {
  try {
    state = await api("/api/gps");
    fetchedAt = Date.now();
  } catch (_) {
    state = { status: "unavailable" };
  }
  subs.forEach((fn) => fn(state));
  return state;
}

export function start() {
  refresh();
  setInterval(refresh, 1000);
}

// GPS time in milliseconds since the epoch, or null when there is no current fix.
// offset_ms is (GPS time - system clock) measured on the server, so adding it to
// this computer's clock gives GPS time.
export function gpsNow() {
  if (state.status !== "fix" || state.offset_ms == null) return null;
  const ageNow = (state.age_ms || 0) + (Date.now() - fetchedAt);
  if (ageNow > STALE_MS) return null;
  return Date.now() + state.offset_ms;
}

// Short text for the header when there is no GPS time to show.
export function shortNote() {
  switch (state.status) {
    case "fix": return gpsNow() == null ? "signal lost" : (state.correction_ms ? "leap-corrected" : "");
    case "acquiring": return "searching";
    case "no_data": return "no data";
    case "connecting": return "connecting";
    case "error": return "receiver problem";
    case "unavailable": return "unavailable";
    default: return "no receiver";
  }
}

export const hms = (ms) => new Date(ms).toISOString().slice(11, 19);
export const dateTime = (ms) => new Date(ms).toISOString().replace("T", " ").slice(0, 19);
