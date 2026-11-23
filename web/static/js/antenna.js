// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Antenna tools. The math lives on the server (internal/calc, unit-tested);
// this file only collects inputs and displays results.
import { h, api } from "./dom.js";

// ---- formatting ------------------------------------------------------------
function ftIn(feet) {
  const tenths = Math.round(feet * 120); // tenths of an inch
  const ft = Math.floor(tenths / 120);
  const inch = (tenths - ft * 120) / 10;
  return `${ft} ft ${inch.toFixed(1)} in`;
}
const len = (l) => `${ftIn(l.feet)}  (${l.feet.toFixed(2)} ft, ${l.meters.toFixed(2)} m)`;
const db = (v) => (v == null ? "∞ dB" : v.toFixed(2) + " dB");
const num = (v, digits = 2) => Number(v).toFixed(digits);

const FREQ = { name: "f", label: "Frequency", unit: "MHz", value: "14.2", step: "any", required: true };

// ---- calculator definitions -------------------------------------------------
// Each has: id, title, blurb, fields (or modes), render(result) -> [[label, value], ...], notes.
const CALCS = [
  {
    id: "dipole", title: "Dipole", endpoint: "dipole",
    blurb: "Half-wave wire dipole, fed in the centre.",
    fields: [FREQ],
    render: (r) => [["Total length", len(r.total)], ["Each leg", len(r.leg)], ["Free-space half wave (reference)", len(r.free_space)]],
    notes: ["Uses the common 468 / f(MHz) feet rule, which includes about 5% shortening from end effect.",
            "Cut a little long (2-3%) and trim for the lowest SWR with an analyzer.",
            "Feed with 50 ohm coax and a 1:1 choke or balun at the feedpoint. Height, ground and nearby objects shift the resonant length."],
  },
  {
    id: "vertical", title: "Vertical", endpoint: "vertical",
    blurb: "Quarter-wave ground-plane vertical with radials.",
    fields: [FREQ],
    render: (r) => [["Radiator", len(r.radiator)], ["Each radial", len(r.radial)]],
    notes: ["Radiator is 234 / f(MHz) feet. Radials are cut about 5% longer than the radiator; their length is not critical.",
            "Use at least four radials. More is better, especially close to the ground."],
  },
  {
    id: "jpole", title: "J-Pole", endpoint: "jpole",
    blurb: "Three-quarter-wave J-pole with a quarter-wave stub.",
    fields: [{ ...FREQ, value: "146.52" }, { name: "vf", label: "Velocity factor", unit: "", value: "0.95", step: "any" }],
    render: (r) => [["Overall length (3/4 wave)", len(r.overall)], ["Matching stub (1/4 wave)", len(r.stub)]],
    notes: ["A starting point only. Velocity factor is about 0.95 for 450 ohm ladder line and a little higher for copper pipe.",
            "Slide the feed point along the stub to tune for the lowest SWR."],
  },
  {
    id: "wavelength", title: "Wavelength", endpoint: "wavelength",
    blurb: "Free-space wavelength and common fractions.",
    fields: [FREQ],
    render: (r) => [["Wavelength", len(r.wavelength)], ...r.fractions.map((f) => [f.name, len(f.length)])],
    notes: ["Free-space values: real antennas are shorter (see the dipole and vertical tools)."],
  },
  {
    id: "swr", title: "SWR / Match", endpoint: "swr",
    blurb: "SWR, return loss and mismatch loss.",
    modes: [
      { id: "z", label: "From load impedance", fields: [
        { name: "z0", label: "Line impedance", unit: "ohms", value: "50", step: "any", required: true },
        { name: "r", label: "Load resistance (R)", unit: "ohms", value: "75", step: "any", required: true },
        { name: "x", label: "Load reactance (X)", unit: "ohms", value: "0", step: "any" } ] },
      { id: "s", label: "From SWR", fields: [{ name: "swr", label: "SWR", unit: ": 1", value: "2", step: "any", required: true }] },
      { id: "rl", label: "From return loss", fields: [{ name: "rl", label: "Return loss", unit: "dB", value: "10", step: "any", required: true }] },
    ],
    render: (r) => [
      ["SWR", r.swr == null ? "∞ : 1" : num(r.swr) + " : 1"],
      ["Return loss", db(r.return_loss_db)],
      ["Mismatch loss", r.mismatch_loss_db == null ? "∞ dB" : num(r.mismatch_loss_db, 3) + " dB"],
      ["Power reflected", num(r.reflected_pct, 1) + " %"],
      ["Reflection coefficient |Γ|", num(r.gamma, 4)],
    ],
    notes: ["Mismatch loss is the power lost to reflection at the load. Extra feedline loss from SWR is in the Feedline loss tool.",
            "Reactance (X) is positive for an inductive load and negative for a capacitive one."],
  },
  {
    id: "coax", title: "Feedline Loss", endpoint: "coax",
    blurb: "Loss in a cable run, including the extra loss caused by SWR.",
    fields: [
      { name: "loss", label: "Cable loss at your frequency", unit: "dB per 100 units", value: "1.5", step: "any", required: true },
      { name: "length", label: "Cable length", unit: "same units (ft or m)", value: "50", step: "any", required: true },
      { name: "swr", label: "SWR at the antenna", unit: ": 1", value: "1", step: "any" },
      { name: "power", label: "Transmitter power (optional)", unit: "W", value: "100", step: "any" } ],
    render: (r) => {
      const rows = [["Matched loss", num(r.matched_loss_db) + " dB"], ["Extra loss from SWR", num(r.extra_loss_db) + " dB"], ["Total loss", num(r.total_loss_db) + " dB"]];
      if (r.power_in_w) rows.push(["Power at the antenna", `${num(r.power_out_w, 1)} W  (${num(100 * r.power_out_w / r.power_in_w, 0)} % of ${num(r.power_in_w, 0)} W)`]);
      return rows;
    },
    notes: ["Enter the loss per 100 feet or per 100 metres from your cable's datasheet at the operating frequency, and the length in the same unit.",
            "Cable loss rises with frequency and with age and moisture. 3 dB of loss means half the power is gone."],
  },
  {
    id: "power", title: "Power, EIRP, ERP", endpoint: "power",
    blurb: "Convert power to dBm/dBW and compute EIRP and ERP.",
    fields: [
      { name: "tx", label: "Transmitter power", unit: "W", value: "100", step: "any", required: true },
      { name: "loss", label: "Feedline loss", unit: "dB", value: "0", step: "any" },
      { name: "gain", label: "Antenna gain", unit: "dBi", value: "2.15", step: "any" } ],
    render: (r) => [
      ["Transmitter power", `${num(r.tx_dbm, 1)} dBm  /  ${num(r.tx_dbw, 1)} dBW`],
      ["EIRP", `${num(r.eirp_w, 1)} W  (${num(r.eirp_dbm, 1)} dBm)`],
      ["ERP", num(r.erp_w, 1) + " W"],
      ["Antenna gain", `${num(r.gain_dbi)} dBi  =  ${num(r.gain_dbd)} dBd`],
    ],
    notes: ["EIRP is referenced to an isotropic radiator, ERP to a half-wave dipole (dBi = dBd + 2.15)."],
  },
];

