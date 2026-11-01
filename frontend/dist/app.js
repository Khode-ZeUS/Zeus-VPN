"use strict";

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => document.querySelectorAll(selector);
const api = () => window.go.main.App;

const translations = {
  en: {
    brandTagline: "THE ART OF DIGITAL PROTECTION", minimize: "Minimize", close: "Close",
    introKicker: "THE ART OF DIGITAL PROTECTION", introPreparing: "Preparing your command center", introConnectingBridge: "Connecting to the application bridge", introReadingState: "Reading the application state", introStateReady: "Application state ready", introError: "The application could not read its initial state.", introLocalEngine: "Local application engine", retry: "Retry",
    accountAccess: "ACCOUNT ACCESS", welcomeToZeus: "Welcome to ZEUS", signInMozilla: "Sign in with your Mozilla account", email: "Email", password: "Password", signIn: "Sign in", signInWithFirefox: "Sign in with Firefox", cancel: "Cancel", loginHint: "The Mozilla sign-in page opens inside this window.", freeMozilla: "Free with any Mozilla account · 50 GB per month", createAccount: "Create a free account",
    navigation: "NAVIGATION", primaryNavigation: "Primary navigation", mobileNavigation: "Mobile navigation", liveChart: "Live session traffic chart", commandCenter: "Command Center", locations: "Locations", statistics: "Statistics", activity: "Activity", settings: "Settings", home: "Home", protectedByMozilla: "Protected by Mozilla",
    commandCenterEyebrow: "COMMAND CENTER", commandCenterTitle: "Your protection, at a glance.", commandCenterLead: "A clear view of the connection that protects your digital life.", connectionState: "CONNECTION STATE", connect: "Connect", disconnect: "Disconnect", stateDisconnected: "Disconnected", stateConnecting: "Connecting", stateConnected: "Connected", stateDisconnecting: "Disconnecting", stateReconnecting: "Reconnecting", tapToConnect: "Choose a location, then establish protection.", securingConnection: "Securing your connection", protectedNow: "Your traffic is protected", disconnecting: "Closing the secure route", reconnecting: "Connection lost, retrying", actualStateOnly: "Actual network state only",
    protectedRoute: "PROTECTED ROUTE", selectedLocation: "Selected location", change: "Change", connectionMode: "Connection mode", systemProxy: "System Proxy", fullVpn: "Full VPN", exitIp: "Exit IP", accountQuota: "ACCOUNT QUOTA", monthlyData: "Monthly data", remaining: "Remaining", allAccounts: "All accounts", liveTraffic: "LIVE TRAFFIC", currentSession: "Current session", openObservatory: "Open observatory", download: "Download", upload: "Upload", sessionTotal: "Session total", liveOneSecond: "Live · one-second samples", protectionMode: "PROTECTION MODE", chooseTrafficRoute: "Choose how traffic leaves this device.",
    locationsEyebrow: "NETWORK ATLAS", locationsTitle: "Choose your location.", locationsLead: "Select a real server route. Latency is measured only when requested.", measureLatency: "Measure latency", searchLocations: "Search countries or cities", serverDataActual: "Server data from the application", loadingServers: "Loading servers…", couldNotLoadServers: "Could not load servers.", noMatches: "No matches", fastestAvailable: "Fastest available server", cities: "cities", city: "city", timeout: "timeout",
    statisticsEyebrow: "NETWORK OBSERVATORY", statisticsTitle: "See what ZEUS is carrying.", statisticsLead: "Live, current-session measurements from the existing tunnel.", liveSession: "LIVE SESSION", sessionTraffic: "SESSION TRAFFIC", notConnected: "No active session", oneSecondSamples: "1s samples", combinedTraffic: "Combined traffic", chartAwaiting: "Live samples will appear when the tunnel is active.", downloadRate: "Download rate", uploadRate: "Upload rate", quotaRemaining: "Quota remaining", historyBoundary: "HISTORY BOUNDARY", historyUnavailable: "Historical time series unavailable", historyUnavailableDetail: "This version exposes live session samples and recorded destinations, not daily, weekly or monthly buckets.", recordedDestinations: "RECORDED DESTINATIONS", whereTrafficWent: "Where recorded traffic went.", retainedUsage: "Retained usage data", destinationNote: "Cumulative destination totals with last activity, not a time-series history.", windowsApplications: "WINDOWS APPLICATIONS", applicationTraffic: "Application traffic", windowsOnly: "Windows only",
    activityEyebrow: "ACTIVITY LEDGER", activityTitle: "A factual record of the application.", activityLead: "Logs, notices and retained destination usage — without invented history.", copyLogs: "Copy logs", applicationLogs: "APPLICATION LOGS", diagnosticRecord: "Diagnostic record", openLogFile: "Open log file", logsEmpty: "Open logs to load the current diagnostic record.", destinationUsage: "Destination usage", searchDestinations: "Search destinations", total: "Total", clearHistory: "Clear history", usageUnavailable: "Usage data unavailable", usageUnavailableDetail: "The application could not read retained destination usage.", retryUsage: "Retry usage", logsUnavailable: "Logs unavailable", logsUnavailableDetail: "The application could not read its diagnostic record.", retryLogs: "Retry logs",
    settingsEyebrow: "CONTROL ROOM", settingsTitle: "Shape the way ZEUS protects you.", settingsLead: "Connection, routing, startup and appearance preferences.", accounts: "ACCOUNTS", accountsTitle: "Mozilla accounts", addAccount: "Add another account", connection: "CONNECTION", connectionSettings: "Connection behavior", autoConnect: "Connect on launch", autoConnectDetail: "Reconnect automatically when ZEUS starts.", autoSwitch: "Auto-switch accounts", autoSwitchDetail: "Use the next account when one runs out of data.", startAtLogin: "Start at login", startWithWindows: "Start with Windows", openAtLogin: "Open at login", autostartDetail: "Launch ZEUS automatically when you sign in.", killSwitch: "Kill switch", killSwitchDetail: "Block traffic if the VPN drops until it reconnects or you disconnect.", routing: "ROUTING", routingSettings: "Traffic boundaries", bypassIran: "Bypass Iranian sites", bypassIranDetail: ".ir domains connect directly.", bypassLan: "Bypass local network", bypassLanDetail: "Printers, routers and LAN stay direct.", bypassLanUnsupported: "Android 13 or newer is required for route-level LAN bypass.", fullVpnApps: "FULL VPN APPS", allApps: "All apps", selectedOnly: "Selected only", exceptSelected: "Except selected", chooseApps: "Choose apps", searchApps: "Search apps", antiFiltering: "ANTI-FILTERING", antiFilteringSettings: "Transport detail", tlsFragmentation: "TLS fragmentation", tlsFragmentationDetail: "Hides server names from SNI filtering.", tlsAuto: "Auto", tlsAlways: "Always (SNI)", tlsFirstByte: "First byte", tlsOff: "Off", localProxyPort: "Local proxy port", localProxyPortDetail: "HTTP + SOCKS5 for other apps.", appearance: "APPEARANCE", appearanceTitle: "A quieter command center.", language: "Language", languageDetail: "Choose the interface direction.", english: "English", persian: "فارسی", visualEffects: "Atmospheric effects", visualEffectsDetail: "Reduce movement and decorative lighting.", troubleshooting: "TROUBLESHOOTING", diagnosticsTitle: "Diagnostics", viewLogs: "View logs", openStatistics: "Open statistics",
    confirmation: "CONFIRMATION", confirm: "Confirm", elevatedAccess: "ELEVATED ACCESS", administratorNeeded: "Administrator needed", restartAsAdmin: "Restart as administrator", useSystemProxy: "Use System Proxy instead",
    checkingData: "Checking data…", outOfData: "Out of data", signInExpired: "Sign-in expired · remove and add it again", active: "Active", use: "Use", noApps: "No apps found", noUsage: "No data usage recorded yet.", noUsageDetail: "Connect and use apps — their usage appears here.", noActiveSession: "No active session", recordedNoData: "No destinations recorded yet.", lastActive: "Last active", noLogs: "No log lines available.", logsCopied: "Logs copied", usageCleared: "Usage history cleared", nowUsing: "Now using", openAppSettings: "Open Settings", adminTun: "Full VPN mode creates a virtual network adapter so every app is protected. Windows requires administrator permission for that.", adminKill: "The kill switch uses the Windows firewall to block traffic when the VPN drops. Windows requires administrator permission for that.",
    allData: "All data", session: "Session", unknown: "Unknown", loading: "Loading…", error: "Error", noQuota: "Quota unavailable", recommended: "Recommended", anyCity: "Any city", destinations: "destinations", reloadServers: "Reload servers",
  },
  fa: {
    brandTagline: "هنرِ حفاظت دیجیتال", minimize: "کوچک‌کردن", close: "بستن",
    introKicker: "هنرِ حفاظت دیجیتال", introPreparing: "در حال آماده‌سازی مرکز فرماندهی", introConnectingBridge: "در حال اتصال به پل برنامه", introReadingState: "در حال خواندن وضعیت برنامه", introStateReady: "وضعیت برنامه آماده است", introError: "برنامه نتوانست وضعیت اولیه را بخواند.", introLocalEngine: "موتور محلی برنامه", retry: "تلاش دوباره",
    accountAccess: "دسترسی حساب", welcomeToZeus: "به زئوس خوش آمدید", signInMozilla: "با حساب موزیلا وارد شوید", email: "ایمیل", password: "رمز عبور", signIn: "ورود", signInWithFirefox: "ورود با فایرفاکس", cancel: "لغو", loginHint: "صفحه ورود موزیلا داخل همین پنجره باز می‌شود.", freeMozilla: "رایگان با هر حساب موزیلا · ۵۰ گیگابایت در ماه", createAccount: "ساخت حساب رایگان",
    navigation: "ناوبری", primaryNavigation: "ناوبری اصلی", mobileNavigation: "ناوبری موبایل", liveChart: "نمودار ترافیک نشست زنده", commandCenter: "مرکز فرماندهی", locations: "موقعیت‌ها", statistics: "آمار", activity: "فعالیت", settings: "تنظیمات", home: "خانه", protectedByMozilla: "حفاظت‌شده توسط موزیلا",
    commandCenterEyebrow: "مرکز فرماندهی", commandCenterTitle: "حفاظت شما، در یک نگاه.", commandCenterLead: "نمایی روشن از ارتباطی که از زندگی دیجیتال شما محافظت می‌کند.", connectionState: "وضعیت اتصال", connect: "اتصال", disconnect: "قطع اتصال", stateDisconnected: "متصل نیست", stateConnecting: "در حال اتصال", stateConnected: "متصل", stateDisconnecting: "در حال قطع اتصال", stateReconnecting: "در حال اتصال دوباره", tapToConnect: "موقعیت را انتخاب کنید و حفاظت را فعال کنید.", securingConnection: "در حال ایمن‌سازی اتصال", protectedNow: "ترافیک شما محافظت می‌شود", disconnecting: "در حال بستن مسیر امن", reconnecting: "اتصال از دست رفت؛ تلاش دوباره", actualStateOnly: "فقط وضعیت واقعی شبکه",
    protectedRoute: "مسیر محافظت‌شده", selectedLocation: "موقعیت انتخاب‌شده", change: "تغییر", connectionMode: "حالت اتصال", systemProxy: "پروکسی سیستم", fullVpn: "VPN کامل", exitIp: "IP خروجی", accountQuota: "سهمیه حساب", monthlyData: "داده ماهانه", remaining: "باقی‌مانده", allAccounts: "همه حساب‌ها", liveTraffic: "ترافیک زنده", currentSession: "نشست فعلی", openObservatory: "باز کردن رصدخانه", download: "دریافت", upload: "ارسال", sessionTotal: "مجموع نشست", liveOneSecond: "زنده · نمونه‌های یک‌ثانیه‌ای", protectionMode: "حالت حفاظت", chooseTrafficRoute: "نحوه خروج ترافیک از این دستگاه را انتخاب کنید.",
    locationsEyebrow: "اطلس شبکه", locationsTitle: "موقعیت خود را انتخاب کنید.", locationsLead: "یک مسیر واقعی سرور را انتخاب کنید. تأخیر فقط در صورت درخواست اندازه‌گیری می‌شود.", measureLatency: "اندازه‌گیری تأخیر", searchLocations: "جست‌وجوی کشور یا شهر", serverDataActual: "اطلاعات سرور از برنامه", loadingServers: "در حال بارگذاری سرورها…", couldNotLoadServers: "بارگذاری سرورها ممکن نشد.", noMatches: "موردی پیدا نشد", fastestAvailable: "سریع‌ترین سرور موجود", cities: "شهر", city: "شهر", timeout: "پایان زمان",
    statisticsEyebrow: "رصدخانه شبکه", statisticsTitle: "ببینید زئوس چه چیزی را حمل می‌کند.", statisticsLead: "اندازه‌گیری زنده نشست فعلی از تونل موجود.", liveSession: "نشست زنده", sessionTraffic: "ترافیک نشست", notConnected: "نشست فعالی وجود ندارد", oneSecondSamples: "نمونه‌های ۱ ثانیه‌ای", combinedTraffic: "ترافیک ترکیبی", chartAwaiting: "نمونه‌های زنده هنگام فعال بودن تونل نمایش داده می‌شوند.", downloadRate: "سرعت دریافت", uploadRate: "سرعت ارسال", quotaRemaining: "سهمیه باقی‌مانده", historyBoundary: "مرز تاریخچه", historyUnavailable: "سری زمانی تاریخی در دسترس نیست", historyUnavailableDetail: "این نسخه نمونه‌های زنده نشست و مقصدهای ثبت‌شده را دارد، نه داده‌های روزانه، هفتگی یا ماهانه.", recordedDestinations: "مقصدهای ثبت‌شده", whereTrafficWent: "ترافیک ثبت‌شده به کجا رفته است.", retainedUsage: "داده مصرف نگه‌داری‌شده", destinationNote: "مجموع تجمعی مقصدها با آخرین فعالیت؛ نه تاریخچه سری زمانی.", windowsApplications: "برنامه‌های ویندوز", applicationTraffic: "ترافیک برنامه‌ها", windowsOnly: "فقط ویندوز",
    activityEyebrow: "دفتر فعالیت", activityTitle: "گزارش واقعی برنامه.", activityLead: "لاگ‌ها، اعلان‌ها و مصرف مقصدهای ثبت‌شده — بدون تاریخچه ساختگی.", copyLogs: "کپی لاگ‌ها", applicationLogs: "لاگ‌های برنامه", diagnosticRecord: "گزارش عیب‌یابی", openLogFile: "باز کردن فایل لاگ", logsEmpty: "برای بارگذاری گزارش فعلی، لاگ‌ها را باز کنید.", destinationUsage: "مصرف مقصدها", searchDestinations: "جست‌وجوی مقصدها", total: "مجموع", clearHistory: "پاک‌کردن تاریخچه", usageUnavailable: "داده مصرف در دسترس نیست", usageUnavailableDetail: "برنامه نتوانست مصرف مقصدهای نگه‌داری‌شده را بخواند.", retryUsage: "تلاش دوباره برای مصرف", logsUnavailable: "لاگ‌ها در دسترس نیستند", logsUnavailableDetail: "برنامه نتوانست گزارش عیب‌یابی را بخواند.", retryLogs: "تلاش دوباره برای لاگ‌ها",
    settingsEyebrow: "اتاق کنترل", settingsTitle: "نحوه حفاظت زئوس را تنظیم کنید.", settingsLead: "تنظیمات اتصال، مسیریابی، شروع و ظاهر.", accounts: "حساب‌ها", accountsTitle: "حساب‌های موزیلا", addAccount: "افزودن حساب دیگر", connection: "اتصال", connectionSettings: "رفتار اتصال", autoConnect: "اتصال هنگام شروع", autoConnectDetail: "با شروع زئوس، خودکار دوباره متصل شود.", autoSwitch: "تعویض خودکار حساب", autoSwitchDetail: "وقتی سهمیه یک حساب تمام شد از حساب بعدی استفاده شود.", startAtLogin: "شروع هنگام ورود", startWithWindows: "شروع با ویندوز", openAtLogin: "باز شدن هنگام ورود", autostartDetail: "زئوس هنگام ورود شما خودکار اجرا شود.", killSwitch: "کلید قطع اضطراری", killSwitchDetail: "اگر VPN قطع شد، ترافیک را تا اتصال دوباره مسدود کند.", routing: "مسیریابی", routingSettings: "مرزهای ترافیک", bypassIran: "عبور مستقیم از سایت‌های ایرانی", bypassIranDetail: "دامنه‌های .ir مستقیم متصل می‌شوند.", bypassLan: "عبور مستقیم از شبکه محلی", bypassLanDetail: "پرینترها، روترها و شبکه محلی مستقیم می‌مانند.", bypassLanUnsupported: "برای عبور مستقیم مسیرهای شبکه محلی در اندروید، نسخه ۱۳ یا جدیدتر لازم است.", fullVpnApps: "برنامه‌های VPN کامل", allApps: "همه برنامه‌ها", selectedOnly: "فقط انتخاب‌شده‌ها", exceptSelected: "به‌جز انتخاب‌شده‌ها", chooseApps: "انتخاب برنامه‌ها", searchApps: "جست‌وجوی برنامه‌ها", antiFiltering: "ضدفیلتر", antiFilteringSettings: "جزئیات انتقال", tlsFragmentation: "قطعه‌قطعه‌سازی TLS", tlsFragmentationDetail: "نام سرورها را از فیلتر SNI پنهان می‌کند.", tlsAuto: "خودکار", tlsAlways: "همیشه (SNI)", tlsFirstByte: "بایت اول", tlsOff: "خاموش", localProxyPort: "پورت پروکسی محلی", localProxyPortDetail: "HTTP و SOCKS5 برای برنامه‌های دیگر.", appearance: "ظاهر", appearanceTitle: "مرکز فرماندهی آرام‌تر.", language: "زبان", languageDetail: "جهت رابط را انتخاب کنید.", english: "English", persian: "فارسی", visualEffects: "جلوه‌های محیطی", visualEffectsDetail: "حرکت و نورپردازی تزئینی را کاهش دهید.", troubleshooting: "عیب‌یابی", diagnosticsTitle: "عیب‌یابی", viewLogs: "مشاهده لاگ‌ها", openStatistics: "باز کردن آمار",
    confirmation: "تأیید", confirm: "تأیید", elevatedAccess: "دسترسی سطح بالا", administratorNeeded: "دسترسی مدیر لازم است", restartAsAdmin: "راه‌اندازی دوباره به‌عنوان مدیر", useSystemProxy: "استفاده از پروکسی سیستم",
    checkingData: "در حال بررسی داده…", outOfData: "داده تمام شده", signInExpired: "ورود منقضی شده · حذف و دوباره اضافه کنید", active: "فعال", use: "استفاده", noApps: "برنامه‌ای پیدا نشد", noUsage: "هنوز مصرفی ثبت نشده است.", noUsageDetail: "اتصال برقرار کنید و از برنامه‌ها استفاده کنید — مصرف اینجا نمایش داده می‌شود.", noActiveSession: "نشست فعالی وجود ندارد", recordedNoData: "هنوز مقصدی ثبت نشده است.", lastActive: "آخرین فعالیت", noLogs: "خط لاگی در دسترس نیست.", logsCopied: "لاگ‌ها کپی شد", usageCleared: "تاریخچه مصرف پاک شد", nowUsing: "اکنون در حال استفاده از", openAppSettings: "باز کردن تنظیمات", adminTun: "حالت VPN کامل یک آداپتور مجازی می‌سازد تا همه برنامه‌ها محافظت شوند. ویندوز برای این کار دسترسی مدیر می‌خواهد.", adminKill: "کلید قطع اضطراری برای مسدودکردن ترافیک از فایروال ویندوز استفاده می‌کند. ویندوز برای این کار دسترسی مدیر می‌خواهد.",
    allData: "همه داده‌ها", session: "نشست", unknown: "نامشخص", loading: "در حال بارگذاری…", error: "خطا", noQuota: "سهمیه در دسترس نیست", recommended: "پیشنهادی", anyCity: "هر شهر", destinations: "مقصد", reloadServers: "بارگذاری دوباره سرورها",
  },
};

