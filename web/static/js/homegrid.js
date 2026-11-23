// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Your own grid square, shared by the Callsigns page and the Logbook. It is kept in this browser
// only (localStorage may be unavailable: never rely on it) and can come from the GPS.
import { current as gpsCurrent } from "./gpsstate.js";

const KEY = "groundwave.homeGrid";
export const GRID_RE = /^[A-Ra-r]{2}([0-9]{2}([A-Xa-x]{2}([0-9]{2})?)?)?$/;

export function getHomeGrid() { try { return localStorage.getItem(KEY) || ""; } catch (_) { return ""; } }
export function setHomeGrid(v) { try { localStorage.setItem(KEY, v); } catch (_) { /* ignore */ } }

// The grid square of the current GPS fix, or "" when there is none.
export function gpsGrid() {
  const g = gpsCurrent();
  return g && g.status === "fix" && g.grid ? g.grid : "";
}