// ---- view ----------------------------------------------------------------------
export function mount(root, args) {
  const current = CALCS.find((c) => c.id === args[0]) || CALCS[0];
  let timer = null;
  let seq = 0;
  let modeId = current.modes ? current.modes[0].id : null;

  const tabs = h("div", { class: "tabs", role: "tablist", "aria-label": "Calculators" },
    CALCS.map((c) => h("a", { href: "#/antenna/" + c.id, role: "tab", "aria-selected": String(c === current) }, c.title)));
  const form = h("form", { class: "calc-form", onsubmit: (e) => e.preventDefault() });
  const out = h("div", { class: "results", "aria-live": "polite" });
  const notes = h("ul", { class: "notes" }, current.notes.map((n) => h("li", {}, n)));

  function activeFields() {
    return current.modes ? current.modes.find((m) => m.id === modeId).fields : current.fields;
  }

  function buildForm() {
    form.replaceChildren();
    if (current.modes) {
      form.append(h("div", { class: "modes", role: "radiogroup", "aria-label": "Input" },
        current.modes.map((m) => h("label", {},
          h("input", { type: "radio", name: "mode", value: m.id, checked: m.id === modeId, onchange: () => { modeId = m.id; buildForm(); update(); } }),
          " " + m.label))));
    }
    for (const f of activeFields()) {
      form.append(h("label", { class: "field" },
        h("span", { class: "lbl" }, f.label),
        h("span", { class: "inp" },
          h("input", { type: "text", inputmode: "decimal", name: f.name, value: f.value, autocomplete: "off", oninput: schedule }),
          f.unit && h("span", { class: "unit" }, f.unit))));
    }
  }

  function schedule() { clearTimeout(timer); timer = setTimeout(update, 120); }

  async function update() {
    const params = new URLSearchParams();
    for (const f of activeFields()) {
      const v = form.elements[f.name].value.trim();
      if (v !== "") params.set(f.name, v);
      else if (f.required) { show(null, `Enter a value for ${f.label.toLowerCase()}.`); return; }
    }
    const mine = ++seq;
    try {
      const r = await api(`/api/calc/${current.endpoint}?${params}`);
      if (mine === seq) show(current.render(r));
    } catch (e) {
      if (mine === seq) show(null, e.message);
    }
  }

  function show(rows, error) {
    out.replaceChildren();
    if (error) { out.append(h("p", { class: "err" }, error)); return; }
    out.append(h("dl", {}, rows.map(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])));
  }

  buildForm();
  root.append(
    h("section", { class: "panel" },
      h("h1", { class: "sr-only" }, "Tools"),
      h("h2", {}, "Antenna Calculator"),
      tabs,
      h("h3", {}, current.title),
      h("p", { class: "hint" }, current.blurb),
      form, out, notes,
      h("p", { class: "hint" }, "These are rules of thumb and textbook formulas. Always confirm with an antenna analyzer or SWR meter before relying on a result.")),
  );
  update();
  return () => { clearTimeout(timer); seq++; };
}