let locale = "en";
let effectsEnabled = true;
try {
  locale = localStorage.getItem("zeus.locale") === "fa" ? "fa" : "en";
  effectsEnabled = localStorage.getItem("zeus.effects") !== "off";
} catch (e) {}

let snap = null;
let servers = [];
let latency = {};
let openCountry = null;
let timerHandle = null;
let lastStats = { up: 0, down: 0, upRate: 0, downRate: 0, quotaLimit: 0, quotaLeft: 0 };
let previousStats = { up: 0, down: 0 };
let sessionSamples = [];
let sessionActive = false;
let addingAccount = false;
let lastQuotaRefresh = 0;
let installedApps = null;
let appMode = "all";
let selectedApps = [];
let usageData = null;
let usageApps = [];
let usageLoading = false;
let usageError = "";
let expandedApp = null;
let webLoginPending = false;
let currentScreen = "home";
let logsLoaded = false;
let logsLoading = false;
let logsError = "";
let confirmResolve = null;
let toastTimer = null;
let introFinished = false;
let introTimer = null;
let pinging = false;
let adminReason = "tun";
let loginStep = null;

const GLOBE = '<svg viewBox="0 0 24 24"><path d="M12 2l2.4 5.6L20 8.3l-4.3 4 1.2 6L12 15.4 7.1 18.3l1.2-6L4 8.3l5.6-.7Z"/></svg>';
const CHEVRON = '<svg viewBox="0 0 24 24"><path d="m9 6 6 6-6 6"/></svg>';

