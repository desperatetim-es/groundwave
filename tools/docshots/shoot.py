# SPDX-License-Identifier: GPL-3.0-only
# Copyright (c) 2026 KD9PSM

"""Takes the screenshots for the user guide (docs/user-guide/img/*.png).

    py tools/docshots/shoot.py        (Windows)
    python3 tools/docshots/shoot.py   (Linux)

Needs Go, Chrome or Chromium, and the `websockets` Python package. Nothing here
touches your real data: it builds a throwaway data folder with made-up contacts,
documents and callsigns (tools/demodata), runs the app against it on port 47399,
and drives Chrome headless over the DevTools protocol.

There is no GPS receiver in the loop. The app's built-in simulator (--gps-sim)
stands in for one, and the page's requests are rewritten on the way through so the
simulator shows up as "COM3" at 9600 baud, as a real USB receiver would. The
internet-time panel is faked the same way, so nothing is sent to any time server.
"""
import base64, json, os, shutil, subprocess, sys, tempfile, time, urllib.request
from pathlib import Path
from websockets.sync.client import connect

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "user-guide" / "img"
PORT, CDP = 47399, 9333
W = 1200

CHROME = next((p for p in [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
] if os.path.exists(p)), None)


# ---- what the faked GPS and internet-time pages say ---------------------------------------
GPS_ON = False  # the GPS/internet comparison note only makes sense once a receiver is connected


def fake_ntp():
    d = {"enabled": False, "interval_s": 600, "checking": False, "age_ms": 12000, "offset_ms": 38.0,
            "spread_ms": 11.0, "answered": 3,
            "servers": ["time.cloudflare.com", "time.nist.gov", "pool.ntp.org"],
            "results": [{"server": "time.cloudflare.com", "ok": True, "offset_ms": 36.2, "delay_ms": 21.4, "stratum": 3},
                        {"server": "time.nist.gov", "ok": True, "offset_ms": 41.0, "delay_ms": 48.9, "stratum": 1},
                        {"server": "pool.ntp.org", "ok": True, "offset_ms": 37.5, "delay_ms": 33.0, "stratum": 2}],
            }
    if GPS_ON:
        d["vs_gps"] = {"gps_minus_ntp_ms": -38.0, "level": "agree",
                       "note": "GPS time and internet time agree, within the GPS receiver's timing accuracy."}
    return d


def fake_callsign_status():
    """The demo database has 8 licenses; the page should look like a real, full one."""
    return {"available": True, "records": 822155, "age_days": 3, "built_at": "2026-11-19T10:00:00Z",
            "source_modified": "Sun, 16 Nov 2026 14:08:58 GMT", "update": {"running": False}}


def fix_ports(d):
    for p in d.get("ports", []):
        if p["name"] == "simulator":
            p.update(name="COM3", description="USB Serial Device (u-blox 7 GPS receiver)", usb=True)
    return d


def fix_gps(d):
    if d.get("port") == "simulator":
        d["port"], d["baud"] = "COM3", 9600
    return d


