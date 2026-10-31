# Zeus VPN 1.1.0.8

## What's new

- **Fixed the HTTP 406 error that blocked signing in.** Mozilla's servers now refuse the old direct sign-in method for every client, so the sign-in flow has changed: **the Sign in button opens Mozilla's official sign-in page inside the app window**, where you enter your email, password and two-factor code as usual — and come back signed in. The old email/password form was removed along with it.
- **Fixed reconnecting immediately after a disconnect** failing with a "port already in use" error.
- **Adding a second account while connected** now works reliably: the sign-in goes through the active tunnel.
- **Existing accounts are restored silently at startup** — if your session only needed a token refresh, you no longer see the sign-in screen at all.
- On networks where Mozilla's sign-in page cannot load directly, the app picks up a system-configured proxy (another VPN tool) automatically as a fallback.

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

- **خطای 406 موقع ورود رفع شد.** روش ورود عوض شده: دکمه‌ی ورود، صفحه‌ی رسمی لاگین موزیلا را داخل همان پنجره‌ی برنامه باز می‌کند؛ ایمیل، رمز و تأیید دومرحله‌ای را همان‌جا وارد کنید و بدون کاری اضافه برمی‌گردید. فرم قدیمی ایمیل/رمز حذف شده است.
- **رفع مشکل وصل‌شدن مجدد بعد از قطع** (خطای اشغال بودن پورت).
- **افزودن اکانت دوم در حالت اتصال‌دار** حالا از داخل تونل انجام می‌شود و درست کار می‌کند.
- اکانت‌های قبلی موقع باز شدن برنامه بی‌صدا تمدید می‌شوند و دیگر صفحه‌ی لاگین را نمی‌بینید.
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.7](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.7)*
