// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Shared internet-time (NTP) state. Polls the local server every few seconds;
// the server only talks to time servers when the user has switched it on or
// pressed "Check now".
import { api } from "./dom.js";

let state = { enabled: false };
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
    state = await api("/api/ntp");
    fetchedAt = Date.now();
  } catch (_) {
    state = { enabled: false, unavailable: true };
  }
  subs.forEach((fn) => fn(state));
  return state;
}

export function start() {
  refresh();
  setInterval(refresh, 5000);
}

// Milliseconds since the last check finished, or null if there has been none.
export function ageMs() {
  return state.age_ms == null ? null : state.age_ms + (Date.now() - fetchedAt);
}

// Internet time in milliseconds since the epoch, or null when there is no fresh
// reading. The reading is (server time - this computer's clock) when it was
// taken, so adding it to the clock now gives internet time.
export function ntpNow() {
  if (state.offset_ms == null) return null;
  const age = ageMs();
  const maxAge = (2 * (state.interval_s || 600) + 60) * 1000;
  if (age == null || age > maxAge) return null;
  return Date.now() + state.offset_ms;
}

export async function setEnabled(on) {
  await api("/api/ntp/settings", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ enabled: on }),
  });
  return refresh();
}

// Runs one check. Rejects with err.status 429 if asked too soon.
export async function checkNow() {
  const r = await api("/api/ntp/check", { method: "POST" });
  await refresh();
  return r;
}