# ---- a tiny DevTools client -----------------------------------------------------------------
class Chrome:
    def __init__(self, ws_url):
        self.ws, self.n = connect(ws_url, max_size=None, legacy=True), 0

    def send(self, method, **params):
        self.n += 1
        self.ws.send(json.dumps({"id": self.n, "method": method, "params": params}))
        return self.n

    def call(self, method, **params):
        mid = self.send(method, **params)
        while True:
            m = json.loads(self.ws.recv())
            if m.get("method") == "Fetch.requestPaused":
                self.paused(m["params"])
            elif m.get("id") == mid:
                if "error" in m:
                    raise RuntimeError(f"{method}: {m['error']}")
                return m.get("result", {})

    def pump(self, seconds):
        """Wait, serving intercepted requests (the page keeps polling while we wait)."""
        end = time.time() + seconds
        while time.time() < end:
            try:
                m = json.loads(self.ws.recv(timeout=max(0.05, end - time.time())))
            except TimeoutError:
                break
            if m.get("method") == "Fetch.requestPaused":
                self.paused(m["params"])

    def paused(self, p):
        rid, req = p["requestId"], p["request"]
        path = req["url"].split(str(PORT), 1)[1].split("?")[0]
        try:
            if "responseStatusCode" not in p and "responseErrorReason" not in p:  # request stage
                if path == "/api/ntp" and req["method"] == "GET":
                    return self.fulfill(rid, 200, json.dumps(fake_ntp()))
                if path == "/api/callsigns/status":
                    return self.fulfill(rid, 200, json.dumps(fake_callsign_status()))
                if path == "/api/gps/connect" and req.get("postData"):
                    body = req["postData"].replace("COM3", "simulator")
                    return self.send("Fetch.continueRequest", requestId=rid,
                                     postData=base64.b64encode(body.encode()).decode())
                return self.send("Fetch.continueRequest", requestId=rid)
            # response stage: fetch the body with a fire-and-wait of our own
            mid = self.send("Fetch.getResponseBody", requestId=rid)
            while True:
                m = json.loads(self.ws.recv())
                if m.get("id") == mid:
                    break
                if m.get("method") == "Fetch.requestPaused":
                    self.paused(m["params"])
            body = base64.b64decode(m["result"]["body"]).decode()
            fix = {"/api/gps": fix_gps, "/api/gps/ports": fix_ports}.get(path)
            if fix and req["method"] == "GET":
                body = json.dumps(fix(json.loads(body)))
            self.fulfill(rid, p["responseStatusCode"], body)
        except Exception as e:  # never leave a request hanging
            print("intercept problem:", path, e, file=sys.stderr)
            self.send("Fetch.continueRequest", requestId=rid)

    def fulfill(self, rid, status, body):
        self.send("Fetch.fulfillRequest", requestId=rid, responseCode=status,
                  body=base64.b64encode(body.encode()).decode(),
                  responseHeaders=[{"name": "Content-Type", "value": "application/json"},
                                   {"name": "Cache-Control", "value": "no-store"}])

    def js(self, expr):
        r = self.call("Runtime.evaluate", expression=expr, awaitPromise=True, returnByValue=True)
        if "exceptionDetails" in r:
            raise RuntimeError(r["exceptionDetails"]["exception"].get("description", str(r)))
        return r["result"].get("value")

    def go(self, route, wait=1.2):
        self.js(f"location.hash = {json.dumps(route)}")
        self.pump(wait)

    def type(self, selector, text, wait=0.6):
        self.js(f"""(() => {{ const e = document.querySelector({json.dumps(selector)}); e.focus(); e.value = {json.dumps(text)};
                    e.dispatchEvent(new Event('input', {{bubbles: true}})); }})()""")
        self.pump(wait)

    def click(self, text, wait=0.8):
        self.js(f"""(() => {{ const b = [...document.querySelectorAll('button,a.btn')]
                    .find(x => x.textContent.trim() === {json.dumps(text)}); b.click(); }})()""")
        self.pump(wait)

    def shot(self, name, height=None, crop=None):
        """Screenshot of the whole page, 1200 px wide, or of crop=(top, bottom) pixels of it."""
        # no height given: the page's content plus a margin, not the (taller) window
        h = height or int(self.js("""Math.ceil(Math.max(...[...document.querySelectorAll('main > *, .panel, .tiles')]
            .map(e => e.getBoundingClientRect().bottom + scrollY)) + 24)"""))
        self.call("Emulation.setDeviceMetricsOverride", width=W, height=h, deviceScaleFactor=1, mobile=False)
        self.pump(0.4)
        params = {"format": "png"}
        if crop:
            params["clip"] = {"x": 0, "y": crop[0], "width": W, "height": crop[1] - crop[0], "scale": 1}
        data = self.call("Page.captureScreenshot", **params)["data"]
        (OUT / f"{name}.png").write_bytes(base64.b64decode(data))
        print("  wrote", name)

    def top_of(self, heading):
        """Page y of the panel whose heading (h2) reads `heading`."""
        return int(self.js(f"""[...document.querySelectorAll('.panel')].find(p => (p.querySelector('h2') || {{}}).textContent === {json.dumps(heading)})
                              .getBoundingClientRect().top + scrollY - 16"""))


