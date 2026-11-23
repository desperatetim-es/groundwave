// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Tiny DOM helper. Text is always inserted as text nodes (never HTML), so
// user-supplied strings cannot inject markup.

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat(Infinity)) {
    if (c === false || c == null) continue;
    el.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return el;
}

export async function api(path, opts) {
  const resp = await fetch(path, opts);
  let body = null;
  try { body = await resp.json(); } catch (_) { /* no body */ }
  if (!resp.ok) {
    const err = new Error((body && body.error) || `The server answered ${resp.status}`);
    err.status = resp.status;
    err.body = body;
    throw err;
  }
  return body;
}
