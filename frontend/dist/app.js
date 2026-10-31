"use strict";

const $ = (s) => document.querySelector(s);
const $$ = (s) => document.querySelectorAll(s);
const api = () => window.go.main.App;

let snap = null;
let servers = [];
let latency = {};
let openCountry = null;
let timerHandle = null;
let lastStats = { up: 0, down: 0, upRate: 0, downRate: 0 };
let addingAccount = false;
let lastQuotaRefresh = 0;
let installedApps = null; // cached GetApps result
let appMode = "all";      // pending per-app tunnel mode
let selectedApps = [];    // pending selection (package ids)
let usageData = null;     // last GetUsage result (refreshed live while open)
let usageTimer = null;    // usage sheet refresh interval
let usageApps = null;     // last GetAppUsage result (Windows)
let expandedApp = null;   // the app whose destinations are shown

const GLOBE = '<svg viewBox="0 0 24 24"><path d="M12 2l2.4 5.6L20 8.3l-4.3 4 1.2 6L12 15.4 7.1 18.3l1.2-6L4 8.3l5.6-.7Z"/></svg>';
const CHEVRON = '<svg viewBox="0 0 24 24"><path d="m9 6 6 6-6 6"/></svg>';

// ---------- helpers ----------
function fmtBytes(n) {
  if (!n || n < 0) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  // Keep the fractional part visible: MB 1 decimal, GB/TB 2 decimals
  // (trailing zeros trimmed, e.g. 10.24 GB / 3.5 GB / 1 GB).
  if (i === 0) return Math.round(n) + " B";
  if (i === 1) return n.toFixed(n < 10 ? 1 : 0) + " KB";
  if (i === 2) return String(+n.toFixed(1)) + " MB";
  return String(+n.toFixed(2)) + " " + u[i];
}
function fmtDuration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = String(Math.floor(s / 3600)).padStart(2, "0");
  const m = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
  const sec = String(s % 60).padStart(2, "0");
  return `${h}:${m}:${sec}`;
}
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function flagHTML(code) {
  if (!code || code === "REC") return GLOBE;
  return "";
}
function setFlag(el, code) {
  if (!code || code === "REC") {
    el.style.backgroundImage = "";
    el.innerHTML = GLOBE;
  } else {
    el.innerHTML = "";
    el.style.backgroundImage = `url(flags/${code.toLowerCase()}.svg)`;
  }
}
// Colors are relative to the fastest server: absolute numbers vary wildly
// between networks (filtered networks often add 500+ ms to every handshake).
function pingClass(ms) {
  if (ms == null) return "";
  if (ms < 0) return "bad";
  const vals = Object.values(latency).filter((v) => v >= 0);
  const best = vals.length ? Math.min(...vals) : ms;
  if (ms <= Math.max(150, best * 1.5)) return "good";
  return ms <= Math.max(300, best * 3) ? "ok" : "bad";
}
function pingText(ms) {
  if (ms == null) return "";
  return ms < 0 ? "timeout" : ms + " ms";
}
function errText(e) {
  return String(e && e.message ? e.message : e).replace(/^Error:\s*/, "");
}
let toastTimer;
function toast(msg, ms = 3200) {
  const t = $("#toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), ms);
}
function setLoading(btn, on) {
  btn.disabled = on;
  btn.classList.toggle("loading", on);
}
function showView(id) {
  for (const v of $$(".view")) v.hidden = v.id !== id;
}

// ---------- in-app confirm (WebView2's native confirm() renders off-frame) ----------
let confirmResolve = null;
function askConfirm(message, title = "Are you sure?") {
  $("#confirm-title").textContent = title;
  $("#confirm-message").textContent = message;
  $("#modal-confirm").hidden = false;
  return new Promise((ok) => { confirmResolve = ok; });
}

// ---------- sheets & modals ----------
function openSheet(id) {
  $("#scrim").hidden = false;
  for (const s of $$(".sheet")) s.hidden = s.id !== id;
}
function closeSheets() {
  const appsOpen = !$("#sheet-apps").hidden;
  $("#scrim").hidden = true;
  for (const s of $$(".sheet")) s.hidden = true;
  clearInterval(usageTimer);
  usageTimer = null;
  // "Selected only"/"Except selected" need at least one app; an empty
  // selection reverts to tunnelling everything.
  if (appsOpen && appMode !== "all" && !selectedApps.length) {
    appMode = "all";
    saveAppSettings();
  }
}

// ---------- rendering ----------
function locationName() {
  const s = snap.settings;
  const cc = s.country_code;
  if (!cc || cc === "REC") return { code: "REC", name: "Recommended", sub: "" };
  const c = servers.find((x) => x.code === cc);
  const city = c && c.cities.find((x) => x.code === s.city_code);
  return { code: cc, name: c ? c.name : cc, sub: city ? city.name : "Any city" };
}

function fmtDate(ms) {
  return ms ? new Date(ms).toLocaleDateString("en-US", { month: "short", day: "numeric" }) : "";
}

function renderAccounts() {
  const list = $("#account-list");
  const html = (snap.accounts || []).map((a) => {
    const initial = (a.email || "Z")[0].toUpperCase();
    let line, cls = "", pct = 0;
    if (a.needsLogin) {
      line = "Sign-in expired · remove and add it again";
      cls = "bad";
    } else if (a.exhausted) {
      line = "Out of data" + (a.quotaReset ? ` · resets ${fmtDate(a.quotaReset)}` : "");
      cls = "warn";
      pct = 100;
    } else if (a.quotaLimit > 0) {
      line = `${fmtBytes(a.quotaLeft)} left of ${fmtBytes(a.quotaLimit)}` + (a.quotaReset ? ` · resets ${fmtDate(a.quotaReset)}` : "");
      pct = Math.min(100, ((a.quotaLimit - a.quotaLeft) / a.quotaLimit) * 100);
    } else {
      line = "Checking data…";
    }
    const action = a.active
      ? '<span class="acct-badge">Active</span>'
      : `<button class="acct-use" data-use="${esc(a.email)}">Use</button>`;
    return `<div class="acct ${a.active ? "active" : ""}">
      <span class="avatar">${esc(initial)}</span>
      <span class="acct-body"><b>${esc(a.email || "Mozilla account")}</b><small class="${cls}">${esc(line)}</small>
        ${a.quotaLimit > 0 ? `<span class="mini-bar"><i style="width:${pct.toFixed(1)}%"></i></span>` : ""}</span>
      ${action}
      <button class="icon-btn" data-remove="${esc(a.email)}" title="Sign out of this account"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>
    </div>`;
  });
  list.innerHTML = html.join("");
}

const AUTOSTART_LABEL = { windows: "Start with Windows", macos: "Open at login", linux: "Start at login" };

// Hide controls that don't exist on this platform (the mobile apps only
// have Full VPN mode and no window buttons, and so on).
function applyPlatform() {
  const f = snap.features || {};
  document.documentElement.dataset.platform = snap.platform || "windows";
  $(".win-buttons").hidden = !f.windowControls;
  $("#mode-switch").hidden = !f.proxyMode;
  $("#set-killswitch").closest(".toggle-row").hidden = !f.killSwitch;
  $("#set-autostart").closest(".toggle-row").hidden = !f.autostart;
  $("#set-autostart").closest(".toggle-row").querySelector("b").textContent = AUTOSTART_LABEL[snap.platform] || "Start at login";
  $("#btn-logfile").hidden = !f.logFile;
  $("#group-protection").hidden = !f.killSwitch;
  $("#group-appfilter").hidden = !f.appFilter;
  $("#app-filter").hidden = !f.appFilter;
}

function render() {
  if (!snap) return;
  applyPlatform();
  renderAccounts();
  if (snap.loggedIn && addingAccount) {
    // The web sign-in finished successfully — return to the app instead of
    // staying on the login view.
    addingAccount = false;
  }
  if (!snap.loggedIn || addingAccount) {
    $("#login-cancel").hidden = !snap.loggedIn;
    $("#login-subtitle").textContent = snap.loggedIn ? "Add another Mozilla account" : "Sign in with your Mozilla account";
    showView("view-login");
    return;
  }
  showView("view-home");
  const st = snap.status;
  document.body.classList.toggle("connected", st === "connected");
  document.body.classList.toggle("reconnecting", st === "reconnecting");

  const accounts = snap.accounts || [];
  const email = snap.email || "Mozilla account";
  $("#account-email").textContent = email;
  $("#avatar").textContent = (snap.email || "Z")[0].toUpperCase();
  const count = $("#acct-count");
  count.hidden = accounts.length < 2;
  count.textContent = "+" + (accounts.length - 1);

  const mode = snap.settings.mode;
  $("#mode-switch").classList.toggle("tun", mode === "tun");
  for (const b of $$("#mode-switch button")) b.classList.toggle("active", b.dataset.mode === mode);

  const orb = $("#orb");
  orb.dataset.status = st;
  const texts = {
    disconnected: ["Not connected", "Tap to connect"],
    connecting: ["Connecting…", "Securing your connection"],
    connected: ["Connected", ""],
    disconnecting: ["Disconnecting…", ""],
    reconnecting: ["Reconnecting…", snap.settings.kill_switch ? "Kill switch is blocking the internet" : "Connection lost, retrying"],
  };
  $("#status-text").textContent = texts[st][0];
  clearInterval(timerHandle);
  if (st === "connected") {
    const tick = () => ($("#status-sub").textContent = fmtDuration(Date.now() - snap.connectedAt));
    tick();
    timerHandle = setInterval(tick, 1000);
  } else {
    $("#status-sub").textContent = texts[st][1];
  }

  // Location card: show the actual server when connected.
  const loc = locationName();
  let flagCode = loc.code, name = loc.name, sub = loc.sub;
  if (st === "connected" && snap.server && snap.server.countryCode !== "REC") {
    flagCode = snap.server.countryCode;
    name = snap.server.countryName;
    sub = snap.server.cityName;
  } else if (st === "connected" && snap.exitCountry) {
    flagCode = snap.exitCountry;
    sub = "Recommended · exit in " + snap.exitCountry;
  }
  setFlag($("#loc-flag"), flagCode);
  $("#loc-name").textContent = sub && sub !== name ? `${name} · ${sub}` : name;
  const key = snap.settings.country_code && snap.settings.city_code ? `${snap.settings.country_code}/${snap.settings.city_code}` : null;
  const ping = key ? latency[key] : null;
  const lp = $("#loc-ping");
  lp.className = "ping " + pingClass(ping);
  lp.textContent = pingText(ping);
  lp.hidden = ping == null;

  $("#exit-ip").textContent = st === "connected" ? (snap.exitIp ? `${snap.exitIp}` : "checking…") : "—";
  // Quota of the active account (live while connected, last known otherwise).
  const active = accounts.find((a) => a.active) || {};
  const limit = snap.quotaLimit || active.quotaLimit || 0;
  const left = snap.quotaLimit ? snap.quotaLeft : active.quotaLeft || 0;
  if (limit > 0) {
    $("#quota-text").textContent = `${fmtBytes(left)} left of ${fmtBytes(limit)}`;
    $("#quota-fill").style.width = Math.min(100, ((limit - left) / limit) * 100).toFixed(1) + "%";
  } else {
    $("#quota-text").textContent = "—";
    $("#quota-fill").style.width = "0";
  }
  const known = accounts.filter((a) => a.quotaLimit > 0);
  $("#total-row").hidden = accounts.length < 2 || !known.length;
  if (known.length) {
    const totalLeft = known.reduce((s, a) => s + (a.exhausted ? 0 : a.quotaLeft), 0);
    const totalLimit = known.reduce((s, a) => s + a.quotaLimit, 0);
    $("#total-text").textContent = `${fmtBytes(totalLeft)} left of ${fmtBytes(totalLimit)}`;
  }

  const banner = $("#banner");
  if (st === "reconnecting" && snap.settings.kill_switch) {
    banner.className = "banner info";
    banner.innerHTML = 'Kill switch is blocking the internet until the VPN reconnects.<button id="banner-disconnect">Disconnect</button>';
    banner.hidden = false;
    $("#banner-disconnect").onclick = () => api().Disconnect();
  } else if (snap.error) {
    banner.className = "banner";
    banner.textContent = snap.error;
    banner.hidden = false;
  } else if (mode === "tun" && !snap.elevated && st === "disconnected") {
    banner.className = "banner info";
    banner.innerHTML = 'Full VPN needs administrator rights.<button id="banner-elevate">Restart as admin</button>';
    banner.hidden = false;
    $("#banner-elevate").onclick = elevate;
  } else {
    banner.hidden = true;
  }

  $("#set-bypass-ir").checked = snap.settings.bypass_iran;
  $("#set-bypass-lan").checked = snap.settings.bypass_lan;
  $("#set-autoconnect").checked = snap.settings.auto_connect;
  $("#set-autoswitch").checked = snap.settings.auto_switch;
  $("#set-killswitch").checked = snap.settings.kill_switch;
  $("#set-tls-split").value = snap.settings.tls_split || "auto";
  $("#set-autostart").checked = snap.autostart;
  // Per-app tunnelling (Android): reflect the saved mode and selection.
  appMode = snap.settings.app_tunnel_mode || "all";
  selectedApps = (snap.settings.apps || []).slice();
  for (const b of $$("#set-apptunnel button")) b.classList.toggle("active", b.dataset.mode === appMode);
  $("#apps-count").textContent = selectedApps.length ? `· ${selectedApps.length}` : "";
  if (document.activeElement !== $("#set-port")) $("#set-port").value = snap.settings.proxy_port;
  $("#about").innerHTML = `Zeus VPN ${esc(snap.version)} · local proxy ${esc(snap.proxyAddr || "127.0.0.1:" + snap.settings.proxy_port)}<br>Powered by Mozilla VPN · <a href="#" id="link-github">GitHub</a>`;
  $("#link-github").onclick = (e) => { e.preventDefault(); api().OpenURL("https://github.com/Khode-ZeUS/Zeus-VPN"); };
}

function renderStats() {
  $("#rate-down").textContent = fmtBytes(lastStats.downRate) + "/s";
  $("#rate-up").textContent = fmtBytes(lastStats.upRate) + "/s";
  $("#total-down").textContent = "Download · " + fmtBytes(lastStats.down);
  $("#total-up").textContent = "Upload · " + fmtBytes(lastStats.up);
  // Live remaining data: the server's number minus what this connection
  // moved since the last refresh, so sharing the account elsewhere is the
  // only thing that changes it in between.
  if (lastStats.quotaLimit > 0) {
    $("#quota-text").textContent = `${fmtBytes(lastStats.quotaLeft)} left of ${fmtBytes(lastStats.quotaLimit)}`;
    $("#quota-fill").style.width = Math.min(100, ((lastStats.quotaLimit - lastStats.quotaLeft) / lastStats.quotaLimit) * 100).toFixed(1) + "%";
  }
}

function renderServers() {
  const list = $("#server-list");
  const q = $("#server-search").value.trim().toLowerCase();
  const sel = snap.settings;
  const selCountry = sel.country_code || "REC";
  if (!servers.length) {
    list.innerHTML = '<div class="list-msg">Loading servers…</div>';
    return;
  }
  let html = "";
  for (const c of servers) {
    const isRec = c.code === "REC";
    const name = isRec ? "Recommended" : c.name;
    const cityMatch = c.cities.filter((x) => !q || x.name.toLowerCase().includes(q));
    if (q && !name.toLowerCase().includes(q) && !cityMatch.length) continue;
    const pings = c.cities.map((x) => latency[`${c.code}/${x.code}`]).filter((v) => v != null && v >= 0);
    const best = pings.length ? Math.min(...pings) : (c.cities.some((x) => latency[`${c.code}/${x.code}`] === -1) ? -1 : null);
    const open = openCountry === c.code || (q && cityMatch.length && !isRec);
    const selected = selCountry === c.code && (!sel.city_code || isRec);
    const sub = isRec ? "Fastest available server" : `${c.cities.length} ${c.cities.length === 1 ? "city" : "cities"}`;
    html += `<div class="srv-group">
      <button class="srv ${selected ? "selected" : ""} ${open ? "open" : ""}" data-country="${esc(c.code)}">
        <span class="flag" ${isRec ? "" : `style="background-image:url(flags/${esc(c.code.toLowerCase())}.svg)"`}>${flagHTML(c.code)}</span>
        <span class="name"><b>${esc(name)}</b><small>${esc(sub)}</small></span>
        ${best != null ? `<span class="ping ${pingClass(best)}">${pingText(best)}</span>` : ""}
        ${!isRec && c.cities.length > 1 ? `<span class="expand" data-expand="${esc(c.code)}">${CHEVRON}</span>` : ""}
      </button>`;
    if (open && !isRec && c.cities.length > 1) {
      html += '<div class="cities">';
      for (const city of (q ? cityMatch : c.cities)) {
        const ms = latency[`${c.code}/${city.code}`];
        const csel = selCountry === c.code && sel.city_code === city.code;
        html += `<button class="city ${csel ? "selected" : ""}" data-country="${esc(c.code)}" data-city="${esc(city.code)}">
          <span>${esc(city.name)}</span>${ms != null ? `<span class="ping ${pingClass(ms)}">${pingText(ms)}</span>` : ""}</button>`;
      }
      html += "</div>";
    }
    html += "</div>";
  }
  list.innerHTML = html || '<div class="list-msg">No matches</div>';
}

// ---------- per-app tunnelling (Android) ----------
async function saveAppSettings() {
  const s = { ...snap.settings, app_tunnel_mode: appMode, apps: selectedApps };
  try {
    await api().SaveSettings(s);
    await refreshSnapshot();
  } catch (e) {
    toast(errText(e));
    render();
  }
}

async function openAppsSheet() {
  for (const b of $$("#set-apptunnel button")) b.classList.toggle("active", b.dataset.mode === appMode);
  $("#app-search").value = "";
  openSheet("sheet-apps");
  if (!installedApps) {
    $("#app-list").innerHTML = '<div class="list-msg">Loading apps…</div>';
    try {
      installedApps = await api().GetApps();
    } catch (e) {
      toast(errText(e));
      installedApps = [];
    }
  }
  renderApps();
}

function renderApps() {
  const list = $("#app-list");
  if (!installedApps) return;
  if (!installedApps.length) {
    list.innerHTML = '<div class="list-msg">No apps found</div>';
    return;
  }
  const q = $("#app-search").value.trim().toLowerCase();
  const sel = new Set(selectedApps);
  const rows = installedApps.filter((a) => !q || (a.name || "").toLowerCase().includes(q) || (a.id || "").toLowerCase().includes(q));
  list.innerHTML = rows.map((a) => `
    <label class="app-row">
      <input type="checkbox" data-app="${esc(a.id)}" ${sel.has(a.id) ? "checked" : ""}>
      ${a.icon
        ? `<img class="app-icon" alt="" loading="lazy" src="data:image/png;base64,${a.icon}">`
        : '<span class="app-icon empty"></span>'}
      <span class="name"><b>${esc(a.name)}</b><small>${esc(a.id)}</small></span>
    </label>`).join("") || '<div class="list-msg">No matches</div>';
}

// ---------- data usage by destination ----------
async function loadUsage() {
  try {
    usageData = await api().GetUsage(0);
  } catch (e) {
    usageData = [];
  }
  try {
    usageApps = snap && snap.platform === "windows" ? await api().GetAppUsage() : [];
  } catch (e) {
    usageApps = [];
  }
  renderUsage();
}

function renderUsage() {
  const list = $("#usage-list");
  if (!usageData) return;
  const q = $("#usage-search").value.trim().toLowerCase();
  const sum = (k) => usageData.reduce((s, x) => s + x[k], 0);
  $("#usage-total-down").textContent = fmtBytes(sum("down"));
  $("#usage-total-up").textContent = fmtBytes(sum("up"));
  $("#usage-total").textContent = fmtBytes(sum("down") + sum("up"));

  let html = "";
  const apps = usageApps || [];
  if (apps.length) {
    // Apps first; tap one to unfold the destinations it used.
    for (const a of apps) {
      if (q && !(a.name || "").toLowerCase().includes(q) && !a.hosts.some((h) => h.host.includes(q))) continue;
      const open = expandedApp === a.name;
      html += `
      <button class="usage-row app" data-app="${esc(a.name)}">
        <span class="name"><b>${esc(a.name)}</b><small>&darr; ${fmtBytes(a.down)} &middot; &uarr; ${fmtBytes(a.up)}${open ? " · " + a.hosts.length + " destinations" : ""}</small></span>
        <span class="usage-total">${fmtBytes(a.up + a.down)}<span class="chev ${open ? "open" : ""}">${CHEVRON}</span></span>
      </button>`;
      if (open) {
        const hosts = a.hosts.filter((h) => !q || h.host.includes(q));
        const max = Math.max(...hosts.map((h) => h.up + h.down), 1);
        html += hosts.map((h) => `
        <div class="usage-row sub">
          <span class="name"><b title="${esc(h.host)}">${esc(h.host)}</b><small>&darr; ${fmtBytes(h.down)} &middot; &uarr; ${fmtBytes(h.up)}</small>
            <span class="mini-bar"><i style="width:${Math.max(3, ((h.up + h.down) / max) * 100).toFixed(1)}%"></i></span></span>
          <span class="usage-total">${fmtBytes(h.up + h.down)}</span>
        </div>`).join("") || '<div class="usage-row sub"><span class="name"><small>No matches</small></span></div>';
      }
    }
  } else {
    // Platforms without per-app lookup: flat destinations.
    const rows = usageData.filter((x) => !q || x.host.toLowerCase().includes(q));
    const max = Math.max(...rows.map((x) => x.up + x.down), 1);
    html = rows.map((x) => `
      <div class="usage-row">
        <span class="name"><b title="${esc(x.host)}">${esc(x.host)}</b><small>&darr; ${fmtBytes(x.down)} &middot; &uarr; ${fmtBytes(x.up)}</small>
          <span class="mini-bar"><i style="width:${Math.max(3, ((x.up + x.down) / max) * 100).toFixed(1)}%"></i></span></span>
        <span class="usage-total">${fmtBytes(x.up + x.down)}</span>
      </div>`).join("");
  }
  if (!html) {
    html = `<div class="list-msg">${(usageData.length || apps.length) ? "No matches" : 'No data usage recorded yet.<br><small class="muted">Connect and use apps — their usage appears here.</small>'}</div>`;
  }
  // Keep the scroll position: the list refreshes live while the sheet is open.
  const at = list.scrollTop;
  list.innerHTML = html;
  list.scrollTop = at;
}

function openUsageSheet() {
  $("#usage-search").value = "";
  usageData = null;
  expandedApp = null;
  openSheet("sheet-usage");
  $("#usage-list").innerHTML = '<div class="list-msg">Loading…</div>';
  loadUsage();
  clearInterval(usageTimer);
  usageTimer = setInterval(loadUsage, 4000); // stay live while open
}

// ---------- data ----------
async function refreshSnapshot() {
  snap = await api().GetSnapshot();
  render();
}

async function loadServers(force) {
  try {
    const list = await api().GetServers(!!force);
    list.sort((a, b) => (a.code === "REC" ? -1 : b.code === "REC" ? 1 : a.name.localeCompare(b.name)));
    servers = list;
    renderServers();
    render();
  } catch (e) {
    $("#server-list").innerHTML = `<div class="list-msg">Could not load servers.<br>${esc(errText(e))}</div>`;
  }
}

let pinging = false;
async function measureLatency() {
  if (pinging) return;
  pinging = true;
  $("#btn-ping").classList.add("spinning");
  try {
    latency = await api().GetLatency();
    renderServers();
    render();
  } finally {
    pinging = false;
    $("#btn-ping").classList.remove("spinning");
  }
}

// ---------- actions ----------
async function toggleConnection() {
  const st = snap.status;
  try {
    if (st === "disconnected") {
      await api().Connect();
    } else {
      await api().Disconnect();
    }
  } catch (e) {
    const msg = errText(e);
    if (msg.startsWith("ELEVATION_REQUIRED")) showAdminModal(msg.endsWith("killswitch") ? "killswitch" : "tun");
    else toast(msg);
  }
}

let adminReason = "tun";
function showAdminModal(reason) {
  adminReason = reason;
  $("#admin-reason").textContent = reason === "killswitch"
    ? "The kill switch uses the Windows firewall to block traffic when the VPN drops. Windows requires administrator permission for that."
    : "Full VPN mode creates a virtual network adapter so every app is protected. Windows requires administrator permission for that.";
  $("#btn-use-proxy").textContent = reason === "killswitch" ? "Turn off kill switch and connect" : "Use System Proxy instead";
  $("#modal-admin").hidden = false;
}

async function elevate() {
  try {
    await api().RestartAsAdmin();
  } catch (e) {
    toast(errText(e));
  }
}

// ---------- web sign-in (the API password route is edge-blocked) ----------
async function startWebLogin() {
  try { localStorage.setItem("zeusAppURL", location.origin + location.pathname); } catch (e) {}
  $("#login-error").textContent = "Opening the Firefox sign-in page…";
  try {
    await api().StartWebLogin(localStorage.getItem("zeusAppURL") || (location.origin + location.pathname));
  } catch (e) {
    $("#login-error").textContent = errText(e);
  }
}

// ---------- wiring ----------
function wire() {
  $("#btn-min").onclick = () => window.runtime.WindowMinimise();
  $("#btn-close").onclick = () => window.runtime.Quit();

  $("#orb").onclick = toggleConnection;
  $("#link-signup").onclick = (e) => { e.preventDefault(); api().OpenURL("https://accounts.firefox.com/signup"); };
  $("#login-web").onclick = startWebLogin;
  $("#login-cancel").onclick = () => {
    addingAccount = false;
    $("#login-error").textContent = "";
    render();
    if (snap.loggedIn) openSheet("sheet-settings");
  };
  for (const b of $$("#mode-switch button")) {
    b.onclick = async () => {
      const m = b.dataset.mode;
      if (m === snap.settings.mode) return;
      try {
        await api().SetMode(m);
        if (m === "tun" && !snap.elevated && snap.status === "connected") showAdminModal("tun");
      } catch (e) {
        toast(errText(e));
      }
    };
  }

  $("#btn-location").onclick = () => {
    openCountry = snap.settings.country_code || null;
    $("#server-search").value = "";
    renderServers();
    openSheet("sheet-servers");
    if (!Object.keys(latency).length) measureLatency();
  };
  $("#btn-ping").onclick = measureLatency;
  $("#server-search").oninput = renderServers;
  $("#server-list").onclick = async (e) => {
    const exp = e.target.closest("[data-expand]");
    if (exp) {
      e.stopPropagation();
      openCountry = openCountry === exp.dataset.expand ? null : exp.dataset.expand;
      renderServers();
      return;
    }
    const item = e.target.closest("[data-country]");
    if (!item) return;
    const country = item.dataset.country === "REC" ? "" : item.dataset.country;
    const city = item.dataset.city || "";
    try {
      await api().SetLocation(country, city);
      await refreshSnapshot();
      closeSheets();
    } catch (err) {
      toast(errText(err));
    }
  };

  const openSettings = () => {
    openSheet("sheet-settings");
    const now = Date.now();
    if (now - lastQuotaRefresh > 60000) {
      lastQuotaRefresh = now;
      api().RefreshQuotas();
    }
  };
  $("#btn-settings").onclick = openSettings;
  $("#btn-account").onclick = openSettings;
  $("#scrim").onclick = closeSheets;
  for (const b of $$("[data-close]")) b.onclick = closeSheets;

  const saveSettings = async () => {
    const s = { ...snap.settings };
    s.bypass_iran = $("#set-bypass-ir").checked;
    s.bypass_lan = $("#set-bypass-lan").checked;
    s.auto_connect = $("#set-autoconnect").checked;
    s.auto_switch = $("#set-autoswitch").checked;
    s.kill_switch = $("#set-killswitch").checked;
    s.tls_split = $("#set-tls-split").value;
    s.proxy_port = parseInt($("#set-port").value, 10) || s.proxy_port;
    try {
      await api().SaveSettings(s);
      await refreshSnapshot();
    } catch (e) {
      toast(errText(e));
      render();
    }
  };
  for (const id of ["#set-bypass-ir", "#set-bypass-lan", "#set-autoconnect", "#set-autoswitch", "#set-tls-split", "#set-killswitch"]) $(id).onchange = saveSettings;
  $("#set-port").onchange = saveSettings;

  for (const b of $$("#set-apptunnel button")) {
    b.onclick = async () => {
      const m = b.dataset.mode;
      if (m === appMode) return;
      appMode = m;
      for (const x of $$("#set-apptunnel button")) x.classList.toggle("active", x.dataset.mode === m);
      if (m === "all") {
        selectedApps = [];
        await saveAppSettings();
      } else if (selectedApps.length) {
        await saveAppSettings();
      } else {
        openAppsSheet(); // pick at least one app, or close to revert
      }
    };
  }
  $("#btn-choose-apps").onclick = () => openAppsSheet();
  $("#app-search").oninput = renderApps;
  $("#app-list").onchange = async (e) => {
    const id = e.target.dataset && e.target.dataset.app;
    if (!id) return;
    if (e.target.checked) {
      if (!selectedApps.includes(id)) selectedApps.push(id);
    } else {
      selectedApps = selectedApps.filter((x) => x !== id);
    }
    $("#apps-count").textContent = selectedApps.length ? `· ${selectedApps.length}` : "";
    if (selectedApps.length) await saveAppSettings();
    // With nothing selected, keep the picker open: closing it reverts to "All apps".
  };
  $("#set-autostart").onchange = async (e) => {
    try {
      await api().SetAutostart(e.target.checked);
    } catch (err) {
      toast(errText(err));
    }
    refreshSnapshot();
  };

  $("#btn-usage").onclick = () => openUsageSheet();
  $("#usage-search").oninput = renderUsage;
  $("#usage-list").onclick = (e) => {
    const row = e.target.closest("[data-app]");
    if (!row) return;
    expandedApp = expandedApp === row.dataset.app ? null : row.dataset.app;
    renderUsage();
  };
  $("#btn-usage-reset").onclick = async () => {
    if (!(await askConfirm("Clear the usage history?", "Clear usage history"))) return;
    try {
      await api().ResetUsage();
      usageData = [];
      usageApps = [];
      expandedApp = null;
      renderUsage();
      toast("Usage history cleared");
    } catch (err) {
      toast(errText(err));
    }
  };

  $("#btn-add-account").onclick = () => {
    addingAccount = true;
    closeSheets();
    $("#login-error").textContent = "";
    render();
  };
  $("#account-list").onclick = async (e) => {
    const use = e.target.closest("[data-use]");
    const rm = e.target.closest("[data-remove]");
    try {
      if (use) {
        await api().UseAccount(use.dataset.use);
        toast("Now using " + use.dataset.use);
      } else if (rm) {
        const email = rm.dataset.remove;
        if (!(await askConfirm(`Sign out of ${email || "this account"}?`, "Sign out"))) return;
        await api().RemoveAccount(email);
        await refreshSnapshot();
        if (!snap.loggedIn) {
          closeSheets();
          servers = [];
          latency = {};
        }
      }
    } catch (err) {
      toast(errText(err));
    }
  };
  $("#btn-logfile").onclick = () => api().OpenLogFile();

  $("#btn-logs").onclick = async () => {
    const lines = await api().GetLogs();
    const box = $("#logs");
    box.innerHTML = "";
    for (const l of lines) appendLog(l);
    openSheet("sheet-logs");
    box.scrollTop = box.scrollHeight;
  };
  $("#btn-copy-logs").onclick = () => {
    navigator.clipboard.writeText($("#logs").innerText).then(() => toast("Logs copied"));
  };

  $("#btn-elevate").onclick = elevate;
  $("#btn-use-proxy").onclick = async () => {
    $("#modal-admin").hidden = true;
    if (adminReason === "killswitch") {
      await api().SaveSettings({ ...snap.settings, kill_switch: false });
      await refreshSnapshot();
      toggleConnection();
    } else {
      await api().SetMode("proxy");
    }
  };
  $("#modal-admin").onclick = (e) => { if (e.target.id === "modal-admin") $("#modal-admin").hidden = true; };
  $("#btn-confirm-yes").onclick = () => { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(true); confirmResolve = null; };
  $("#btn-confirm-no").onclick = () => { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(false); confirmResolve = null; };
  $("#modal-confirm").onclick = (e) => {
    if (e.target.id === "modal-confirm") {
      $("#modal-confirm").hidden = true;
      if (confirmResolve) confirmResolve(false);
      confirmResolve = null;
    }
  };

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      closeSheets();
      $("#modal-admin").hidden = true;
      if (!$("#modal-confirm").hidden) {
        $("#modal-confirm").hidden = true;
        if (confirmResolve) confirmResolve(false);
        confirmResolve = null;
      }
    }
  });
  document.addEventListener("contextmenu", (e) => {
    if (!e.target.closest("input, .logs")) e.preventDefault();
  });
}

function appendLog(line) {
  const box = $("#logs");
  const div = document.createElement("div");
  div.className = / warning /.test(line) ? "warning" : / error /.test(line) ? "error" : "info";
  div.textContent = line;
  box.appendChild(div);
  if (box.children.length > 500) box.firstChild.remove();
}

async function init() {
  wire();
  window.runtime.EventsOn("state", (s) => { snap = s; render(); });
  window.runtime.EventsOn("stats", (s) => { lastStats = s; renderStats(); });
  window.runtime.EventsOn("notice", (msg) => toast(msg, 6000));
  window.runtime.EventsOn("log", (line) => {
    if (!$("#sheet-logs").hidden) {
      const box = $("#logs");
      const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 30;
      appendLog(line);
      if (atBottom) box.scrollTop = box.scrollHeight;
    }
  });
  await refreshSnapshot();
  if (snap.loggedIn) loadServers(false);
}

(function waitForBridge() {
  if (window.go && window.go.main && window.runtime) init();
  else setTimeout(waitForBridge, 20);
})();