# ---- sample content -------------------------------------------------------------------------
def tiny_pdf(title, lines):
    """A one-page PDF with a few lines of text, written by hand (no libraries)."""
    esc = lambda s: s.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")
    stream = ("BT /F1 20 Tf 72 720 Td (" + esc(title) + ") Tj /F1 12 Tf 0 -34 Td 18 TL "
              + " ".join("(" + esc(x) + ") Tj T*" for x in lines) + " ET")
    objs = ["<</Type/Catalog/Pages 2 0 R>>", "<</Type/Pages/Kids[3 0 R]/Count 1>>",
            "<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
            f"<</Length {len(stream)}>>\nstream\n{stream}\nendstream", "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>"]
    out, offs = b"%PDF-1.4\n", []
    for i, o in enumerate(objs, 1):
        offs.append(len(out))
        out += f"{i} 0 obj\n{o}\nendobj\n".encode()
    x = len(out)
    out += f"xref\n0 {len(objs) + 1}\n0000000000 65535 f \n".encode() + b"".join(f"{o:010d} 00000 n \n".encode() for o in offs)
    return out + f"trailer<</Size {len(objs) + 1}/Root 1 0 R>>\nstartxref\n{x}\n%%EOF\n".encode()


DOCS = [
    ("Sample - Net Check-In Script.pdf", "Net Check-In Script (sample)", ["net", "procedures"],
     ["This is a made-up sample document so the library is not empty.",
      "Net control: 'This is the weekly net. Stations with traffic, call now.'",
      "Check-in: give your call sign slowly, phonetically, once."]),
    ("Sample - Field Day Packing List.pdf", "Field Day Packing List (sample)", ["field day", "checklist"],
     ["Antenna, coax, tools, tent, tarp, chairs, power strip, logging laptop.",
      "Sample document: replace with your own files."]),
    ("Sample - Antenna Notes.pdf", "Antenna Notes (sample)", ["antenna", "notes"],
     ["A half-wave dipole is about 468 / frequency in MHz feet long.",
      "Sample document: replace with your own files."]),
]

# date, time (UTC), call, MHz, mode, sent, received, name, place, extra fields
QSOS = [
    ("20261114", "151200", "K9DEMO", 14.074, "FT8", "-07", "-11", "", "", {}),
    ("20261115", "003000", "W7DEMO", 7.030, "CW", "579", "559", "Jordan", "Portland",
     {"grid": "CN85", "grid_source": "user", "pota_ref": "US-1234", "comment": "Park to park, loud signal."}),
    ("20261118", "194500", "W0DEMO", 14.255, "SSB", "59", "57", "Morgan", "Denver", {}),
    ("20261120", "142000", "K2DEMO", 21.074, "FT8", "-12", "-15", "Robin", "New York", {}),
    ("20261121", "230500", "K0DEMO", 146.52, "FM", "59", "59", "Alex", "Lawrence", {"comment": "Simplex, across town."}),
    ("20261121", "233000", "W1AW", 18.100, "RTTY", "599", "599", "", "Newington", {}),
]