function t(key, fallback = key) {
  return (translations[locale] && translations[locale][key]) || translations.en[key] || fallback;
}
function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function fmtBytes(value) {
  let n = Number(value) || 0;
  if (n < 0) n = 0;
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  if (i === 0) return `${Math.round(n)} B`;
  if (i === 1) return `${n.toFixed(n < 10 ? 1 : 0)} KB`;
  if (i === 2) return `${String(+n.toFixed(1))} MB`;
  return `${String(+n.toFixed(2))} ${units[i]}`;
}
function fmtDuration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(s / 3600)).padStart(2, "0")}:${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
}
function fmtDateMs(milliseconds) {
  if (!milliseconds) return "";
  try {
    return new Intl.DateTimeFormat(locale === "fa" ? "fa-IR" : "en-US", { month: "short", day: "numeric" }).format(new Date(Number(milliseconds)));
  } catch (e) {
    return new Date(Number(milliseconds)).toLocaleDateString();
  }
}
function fmtDateSeconds(seconds) {
  if (!seconds) return "";
  try {
    return new Intl.DateTimeFormat(locale === "fa" ? "fa-IR" : "en-US", { month: "short", day: "numeric" }).format(new Date(Number(seconds) * 1000));
  } catch (e) {
    return new Date(Number(seconds) * 1000).toLocaleDateString();
  }
}
function fmtNumber(value) {
  try { return new Intl.NumberFormat(locale === "fa" ? "fa-IR" : "en-US").format(value); } catch (e) { return String(value); }
}
function errText(error) {
  return String(error && error.message ? error.message : error).replace(/^Error:\s*/, "");
}
function pingClass(ms) {
  if (ms == null) return "";
  if (ms < 0) return "bad";
  const values = Object.values(latency).filter((value) => value >= 0);
  const best = values.length ? Math.min(...values) : ms;
  if (ms <= Math.max(150, best * 1.5)) return "good";
  return ms <= Math.max(300, best * 3) ? "ok" : "bad";
}
function pingText(ms) {
  if (ms == null) return "";
  return ms < 0 ? t("timeout") : `${ms} ms`;
}
function toast(message, ms = 3200) {
  const el = $("#toast");
  if (!el) return;
  el.textContent = message;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, ms);
}
function setFlag(el, code) {
  if (!el) return;
  if (!code || code === "REC") {
    el.style.backgroundImage = "";
    el.innerHTML = GLOBE;
  } else {
    el.innerHTML = "";
    el.style.backgroundImage = `url(flags/${String(code).toLowerCase()}.svg)`;
  }
}
function showIntroStatus(statusKey, detailKey) {
  const status = $("#intro-status");
  const detail = $("#intro-detail");
  if (status) status.textContent = t(statusKey);
  if (detail) detail.textContent = t(detailKey);
}
function closePanel() {
  $("#panel-scrim").hidden = true;
  $("#panel-apps").hidden = true;
}
function askConfirm(message, title = t("confirmation")) {
  $("#confirm-title").textContent = title;
  $("#confirm-message").textContent = message;
  $("#modal-confirm").hidden = false;
  return new Promise((resolve) => { confirmResolve = resolve; });
}

