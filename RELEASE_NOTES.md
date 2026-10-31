# Zeus VPN 1.1.0.8

## What's new

- **Signing in works again, on every platform.** Mozilla's servers refuse the old direct sign-in method for plain clients, so the app now answers Mozilla's edge check by itself: on **Windows and Linux the Sign in button opens Mozilla's official sign-in page inside the app window** (email, password and two-factor code as usual — and you come back signed in), while **on Android and iOS you sign in with a simple email/password form right in the app** — no web page needed. If Mozilla asks for an emailed code or a safety unblock code, the form walks you through it.
- **Adding a second account while connected** now works reliably: the sign-in goes through the active tunnel.
- **Fixed reconnecting immediately after a disconnect** failing with a "port already in use" error.
- **Existing accounts are restored silently at startup** — if your session only needed a token refresh, you no longer see the sign-in screen at all.
- On networks where Mozilla's endpoints cannot be reached directly, the app falls back to the VPN tunnel or a system-configured proxy (another VPN tool) automatically.

Everything from earlier releases is on board: the data usage panel, honest server pings, live remaining-data refresh, and release-signed Android builds.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 (Ubuntu/Debian/Fedora 36+) | `zeusvpn_1.1.0.8_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| Linux x86-64 (CentOS/RHEL 9, older WebKitGTK) | `ZeusVPN-linux-amd64-gtk40.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **ورود دوباره کار می‌کند — روی همه‌ی پلتفرم‌ها.** سرورهای موزیلا روش مستقیم قبلی را برای کلاینت‌های معمولی می‌بندند؛ حالا برنامه خودش پاسخ چالش لبه‌ی موزیلا را می‌دهد: روی **ویندوز و لینوکس** دکمه‌ی ورود، صفحه‌ی رسمی لاگین موزیلا را داخل همان پنجره باز می‌کند، و روی **اندروید و iOS** با یک فرم ساده‌ی ایمیل/رمز داخل خود برنامه وارد می‌شوید — بدون صفحه‌ی وب. اگر موزیلا کد ایمیلی یا کد امنیتی بخواهد، همان فرم مرحله‌به‌مرحله راهنمایی می‌کند.
- **افزودن اکانت دوم در حالت اتصال‌دار** حالا از داخل تونل انجام می‌شود و درست کار می‌کند.
- **رفع مشکل وصل‌شدن مجدد بعد از قطع** (خطای اشغال بودن پورت).
- اکانت‌های قبلی موقع باز شدن برنامه بی‌صدا تمدید می‌شوند و دیگر صفحه‌ی لاگین را نمی‌بینید.
- روی شبکه‌هایی که سرورهای موزیلا مستقیم باز نمی‌شوند، برنامه خودش از تونل یا پروکسی سیستم (ابزار VPN دیگر) استفاده می‌کند.
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.7](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.7)*
