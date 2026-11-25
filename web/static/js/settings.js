// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Settings page (the gear in the header). Each choice is saved the moment it is changed. Everything that
// makes the program contact the internet by itself is off until switched on here.
import { h } from "./dom.js";
import * as prefs from "./prefs.js";
import * as net from "./ntpstate.js";

const PAGES = [["library", "Library"], ["antenna", "Tools"], ["callsigns", "Callsigns"], ["logbook", "Logbook"], ["clocks", "Time & Location"]];

export function mount(root) {
  const status = h("p", { class: "saved", "aria-live": "polite" });
  let timer = null;
  function say(text, bad) {
    status.textContent = text;
    status.className = "saved" + (bad ? " err" : "");
    clearTimeout(timer);
    if (text && !bad) timer = setTimeout(() => { status.textContent = ""; }, 2500);
  }
  // Runs a save, reports it, and puts the control back if the server refused.
  async function change(control, run, undo) {
    control.disabled = true;
    try { await run(); say("Saved."); } catch (e) { undo(); say(e.message || "That could not be saved.", true); } finally { control.disabled = false; }
  }

  // ---- updates ----
  const upd = h("input", { type: "checkbox", id: "set-updates" });
  upd.addEventListener("change", () => change(upd, () => prefs.save({ check_updates_on_start: upd.checked }), () => { upd.checked = !upd.checked; }));

  // ---- which pages to show ----
  const pageBoxes = PAGES.map(([key, title]) => {
    const box = h("input", { type: "checkbox", id: "set-page-" + key, "data-page": key });
    box.addEventListener("change", () => {
      const hidden = PAGES.map(([k]) => k).filter((k) => (k === key ? !box.checked : prefs.hiddenPages().includes(k)));
      change(box, () => prefs.save({ hidden_pages: hidden }), () => { box.checked = !box.checked; });
    });
    return { key, title, box };
  });

  // ---- internet time ----
  const ntp = h("input", { type: "checkbox", id: "set-ntp" });
  ntp.addEventListener("change", () => change(ntp, () => net.setEnabled(ntp.checked), () => { ntp.checked = !ntp.checked; }));

  // ---- callsign databases at launch ----
  const launchOpts = [
    ["off", "Do nothing when the program starts", ""],
    ["check", "Check whether a newer list is available", "Asks each publisher when its list was last changed (a small request; the list is not downloaded). The Callsigns page then says if a newer one exists."],
    ["update", "Check, and update any list that is out of date", "Downloads and rebuilds a list automatically when its publisher has a newer one. The US list is about 200 MB; Canada's is about 3 MB."],
  ].map(([value, label, hint]) => {
    const input = h("input", { type: "radio", name: "set-launch", id: "set-launch-" + value, value });
    input.addEventListener("change", () => {
      if (!input.checked) return;
      const before = prefs.get().callsign_launch;
      change(input, () => prefs.save({ callsign_launch: value }), () => { const old = launchOpts.find((o) => o.value === before); if (old) old.input.checked = true; });
    });
    return { value, label, hint, input };
  });

  function paint() {
    const p = prefs.get();
    upd.checked = !!p.check_updates_on_start;
    for (const { key, box } of pageBoxes) box.checked = !p.hidden_pages.includes(key);
    for (const o of launchOpts) o.input.checked = o.value === p.callsign_launch;
  }
  const stopPrefs = prefs.subscribe(paint);
  const stopNtp = net.subscribe((s) => { if (document.activeElement !== ntp) ntp.checked = !!s.enabled; });
  paint();
  prefs.load();

  const check = (input, label, hint) => h("div", { class: "setting" },
    h("label", {}, input, h("span", { class: "setting-title" }, label)), hint && h("p", { class: "hint" }, hint));

  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "page-title" }, "Settings"),
      h("p", { class: "hint" }, "Changes are saved as soon as you make them. groundwλve never contacts the internet by itself unless you turn one of these on."),
      status),
    h("section", { class: "panel" },
      h("h2", {}, "Updates"),
      check(upd, " Check for updates when groundwλve starts",
        "Off by default. When on, the program sends one anonymous request to GitHub a few seconds after it starts, and a small dot appears on the ? button if a newer version exists. Nothing is downloaded or installed. You can always press Check for updates in the ? menu instead.")),
    h("section", { class: "panel" },
      h("h2", {}, "Menu"),
      h("p", { class: "hint" }, "Untick the pages you do not use to take them out of the top menu and the home page. They are only hidden: a link to a hidden page still works, and nothing is deleted."),
      ...pageBoxes.map(({ title, box }) => check(box, " Show " + title))),
    h("section", { class: "panel" },
      h("h2", {}, "Internet time"),
      check(ntp, " Check internet time automatically every 10 minutes",
        "Off by default. Each check sends a small request to public time servers, which can see this computer's internet address. You can also press Check Now on the Time & Location page at any time.")),
    h("section", { class: "panel" },
      h("h2", {}, "Callsign databases at launch"),
      h("p", { class: "hint" }, "This only concerns lists you have already downloaded; a country is never downloaded for you. It takes effect the next time groundwλve starts."),
      ...launchOpts.map((o) => check(o.input, " " + o.label, o.hint))),
  );
  return () => { stopPrefs(); stopNtp(); clearTimeout(timer); };
}