function applyLocale() {
  document.documentElement.lang = locale === "fa" ? "fa" : "en";
  document.documentElement.dir = locale === "fa" ? "rtl" : "ltr";
  document.body.classList.toggle("effects-off", !effectsEnabled);
  for (const el of $$('[data-i18n]')) el.textContent = t(el.dataset.i18n, el.textContent);
  for (const el of $$('[data-i18n-placeholder]')) el.placeholder = t(el.dataset.i18nPlaceholder, el.placeholder);
  for (const el of $$('[data-i18n-aria]')) el.setAttribute("aria-label", t(el.dataset.i18nAria, el.getAttribute("aria-label") || ""));
  for (const el of $$('[data-i18n-title]')) {
    const value = t(el.dataset.i18nTitle, el.title);
    el.title = value;
    el.setAttribute("aria-label", value);
  }
  if ($("#set-language")) $("#set-language").value = locale;
  if ($("#set-effects")) $("#set-effects").checked = effectsEnabled;
  updateBreadcrumb();
}
function setLocale(next) {
  locale = next === "fa" ? "fa" : "en";
  try { localStorage.setItem("zeus.locale", locale); } catch (e) {}
  applyLocale();
  render();
  renderStatistics();
  renderActivity();
}
function setEffects(enabled) {
  effectsEnabled = !!enabled;
  try { localStorage.setItem("zeus.effects", effectsEnabled ? "on" : "off"); } catch (e) {}
  applyLocale();
}

function updateBreadcrumb() {
  const el = $("#breadcrumb-title");
  if (!el) return;
  const labels = { home: "commandCenter", locations: "locations", statistics: "statistics", activity: "activity", settings: "settings" };
  el.textContent = t(labels[currentScreen] || "commandCenter");
}
function updateNavigation() {
  document.body.dataset.screen = currentScreen;
  for (const el of $$(".screen")) el.hidden = el.dataset.screen !== currentScreen;
  for (const el of $$(".nav-link[data-screen]")) el.classList.toggle("active", el.dataset.screen === currentScreen);
  updateBreadcrumb();
}
function navigate(screen, load = true) {
  if (!["home", "locations", "statistics", "activity", "settings"].includes(screen)) screen = "home";
  currentScreen = screen;
  updateNavigation();
  if (!load || !snap || !snap.loggedIn) return;
  if (screen === "locations") {
    renderServers();
    if (!servers.length) loadServers(false);
  }
  if (screen === "statistics" || screen === "activity") loadUsage();
  if (screen === "activity" && !logsLoaded) loadLogs();
}
function showLogin() {
  $("#login-view").hidden = false;
  $("#app-shell").hidden = true;
  $("#login-cancel").hidden = !snap || !snap.loggedIn;
  $("#login-subtitle").textContent = snap && snap.loggedIn ? t("addAccount") : t("signInMozilla");
  $("#login-error").textContent = snap && snap.error ? snap.error : "";
}

function locationName() {
  const settings = snap.settings || {};
  const cc = settings.country_code;
  if (!cc || cc === "REC") return { code: "REC", name: t("recommended"), sub: "" };
  const country = servers.find((item) => item.code === cc);
  const city = country && country.cities.find((item) => item.code === settings.city_code);
  return { code: cc, name: country ? country.name : cc, sub: city ? city.name : t("anyCity") };
}
function renderAccounts() {
  const list = $("#account-list");
  if (!list || !snap) return;
  list.innerHTML = (snap.accounts || []).map((account) => {
    const initial = (account.email || "Z")[0].toUpperCase();
    let line = t("checkingData");
    let cls = "";
    let pct = 0;
    if (account.needsLogin) { line = t("signInExpired"); cls = "bad"; }
    else if (account.exhausted) { line = t("outOfData") + (account.quotaReset ? ` · ${fmtDateMs(account.quotaReset)}` : ""); cls = "warn"; pct = 100; }
    else if (account.quotaLimit > 0) { line = `${fmtBytes(account.quotaLeft)} ${t("remaining").toLowerCase()} · ${fmtBytes(account.quotaLimit)}`; pct = Math.min(100, ((account.quotaLimit - account.quotaLeft) / account.quotaLimit) * 100); }
    const action = account.active ? `<span class="acct-badge">${esc(t("active"))}</span>` : `<button class="acct-use" data-use="${esc(account.email)}" type="button">${esc(t("use"))}</button>`;
    return `<div class="acct ${account.active ? "active" : ""}">
      <span class="avatar">${esc(initial)}</span>
      <span class="acct-body"><b dir="auto">${esc(account.email || t("accountsTitle"))}</b><small class="${cls}">${esc(line)}</small>${account.quotaLimit > 0 ? `<span class="mini-bar"><i style="width:${pct.toFixed(1)}%"></i></span>` : ""}</span>
      ${action}<button class="icon-btn account-remove" data-remove="${esc(account.email)}" type="button" title="${esc(t("close"))}" aria-label="${esc(t("close"))}"><svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg></button>
    </div>`;
  }).join("");
}

const AUTOSTART_LABEL = { windows: "startWithWindows", macos: "openAtLogin", linux: "startAtLogin" };
function lanBypassSupported() {
  if (!snap || snap.platform !== "android") return true;
  const apiLevel = Number(document.documentElement.dataset.androidApi || 0);
  return !apiLevel || apiLevel >= 33;
}
function applyPlatform() {
  if (!snap) return;
  const features = snap.features || {};
  document.documentElement.dataset.platform = snap.platform || "windows";
  $(".win-buttons").hidden = !features.windowControls;
  const modeSwitch = $("#mode-switch");
  if (modeSwitch) {
    modeSwitch.hidden = !features.proxyMode;
    const card = modeSwitch.closest(".mode-card");
    if (card) card.hidden = !features.proxyMode;
  }
  const killRow = $("#kill-switch-row");
  if (killRow) killRow.hidden = !features.killSwitch;
  const autoStart = $("#set-autostart");
  if (autoStart) {
    autoStart.closest(".toggle-row").hidden = !features.autostart;
    const label = autoStart.closest(".toggle-row").querySelector("b");
    if (label) label.textContent = t(AUTOSTART_LABEL[snap.platform] || "startAtLogin");
  }
  const logFile = $("#btn-logfile");
  if (logFile) logFile.hidden = !features.logFile;
  const appFilter = $("#app-filter");
  if (appFilter) appFilter.hidden = !features.appFilter;
  const lanInput = $("#set-bypass-lan");
  const lanRow = lanInput && lanInput.closest(".toggle-row");
  if (lanInput && lanRow) {
    const supported = lanBypassSupported();
    lanInput.disabled = !supported;
    lanRow.classList.toggle("unsupported", !supported);
    lanRow.setAttribute("aria-disabled", supported ? "false" : "true");
    const detail = lanRow.querySelector("small");
    if (detail) detail.textContent = supported ? t("bypassLanDetail") : t("bypassLanUnsupported");
  }
  const mobile = snap.platform === "android" || snap.platform === "ios";
  $("#login-direct").hidden = !mobile;
  $("#login-web").hidden = mobile;
  $("#login-hint").hidden = mobile;
}

function render() {
  if (!snap) return;
  applyPlatform();
  renderAccounts();
  if (webLoginPending && snap.loggedIn) { webLoginPending = false; addingAccount = false; }
  if (!snap.loggedIn || addingAccount) {
    showLogin();
    return;
  }
  $("#login-view").hidden = true;
  $("#app-shell").hidden = false;
  const accountEmail = snap.email || t("accountsTitle");
  $("#account-email").textContent = accountEmail;
  $("#avatar").textContent = (snap.email || "Z")[0].toUpperCase();
  const count = $("#acct-count");
  count.hidden = (snap.accounts || []).length < 2;
  count.textContent = "+" + Math.max(0, (snap.accounts || []).length - 1);
  $("#sidebar-version").textContent = snap.version ? `v${snap.version}` : "";
  renderHome();
  renderSettings();
  updateNavigation();
  renderStatistics();
  renderActivity();
}