def main():
    if not CHROME:
        sys.exit("Chrome or Chromium was not found.")
    OUT.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="gw-shots-"))
    data, exe = work / "data", work / ("gw.exe" if os.name == "nt" else "gw")
    subprocess.run(["go", "build", "-o", str(exe), "./cmd/groundwave"], cwd=ROOT, check=True)
    subprocess.run(["go", "run", "./tools/demodata", "-data", str(data)], cwd=ROOT, check=True)
    app = subprocess.Popen([str(exe), "--data", str(data), "--port", str(PORT), "--no-browser", "--gps-sim"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    chrome = subprocess.Popen([CHROME, "--headless=new", f"--remote-debugging-port={CDP}",
                               f"--user-data-dir={work / 'chrome'}", "--hide-scrollbars",
                               "--force-device-scale-factor=1", f"--window-size={W},900", "about:blank"],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        page = None
        for _ in range(100):
            try:
                tabs = json.load(urllib.request.urlopen(f"http://127.0.0.1:{CDP}/json"))
                page = next(t for t in tabs if t["type"] == "page")
                urllib.request.urlopen(f"http://127.0.0.1:{PORT}/api/ping")
                break
            except Exception:
                time.sleep(0.2)
        c = Chrome(page["webSocketDebuggerUrl"])
        c.call("Page.enable")
        c.call("Emulation.setEmulatedMedia", features=[{"name": "prefers-color-scheme", "value": "light"}])
        c.call("Fetch.enable", patterns=[
            {"urlPattern": f"*:{PORT}/api/gps*", "requestStage": "Request"},
            {"urlPattern": f"*:{PORT}/api/gps*", "requestStage": "Response"},
            {"urlPattern": f"*:{PORT}/api/ntp", "requestStage": "Request"},
            {"urlPattern": f"*:{PORT}/api/callsigns/status", "requestStage": "Request"}])
        c.call("Page.navigate", url=f"http://127.0.0.1:{PORT}/")
        c.pump(2)

        print("time and location, no receiver yet")
        c.go("#/clocks", 2.0)
        c.js("[...document.querySelectorAll('details.fold')].find(d => d.querySelector('summary').textContent === 'Receiver Settings').open = true")
        c.pump(0.3)
        c.shot("time-before")
        print("connecting the simulated receiver (shown as COM3)")
        c.js("document.querySelector('#port').value = 'COM3'")
        global GPS_ON
        GPS_ON = True
        c.click("Connect", 6.0)
        c.js("document.querySelectorAll('details.fold').forEach(d => d.open = false)")
        c.pump(0.3)
        c.shot("time-gps")                            # time, position, and the folded sections
        # the folded sections, opened: GPS receiver status, satellites and receiver settings
        c.js("document.querySelectorAll('details.fold').forEach(d => { if (d.querySelector('summary').textContent !== 'Internet Time (NTP)') d.open = true })")
        c.pump(0.3)
        split = int(c.js("document.querySelector('details.fold').getBoundingClientRect().top + scrollY - 16"))
        end = int(c.js("document.documentElement.scrollHeight"))
        c.shot("time-gps-details", crop=(split, end - 24))
        c.js("document.querySelectorAll('details.fold').forEach(d => d.open = false)")
        c.shot("header-clock", height=64)

        print("home")
        c.go("#/", 1.0)
        c.shot("home")

        print("library")
        for fname, title, tags, lines in DOCS:
            b64 = base64.b64encode(tiny_pdf(title, lines)).decode()
            c.js(f"""(async () => {{ const bin = Uint8Array.from(atob('{b64}'), x => x.charCodeAt(0));
                const fd = new FormData(); fd.append('file', new Blob([bin], {{type: 'application/pdf'}}), {json.dumps(fname)});
                const r = await fetch('/api/docs', {{method: 'POST', body: fd}}); const d = await r.json();
                await fetch('/api/docs/' + d.id, {{method: 'PATCH', headers: {{'Content-Type': 'application/json'}},
                    body: JSON.stringify({{title: {json.dumps(title)}, tags: {json.dumps(tags)}}})}});
                return d.id; }})()""")
        c.go("#/library", 1.5)
        c.shot("library")
        c.type("input[type=search]", "antenna", 1.0)
        c.shot("library-search")

        print("tools")
        c.go("#/antenna/dipole", 1.0)
        c.shot("antenna-dipole")
        c.go("#/antenna/swr", 1.0)
        c.shot("antenna-swr")
        c.go("#/antenna/coax", 1.0)
        c.type("input[name=swr]", "2.5", 0.8)
        c.shot("antenna-feedline")

        print("callsigns")
        c.go("#/callsigns", 1.5)
        c.click("Use GPS", 1.0)
        c.type("#cs-q", "sample", 1.2)
        c.shot("callsigns")

        print("logbook")
        for d, t, call, mhz, mode, s, r, name, qth, extra in QSOS:
            body = {"call": call, "date": d, "time": t, "freq_mhz": mhz, "mode": mode, "rst_sent": s,
                    "rst_rcvd": r, "name": name, "qth": qth, **extra}
            code = c.js(f"fetch('/api/logbook', {{method: 'POST', headers: {{'Content-Type': 'application/json'}}, "
                        f"body: JSON.stringify({json.dumps(body)})}}).then(r => r.status)")
            if code not in (200, 201):
                print("  logbook add returned", code)
        c.go("#/logbook", 1.5)
        c.type("#lb-call", "K9DEMO", 1.5)
        c.type("input[aria-label='Frequency in MHz']", "14.074", 0.3)
        c.type("input[aria-label='Mode']", "FT8", 0.3)
        c.type("input[aria-label='Signal report sent']", "-07", 0.3)
        c.type("input[aria-label='Signal report received']", "-11", 0.8)
        c.shot("logbook")
        print("done:", OUT)
    finally:
        chrome.terminate()
        app.terminate()
        time.sleep(0.5)
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
