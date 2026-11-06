# Zeus VPN 1.2.0.0 (upcoming — draft)

## What's new

- **Signing in works again, on every platform.** Mozilla's servers refuse the old direct sign-in method for plain clients, so the app now answers Mozilla's edge check by itself: **you sign in with a simple email/password form right in the app** — no web page anywhere, on the desktop just like on the phone. If Mozilla asks for an emailed code or a safety unblock code, the form walks you through it.
- **Fixed connecting on networks that hijack the VPN-server handshake** ("certificate signed by unknown authority"): the app now recognizes the forged certificate and repeats the handshake with the server name hidden, exactly the trick it already uses for blocked websites.
- **Fixed connecting failing after a brief network blip** — the sign-in token refresh now retries with a fresh connection instead of failing the connect.
- **Adding a second account while connected** now works reliably: the sign-in goes through the active tunnel.
- **Fixed reconnecting immediately after a disconnect** failing with a "port already in use" error.
- **Existing accounts are restored silently at startup** — if your session only needed a token refresh, you no longer see the sign-in screen at all.
- **Better compatibility and diagnostics**: the server list is compiled into the app as a last-resort (the server page is never empty), the VPN-server handshake now presents a browser-grade TLS fingerprint for filtered networks, and each connection stage (DNS, TCP, TLS, certificate, HTTP) is recorded — Settings → Network compatibility → Connection diagnostics.

**Known issues**

- Updating from an older version may ask for **one sign-in** — the account file moves to the device's secure store (Android Keystore / iOS Keychain) in this release.

Everything from earlier releases is on board: the data usage panel, honest server pings, live remaining-data refresh, and release-signed Android builds.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 (Ubuntu/Debian/Fedora 36+) | `zeusvpn_1.2.0.0_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| Linux x86-64 (CentOS/RHEL 9, older WebKitGTK) | `ZeusVPN-linux-amd64-gtk40.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **ورود دوباره کار می‌کند — روی همه‌ی پلتفرم‌ها.** سرورهای موزیلا روش مستقیم قبلی را برای کلاینت‌های معمولی می‌بندند؛ حالا برنامه خودش پاسخ چالش لبه‌ی موزیلا را می‌دهد: با یک **فرم ساده‌ی ایمیل/رمز داخل خود برنامه** وارد می‌شوید — روی کامپیوتر هم مثل گوشی، بدون هیچ صفحه‌ی وب. اگر موزیلا کد ایمیلی یا کد امنیتی بخواهد، همان فرم مرحله‌به‌مرحله راهنمایی می‌کند.
- **رفع خطای «certificate signed by unknown authority» موقع وصل‌شدن** روی بعضی شبکه‌ها: برنامه گواهی جعلی را تشخیص می‌دهد و هندشیک را با مخفی‌کردن نام سرور تکرار می‌کند.
- **رفع شکست اتصال بعد از یک قطعی کوتاه شبکه** — تمدید توکن حالا با اتصال تازه دوباره تلاش می‌کند.
- **افزودن اکانت دوم در حالت اتصال‌دار** حالا از داخل تونل انجام می‌شود و درست کار می‌کند.
- **رفع مشکل وصل‌شدن مجدد بعد از قطع** (خطای اشغال بودن پورت).
- اکانت‌های قبلی موقع باز شدن برنامه بی‌صدا تمدید می‌شوند و دیگر صفحه‌ی لاگین را نمی‌بینید.
- **سازگاری و عیب‌یابی بهتر**: فهرست سرورها داخل خود برنامه ذخیره شده (صفحه‌ی سرورها دیگر خالی نمی‌ماند)، هندشیک تونل با اثرانگشت مرورگر انجام می‌شود و هر مرحله‌ی اتصال (DNS، TCP، TLS، گواهی، HTTP) ثبت می‌شود — تنظیمات → Network compatibility → Connection diagnostics.

**نکات شناخته‌شده**

- آپدیت از نسخه‌های قبلی ممکن است **یک بار ورود مجدد** بخواهد — فایل اکانت‌ها در این نسخه به Keystore/Keychain دستگاه منتقل می‌شود.
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.9](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.9)*

> This draft is still being prepared — the redesign is landing in it before the final release.