function renderHome() {
  if (!snap) return;
  const status = snap.status || "disconnected";
  const hasError = !!snap.error;
  const visualStatus = hasError && status === "disconnected" ? "error" : status;
  const statusKeys = {
    disconnected: ["stateDisconnected", "tapToConnect", "connect"],
    connecting: ["stateConnecting", "securingConnection", "connect"],
    connected: ["stateConnected", "protectedNow", "disconnect"],
    disconnecting: ["stateDisconnecting", "disconnecting", "disconnect"],
    reconnecting: ["stateReconnecting", "reconnecting", "disconnect"],
  }[status] || ["stateDisconnected", "tapToConnect", "connect"];
  $("#orb").dataset.status = visualStatus;
  $("#orb").setAttribute("aria-label", t(statusKeys[2]));
  $("#command-label").textContent = t(statusKeys[2]);
  $("#status-text").textContent = t(statusKeys[0]);
  $("#status-sub").textContent = hasError ? snap.error : status === "reconnecting" && snap.settings.kill_switch ? `${t("reconnecting")} · ${t("killSwitch")}` : t(statusKeys[1]);
  $("#home-connection-label").textContent = t(statusKeys[0]);
  $("#status-mode").textContent = t(snap.settings.mode === "tun" ? "fullVpn" : "systemProxy").toUpperCase();
  $("#home-route-mode").textContent = t(snap.settings.mode === "tun" ? "fullVpn" : "systemProxy");
  document.body.classList.toggle("connected", status === "connected");
  document.body.classList.toggle("reconnecting", status === "reconnecting");
  clearInterval(timerHandle);
  if (status === "connected") {
    const tick = () => { $("#session-clock").textContent = snap.connectedAt ? fmtDuration(Date.now() - snap.connectedAt) : ""; };
    tick();
    timerHandle = setInterval(tick, 1000);
  } else $("#session-clock").textContent = "";

  const loc = locationName();
  let flagCode = loc.code;
  let name = loc.name;
  let sub = loc.sub;
  if (status === "connected" && snap.server && snap.server.countryCode !== "REC") {
    flagCode = snap.server.countryCode;
    name = snap.server.countryName;
    sub = snap.server.cityName;
  } else if (status === "connected" && snap.exitCountry) {
    flagCode = snap.exitCountry;
    sub = `${t("recommended")} · ${snap.exitCountry}`;
  }
  setFlag($("#loc-flag"), flagCode);
  $("#loc-name").textContent = sub && sub !== name ? `${name} · ${sub}` : name;
  const key = snap.settings.country_code && snap.settings.city_code ? `${snap.settings.country_code}/${snap.settings.city_code}` : null;
  const locationPing = key ? latency[key] : null;
  const ping = $("#loc-ping");
  ping.className = `ping ${pingClass(locationPing)}`;
  ping.textContent = pingText(locationPing);
  ping.hidden = locationPing == null;
  $("#exit-ip").textContent = status === "connected" ? (snap.exitIp || t("checkingData")) : "—";

  const accounts = snap.accounts || [];
  const active = accounts.find((account) => account.active) || {};
  const limit = snap.quotaLimit || active.quotaLimit || 0;
  const left = snap.quotaLimit ? snap.quotaLeft : active.quotaLeft || 0;
  if (limit > 0) {
    $("#quota-text").textContent = `${fmtBytes(left)} ${t("remaining").toLowerCase()} · ${fmtBytes(limit)}`;
    $("#quota-fill").style.width = `${Math.min(100, ((limit - left) / limit) * 100).toFixed(1)}%`;
  } else {
    $("#quota-text").textContent = t("noQuota");
    $("#quota-fill").style.width = "0";
  }
  const known = accounts.filter((account) => account.quotaLimit > 0);
  $("#total-row").hidden = accounts.length < 2 || !known.length;
  if (known.length) {
    const totalLeft = known.reduce((sum, account) => sum + (account.exhausted ? 0 : account.quotaLeft), 0);
    const totalLimit = known.reduce((sum, account) => sum + account.quotaLimit, 0);
    $("#total-text").textContent = `${fmtBytes(totalLeft)} / ${fmtBytes(totalLimit)}`;
  }
  const banner = $("#banner");
  if (status === "reconnecting" && snap.settings.kill_switch) {
    banner.className = "banner info";
    banner.innerHTML = `${esc(t("killSwitch"))}: ${esc(t("reconnecting"))}. <button id="banner-disconnect" type="button">${esc(t("disconnect"))}</button>`;
    banner.hidden = false;
    $("#banner-disconnect").onclick = () => api().Disconnect();
  } else if (snap.error) {
    banner.className = "banner";
    banner.textContent = snap.error;
    banner.hidden = false;
  } else if (snap.settings.mode === "tun" && !snap.elevated && status === "disconnected") {
    banner.className = "banner info";
    banner.innerHTML = `${esc(t("fullVpn"))}: ${esc(t("administratorNeeded"))}. <button id="banner-elevate" type="button">${esc(t("restartAsAdmin"))}</button>`;
    banner.hidden = false;
    $("#banner-elevate").onclick = elevate;
  } else banner.hidden = true;
  renderLiveMetrics();
}

function renderLiveMetrics() {
  const total = (lastStats.up || 0) + (lastStats.down || 0);
  $("#rate-down").textContent = `${fmtBytes(lastStats.downRate)}/s`;
  $("#rate-up").textContent = `${fmtBytes(lastStats.upRate)}/s`;
  $("#total-down").textContent = `${t("currentSession")} · ${fmtBytes(lastStats.down)}`;
  $("#total-up").textContent = `${t("currentSession")} · ${fmtBytes(lastStats.up)}`;
  $("#home-session-total").textContent = fmtBytes(total);
  $("#stats-total").textContent = fmtBytes(total);
  $("#stats-down-rate").textContent = `${fmtBytes(lastStats.downRate)}/s`;
  $("#stats-up-rate").textContent = `${fmtBytes(lastStats.upRate)}/s`;
  $("#stats-down").textContent = fmtBytes(lastStats.down);
  $("#stats-up").textContent = fmtBytes(lastStats.up);
  const active = snap && (snap.accounts || []).find((account) => account.active);
  const quotaLimit = lastStats.quotaLimit || (active && active.quotaLimit) || 0;
  const quotaLeft = lastStats.quotaLimit ? lastStats.quotaLeft : (active && active.quotaLeft) || 0;
  const quota = quotaLimit > 0 ? `${fmtBytes(quotaLeft)} / ${fmtBytes(quotaLimit)}` : t("noQuota");
  $("#stats-quota").textContent = quota;
  $("#stats-quota-fill").style.width = quotaLimit > 0 ? `${Math.min(100, ((quotaLimit - quotaLeft) / quotaLimit) * 100).toFixed(1)}%` : "0";
  renderLiveChart();
}

function renderLiveChart() {
  const empty = $("#chart-empty");
  const line = $("#chart-line");
  const area = $("#chart-area");
  if (!line || !area || sessionSamples.length < 2) {
    if (empty) empty.hidden = false;
    if (line) line.hidden = true;
    if (area) area.hidden = true;
    return;
  }
  const width = 720;
  const height = 190;
  const max = Math.max(...sessionSamples.map((sample) => sample.upRate + sample.downRate), 1);
  const points = sessionSamples.map((sample, index) => {
    const x = (index / Math.max(1, sessionSamples.length - 1)) * width;
    const y = height - ((sample.upRate + sample.downRate) / max) * (height - 16) - 8;
    return [x, y];
  });
  const d = points.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  line.setAttribute("d", d);
  area.setAttribute("d", `${d} L ${width} ${height} L 0 ${height} Z`);
  line.hidden = false;
  area.hidden = false;
  empty.hidden = true;
}

function renderSettings() {
  if (!snap) return;
  const settings = snap.settings || {};
  $("#set-bypass-ir").checked = !!settings.bypass_iran;
  $("#set-bypass-lan").checked = lanBypassSupported() && !!settings.bypass_lan;
  $("#set-autoconnect").checked = !!settings.auto_connect;
  $("#set-autoswitch").checked = !!settings.auto_switch;
  $("#set-killswitch").checked = !!settings.kill_switch;
  $("#set-tls-split").value = settings.tls_split || "auto";
  $("#set-autostart").checked = !!snap.autostart;
  appMode = settings.app_tunnel_mode || "all";
  selectedApps = (settings.apps || []).slice();
  for (const button of $$("#set-apptunnel button")) button.classList.toggle("active", button.dataset.mode === appMode);
  $("#apps-count").textContent = selectedApps.length ? `· ${selectedApps.length}` : "";
  if (document.activeElement !== $("#set-port")) $("#set-port").value = settings.proxy_port || "";
  $("#set-language").value = locale;
  $("#set-effects").checked = effectsEnabled;
  $("#about").innerHTML = `ZEUS VPN ${esc(snap.version || "")} · ${esc(snap.proxyAddr || `127.0.0.1:${settings.proxy_port || ""}`)}<br>${esc(t("protectedByMozilla"))} · <a href="#" id="link-github">GitHub</a>`;
  $("#link-github").onclick = (event) => { event.preventDefault(); api().OpenURL("https://github.com/Arefxxxx/Zeus-VPN"); };
}

function renderServers() {
  const list = $("#server-list");
  if (!list || !snap) return;
  const search = $("#server-search").value.trim().toLowerCase();
  const settings = snap.settings || {};
  const selectedCountry = settings.country_code || "REC";
  if (!servers.length) {
    list.innerHTML = `<div class="list-msg">${esc(t("loadingServers"))}</div>`;
    return;
  }
  let html = "";
  for (const country of servers) {
    const recommended = country.code === "REC";
    const name = recommended ? t("recommended") : country.name;
    const cityMatches = country.cities.filter((city) => !search || city.name.toLowerCase().includes(search));
    if (search && !name.toLowerCase().includes(search) && !cityMatches.length) continue;
    const pings = country.cities.map((city) => latency[`${country.code}/${city.code}`]).filter((value) => value != null && value >= 0);
    const best = pings.length ? Math.min(...pings) : (country.cities.some((city) => latency[`${country.code}/${city.code}`] === -1) ? -1 : null);
    const open = openCountry === country.code || (search && cityMatches.length && !recommended);
    const selected = selectedCountry === country.code && (!settings.city_code || recommended);
    const sub = recommended ? t("fastestAvailable") : `${country.cities.length} ${country.cities.length === 1 ? t("city") : t("cities")}`;
    html += `<div class="srv-group"><button class="srv ${selected ? "selected" : ""} ${open ? "open" : ""}" data-country="${esc(country.code)}" type="button"><span class="flag" ${recommended ? "" : `style="background-image:url(flags/${esc(country.code.toLowerCase())}.svg)"`}>${recommended ? GLOBE : ""}</span><span class="name"><b>${esc(name)}</b><small>${esc(sub)}</small></span>${best != null ? `<span class="ping ${pingClass(best)}">${esc(pingText(best))}</span>` : ""}${!recommended && country.cities.length > 1 ? `<span class="expand" data-expand="${esc(country.code)}">${CHEVRON}</span>` : ""}</button>`;
    if (open && !recommended && country.cities.length > 1) {
      html += '<div class="cities">';
      for (const city of (search ? cityMatches : country.cities)) {
        const ms = latency[`${country.code}/${city.code}`];
        const selectedCity = selectedCountry === country.code && settings.city_code === city.code;
        html += `<button class="city ${selectedCity ? "selected" : ""}" data-country="${esc(country.code)}" data-city="${esc(city.code)}" type="button"><span>${esc(city.name)}</span>${ms != null ? `<span class="ping ${pingClass(ms)}">${esc(pingText(ms))}</span>` : ""}</button>`;
      }
      html += "</div>";
    }
    html += "</div>";
  }
  list.innerHTML = html || `<div class="list-msg">${esc(t("noMatches"))}</div>`;
}

async function loadServers(force) {
  try {
    const list = await api().GetServers(!!force);
    list.sort((a, b) => (a.code === "REC" ? -1 : b.code === "REC" ? 1 : a.name.localeCompare(b.name)));
    servers = list;
    renderServers();
    renderHome();
  } catch (error) {
    $("#server-list").innerHTML = `<div class="list-msg">${esc(t("couldNotLoadServers"))}<br>${esc(errText(error))}</div>`;
  }
}
async function measureLatency() {
  if (pinging) return;
  pinging = true;
  $("#btn-ping").classList.add("spinning");
  try {
    latency = await api().GetLatency();
    renderServers();
    renderHome();
  } catch (error) {
    toast(errText(error));
  } finally {
    pinging = false;
    $("#btn-ping").classList.remove("spinning");
  }
}

async function loadUsage() {
  if (usageLoading) return;
  usageLoading = true;
  usageError = "";
  usageData = null;
  renderStatistics();
  renderActivity();
  try {
    usageData = await api().GetUsage(0);
  } catch (error) {
    usageData = null;
    usageError = errText(error);
  }
  try { usageApps = snap && snap.platform === "windows" ? await api().GetAppUsage() : []; } catch (error) { usageApps = []; }
  usageLoading = false;
  renderStatistics();
  renderActivity();
}
function usageTotals() {
  const data = usageData || [];
  return { down: data.reduce((sum, item) => sum + (item.down || 0), 0), up: data.reduce((sum, item) => sum + (item.up || 0), 0) };
}
function destinationRows(data, limit = 0) {
  if (usageError) return `<div class="list-msg state-message error-state"><span class="empty-mark">!</span><strong>${esc(t("usageUnavailable"))}</strong><small>${esc(t("usageUnavailableDetail"))}</small><small class="technical-value">${esc(usageError)}</small><button class="btn secondary compact" type="button" data-retry-usage>${esc(t("retryUsage"))}</button></div>`;
  if (data == null) return `<div class="list-msg state-message"><span class="loading-glyph" aria-hidden="true"></span><span>${esc(t("loading"))}</span></div>`;
  const rows = data.slice(0, limit || data.length);
  if (!rows.length) return `<div class="list-msg"><span class="empty-mark">◆</span><br>${esc(t("recordedNoData"))}</div>`;
  return rows.map((item) => `<div class="destination-row"><div class="destination-main"><span class="destination-rank">${String(rows.indexOf(item) + 1).padStart(2, "0")}</span><div><b dir="auto" title="${esc(item.host)}">${esc(item.host)}</b><small>${esc(t("lastActive"))}${item.last ? ` · ${esc(fmtDateSeconds(item.last))}` : ""}</small></div></div><div class="destination-values"><span>↓ ${esc(fmtBytes(item.down))}</span><span>↑ ${esc(fmtBytes(item.up))}</span><strong>${esc(fmtBytes((item.up || 0) + (item.down || 0)))} </strong></div></div>`).join("");
}
function appRows() {
  if (!usageApps.length) return `<div class="list-msg">${esc(t("noUsage"))}</div>`;
  return usageApps.map((app) => `<div class="destination-row app-usage-row"><div class="destination-main"><span class="destination-rank">◆</span><div><b dir="auto">${esc(app.name)}</b><small>↓ ${esc(fmtBytes(app.down))} · ↑ ${esc(fmtBytes(app.up))}</small></div></div><strong>${esc(fmtBytes((app.up || 0) + (app.down || 0)))}</strong></div>`).join("");
}
function renderStatistics() {
  if (!snap) return;
  const status = snap.status || "disconnected";
  $("#stats-session-state").textContent = status === "connected" ? t("stateConnected") : t("notConnected");
  $("#stats-live-label").textContent = status === "connected" ? t("oneSecondSamples") : t("session");
  const quota = lastStats.quotaLimit > 0 ? `${fmtBytes(lastStats.quotaLeft)} / ${fmtBytes(lastStats.quotaLimit)}` : t("noQuota");
  $("#stats-quota").textContent = quota;
  const totals = usageTotals();
  $("#stats-destinations").innerHTML = destinationRows(usageData, 8);
  $("#stats-usage-note").textContent = usageError ? `${t("usageUnavailable")}: ${usageError}` : usageLoading ? t("loading") : usageData && usageData.length ? t("destinationNote") : t("recordedNoData");
  const appsPanel = $("#stats-apps");
  appsPanel.hidden = !(snap.platform === "windows" && usageApps.length);
  if (!appsPanel.hidden) $("#stats-apps-list").innerHTML = appRows();
  renderLiveMetrics();
  void totals;
}
function renderActivity() {
  if (!snap) return;
  const totals = usageTotals();
  $("#activity-usage-total-down").textContent = usageData ? fmtBytes(totals.down) : "—";
  $("#activity-usage-total-up").textContent = usageData ? fmtBytes(totals.up) : "—";
  $("#activity-usage-total").textContent = usageData ? fmtBytes(totals.down + totals.up) : "—";
  const query = $("#activity-usage-search") ? $("#activity-usage-search").value.trim().toLowerCase() : "";
  const filtered = (usageData || []).filter((item) => !query || String(item.host || "").toLowerCase().includes(query));
  $("#activity-usage-list").innerHTML = usageData == null ? destinationRows(null) : destinationRows(filtered);
}
async function loadLogs() {
  if (logsLoading) return;
  logsLoading = true;
  logsError = "";
  const box = $("#logs");
  if (box) box.innerHTML = `<div class="list-msg state-message"><span class="loading-glyph" aria-hidden="true"></span><span>${esc(t("loading"))}</span></div>`;
  try {
    const lines = await api().GetLogs();
    if (!box) return;
    box.innerHTML = "";
    if (!lines.length) box.innerHTML = `<div class="list-msg">${esc(t("noLogs"))}</div>`;
    for (const line of lines) appendLog(line);
    logsLoaded = true;
  } catch (error) {
    logsLoaded = false;
    logsError = errText(error);
    if (box) box.innerHTML = `<div class="list-msg state-message error-state"><span class="empty-mark">!</span><strong>${esc(t("logsUnavailable"))}</strong><small>${esc(t("logsUnavailableDetail"))}</small><small class="technical-value">${esc(logsError)}</small><button class="btn secondary compact" type="button" data-retry-logs>${esc(t("retryLogs"))}</button></div>`;
  } finally {
    logsLoading = false;
  }
}
function appendLog(line) {
  const box = $("#logs");
  if (!box) return;
  const empty = box.querySelector(".list-msg");
  if (empty) empty.remove();
  const row = document.createElement("div");
  row.className = / warning /.test(line) ? "warning" : / error /.test(line) ? "error" : "info";
  row.textContent = line;
  box.appendChild(row);
  if (box.children.length > 500) box.firstChild.remove();
}

async function saveSettings() {
  const settings = { ...snap.settings };
  settings.bypass_iran = $("#set-bypass-ir").checked;
  settings.bypass_lan = $("#set-bypass-lan").checked;
  settings.auto_connect = $("#set-autoconnect").checked;
  settings.auto_switch = $("#set-autoswitch").checked;
  settings.kill_switch = $("#set-killswitch").checked;
  settings.tls_split = $("#set-tls-split").value;
  settings.proxy_port = parseInt($("#set-port").value, 10) || settings.proxy_port;
  try { await api().SaveSettings(settings); await refreshSnapshot(); } catch (error) { toast(errText(error)); render(); }
}
async function saveAppSettings() {
  try { await api().SaveSettings({ ...snap.settings, app_tunnel_mode: appMode, apps: selectedApps }); await refreshSnapshot(); } catch (error) { toast(errText(error)); render(); }
}
async function openAppsPanel() {
  for (const button of $$("#set-apptunnel button")) button.classList.toggle("active", button.dataset.mode === appMode);
  $("#app-search").value = "";
  $("#panel-scrim").hidden = false;
  $("#panel-apps").hidden = false;
  if (!installedApps) {
    $("#app-list").innerHTML = `<div class="list-msg">${esc(t("loading"))}</div>`;
    try { installedApps = await api().GetApps(); } catch (error) { installedApps = []; toast(errText(error)); }
  }
  renderApps();
}
function renderApps() {
  const list = $("#app-list");
  if (!installedApps) return;
  if (!installedApps.length) { list.innerHTML = `<div class="list-msg">${esc(t("noApps"))}</div>`; return; }
  const query = $("#app-search").value.trim().toLowerCase();
  const chosen = new Set(selectedApps);
  const rows = installedApps.filter((app) => !query || String(app.name || "").toLowerCase().includes(query) || String(app.id || "").toLowerCase().includes(query));
  list.innerHTML = rows.map((app) => `<label class="app-row"><input type="checkbox" data-app="${esc(app.id)}" ${chosen.has(app.id) ? "checked" : ""}>${app.icon ? `<img class="app-icon" alt="" loading="lazy" src="data:image/png;base64,${app.icon}">` : '<span class="app-icon empty"></span>'}<span class="name"><b>${esc(app.name)}</b><small dir="ltr">${esc(app.id)}</small></span></label>`).join("") || `<div class="list-msg">${esc(t("noMatches"))}</div>`;
}

async function toggleConnection() {
  if (!snap) return;
  try {
    if (snap.status === "disconnected") await api().Connect();
    else if (snap.status === "connected" || snap.status === "reconnecting") await api().Disconnect();
  } catch (error) {
    const message = errText(error);
    if (message.startsWith("ELEVATION_REQUIRED")) showAdminModal(message.endsWith("killswitch") ? "killswitch" : "tun");
    else toast(message);
  }
}
function showAdminModal(reason) {
  adminReason = reason;
  $("#admin-reason").textContent = reason === "killswitch" ? t("adminKill") : t("adminTun");
  $("#btn-use-proxy").textContent = reason === "killswitch" ? t("useSystemProxy") : t("useSystemProxy");
  $("#modal-admin").hidden = false;
}
async function elevate() {
  try { await api().RestartAsAdmin(); } catch (error) { toast(errText(error)); }
}
async function refreshSnapshot() {
  snap = await api().GetSnapshot();
  render();
  return snap;
}

function setIntroError(message) {
  $("#intro-error").hidden = false;
  $("#intro-error-message").textContent = message || t("introError");
  showIntroStatus("introError", "introReadingState");
}
function finishIntro() {
  if (introFinished) return;
  introFinished = true;
  clearTimeout(introTimer);
  const intro = $("#intro-screen");
  intro.classList.remove("leaving");
  intro.hidden = true;
  render();
}
function completeIntro() {
  $("#intro-error").hidden = true;
  showIntroStatus("introStateReady", "introLocalEngine");
  if (!effectsEnabled || window.matchMedia("(prefers-reduced-motion: reduce)").matches) { finishIntro(); return; }
  requestAnimationFrame(() => $("#intro-screen").classList.add("leaving"));
  introTimer = setTimeout(finishIntro, 480);
}
async function bootstrap() {
  showIntroStatus("introPreparing", "introReadingState");
  try {
    await refreshSnapshot();
    if (snap.loggedIn) loadServers(false);
    completeIntro();
  } catch (error) {
    setIntroError(errText(error));
  }
}

async function startWebLogin() {
  webLoginPending = true;
  try { localStorage.setItem("zeusAppURL", location.origin + location.pathname); } catch (e) {}
  $("#login-error").textContent = t("loading");
  try { await api().StartWebLogin(localStorage.getItem("zeusAppURL") || (location.origin + location.pathname)); } catch (error) { $("#login-error").textContent = errText(error); }
}
function loginStepUI(result) {
  loginStep = (result && result.step) || "done";
  $("#login-error").textContent = (result && result.message) || "";
  const needsCode = loginStep === "unblock" || loginStep === "code" || loginStep === "link";
  $("#login-code").hidden = !needsCode;
  $("#login-code").placeholder = loginStep === "unblock" ? "Unblock code from the email" : loginStep === "link" ? "Press continue after opening the link" : "6-digit code from the email";
  $("#login-direct-go span").textContent = needsCode ? t("confirm") : t("signIn");
  if (loginStep === "done") {
    $("#login-email").value = "";
    $("#login-password").value = "";
    $("#login-code").value = "";
    loginStep = null;
    addingAccount = false;
    render();
  }
}
async function directLogin() {
  const email = $("#login-email").value.trim();
  const password = $("#login-password").value;
  if (!email || !password) { $("#login-error").textContent = t("email") + " / " + t("password"); return; }
  $("#login-error").textContent = t("loading");
  try { loginStepUI(await api().Login(email, password)); } catch (error) { $("#login-error").textContent = errText(error); }
}
async function continueLogin() {
  const code = $("#login-code").value.trim();
  $("#login-error").textContent = t("loading");
  try {
    const result = loginStep === "unblock" ? await api().SubmitUnblock(code) : loginStep === "link" ? await api().ConfirmLink() : await api().VerifyCode(code);
    loginStepUI(result);
  } catch (error) { $("#login-error").textContent = errText(error); }
}

function wire() {
  $("#btn-min").onclick = () => window.runtime.WindowMinimise();
  $("#btn-close").onclick = () => window.runtime.Quit();
  $("#orb").onclick = toggleConnection;
  $("#link-signup").onclick = (event) => { event.preventDefault(); api().OpenURL("https://accounts.firefox.com/signup"); };
  $("#login-web").onclick = startWebLogin;
  $("#login-direct-go").onclick = () => (loginStep ? continueLogin() : directLogin());
  $("#login-code").addEventListener("keydown", (event) => { if (event.key === "Enter") $("#login-direct-go").click(); });
  $("#login-cancel").onclick = () => { addingAccount = false; loginStep = null; try { api().CancelLogin(); } catch (e) {} render(); navigate("settings", false); };

  for (const button of $$('button[data-screen]')) button.onclick = () => navigate(button.dataset.screen);
  $("#btn-account").onclick = () => navigate("settings");
  $("#btn-settings").onclick = () => navigate("settings");
  $("#btn-location").onclick = () => navigate("locations");

  for (const button of $$("#mode-switch button")) button.onclick = async () => {
    if (button.dataset.mode === snap.settings.mode) return;
    try { await api().SetMode(button.dataset.mode); if (button.dataset.mode === "tun" && !snap.elevated && snap.status === "connected") showAdminModal("tun"); } catch (error) { toast(errText(error)); }
  };
  $("#btn-ping").onclick = measureLatency;
  $("#server-search").oninput = renderServers;
  $("#server-list").onclick = async (event) => {
    const expand = event.target.closest("[data-expand]");
    if (expand) { event.stopPropagation(); openCountry = openCountry === expand.dataset.expand ? null : expand.dataset.expand; renderServers(); return; }
    const item = event.target.closest("[data-country]");
    if (!item) return;
    try { await api().SetLocation(item.dataset.country === "REC" ? "" : item.dataset.country, item.dataset.city || ""); await refreshSnapshot(); navigate("home"); } catch (error) { toast(errText(error)); }
  };

  const settingIds = ["#set-bypass-ir", "#set-bypass-lan", "#set-autoconnect", "#set-autoswitch", "#set-killswitch", "#set-tls-split", "#set-port"];
  for (const id of settingIds) $(id).onchange = saveSettings;
  $("#set-autostart").onchange = async (event) => { try { await api().SetAutostart(event.target.checked); refreshSnapshot(); } catch (error) { toast(errText(error)); } };
  $("#set-language").onchange = (event) => setLocale(event.target.value);
  $("#set-effects").onchange = (event) => setEffects(event.target.checked);
  $("#btn-add-account").onclick = () => { addingAccount = true; render(); };
  $("#account-list").onclick = async (event) => {
    const use = event.target.closest("[data-use]");
    const remove = event.target.closest("[data-remove]");
    try {
      if (use) { await api().UseAccount(use.dataset.use); toast(`${t("nowUsing")} ${use.dataset.use}`); refreshSnapshot(); }
      else if (remove) {
        const email = remove.dataset.remove;
        if (!(await askConfirm(`${t("signInExpired")} · ${email}`, t("confirmation")))) return;
        await api().RemoveAccount(email); await refreshSnapshot();
      }
    } catch (error) { toast(errText(error)); }
  };
  for (const button of $$("#set-apptunnel button")) button.onclick = async () => {
    appMode = button.dataset.mode;
    for (const item of $$("#set-apptunnel button")) item.classList.toggle("active", item.dataset.mode === appMode);
    if (appMode === "all") { selectedApps = []; await saveAppSettings(); }
    else if (selectedApps.length) await saveAppSettings();
    else openAppsPanel();
  };
  $("#btn-choose-apps").onclick = openAppsPanel;
  $("#app-search").oninput = renderApps;
  $("#app-list").onchange = async (event) => {
    const id = event.target.dataset && event.target.dataset.app;
    if (!id) return;
    if (event.target.checked && !selectedApps.includes(id)) selectedApps.push(id);
    if (!event.target.checked) selectedApps = selectedApps.filter((item) => item !== id);
    $("#apps-count").textContent = selectedApps.length ? `· ${selectedApps.length}` : "";
    if (selectedApps.length) await saveAppSettings();
  };
  $("#btn-close-apps").onclick = closePanel;
  $("#panel-scrim").onclick = closePanel;

  $("#btn-usage").onclick = () => navigate("statistics");
  $("#activity-usage-search").oninput = renderActivity;
  $("#stats-destinations").onclick = (event) => { if (event.target.closest("[data-retry-usage]")) loadUsage(); };
  $("#activity-usage-list").onclick = (event) => { if (event.target.closest("[data-retry-usage]")) loadUsage(); };
  $("#logs").onclick = (event) => { if (event.target.closest("[data-retry-logs]")) loadLogs(); };
  $("#btn-usage-reset").onclick = async () => {
    if (!(await askConfirm(t("clearHistory"), t("confirmation")))) return;
    try { await api().ResetUsage(); usageData = []; usageApps = []; renderStatistics(); renderActivity(); toast(t("usageCleared")); } catch (error) { toast(errText(error)); }
  };
  $("#btn-logs").onclick = async () => { navigate("activity"); if (!logsLoaded) await loadLogs(); };
  $("#btn-copy-logs").onclick = async () => { if (!logsLoaded) await loadLogs(); try { await navigator.clipboard.writeText($("#logs").innerText); toast(t("logsCopied")); } catch (e) { toast(errText(e)); } };
  $("#btn-logfile").onclick = () => api().OpenLogFile();

  $("#btn-elevate").onclick = elevate;
  $("#btn-use-proxy").onclick = async () => {
    $("#modal-admin").hidden = true;
    try {
      if (adminReason === "killswitch") { await api().SaveSettings({ ...snap.settings, kill_switch: false }); await refreshSnapshot(); toggleConnection(); }
      else await api().SetMode("proxy");
    } catch (error) { toast(errText(error)); }
  };
  $("#modal-admin").onclick = (event) => { if (event.target.id === "modal-admin") $("#modal-admin").hidden = true; };
  $("#btn-confirm-yes").onclick = () => { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(true); confirmResolve = null; };
  $("#btn-confirm-no").onclick = () => { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(false); confirmResolve = null; };
  $("#modal-confirm").onclick = (event) => { if (event.target.id === "modal-confirm") { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(false); confirmResolve = null; } };
  $("#intro-retry").onclick = () => { $("#intro-error").hidden = true; bootstrap(); };
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { closePanel(); $("#modal-admin").hidden = true; if (!$("#modal-confirm").hidden) { $("#modal-confirm").hidden = true; if (confirmResolve) confirmResolve(false); confirmResolve = null; } }
  });
  document.addEventListener("contextmenu", (event) => { if (!event.target.closest("input, .logs")) event.preventDefault(); });
}

function handleState(state) {
  const previous = snap;
  snap = state;
  const wasConnected = previous && previous.status === "connected";
  if (snap.status === "connected" && !wasConnected) { sessionSamples = []; previousStats = { up: 0, down: 0 }; lastStats = { up: 0, down: 0, upRate: 0, downRate: 0, quotaLimit: 0, quotaLeft: 0 }; sessionActive = true; }
  if (snap.status === "disconnected") { sessionActive = false; sessionSamples = []; lastStats = { up: 0, down: 0, upRate: 0, downRate: 0, quotaLimit: 0, quotaLeft: 0 }; }
  if (introFinished) render();
}
function handleStats(stats) {
  if (stats.up < previousStats.up || stats.down < previousStats.down) sessionSamples = [];
  if (snap && snap.status === "connected") {
    sessionSamples.push({ at: Date.now(), upRate: Math.max(0, stats.upRate || 0), downRate: Math.max(0, stats.downRate || 0) });
    if (sessionSamples.length > 120) sessionSamples.shift();
  }
  previousStats = { up: stats.up || 0, down: stats.down || 0 };
  lastStats = stats;
  if (introFinished) { renderLiveMetrics(); if (currentScreen === "statistics") renderStatistics(); }
}

async function init() {
  wire();
  applyLocale();
  window.runtime.EventsOn("state", handleState);
  window.runtime.EventsOn("stats", handleStats);
  window.runtime.EventsOn("notice", (message) => toast(message, 6000));
  window.runtime.EventsOn("log", (line) => { if (logsLoaded || currentScreen === "activity") appendLog(line); });
  await bootstrap();
}

(function waitForBridge() {
  if (window.go && window.go.main && window.runtime) init().catch((error) => setIntroError(errText(error)));
  else setTimeout(waitForBridge, 20);
})();
