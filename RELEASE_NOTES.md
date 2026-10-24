# Zeus VPN 1.1.0.1

## What's new

- **Fixed Full VPN mode on the desktop.** The VPN server's own addresses are now kept off the virtual adapter's routes on every platform. Previously the app's own tunnel connection could be captured by the adapter it was carrying, so Full VPN connected but carried no traffic (endless `DNS lookup failed` lines in the log). A connection addressed to the VPN server itself is now refused instantly instead of recursing, and the protection follows the server on automatic failover.
- **Per-app tunnelling on Android.** Settings → **Full VPN apps**: tunnel only the apps you choose, or everything except a few (banking apps, local services, …), picked from a searchable list. Other platforms route all apps: per-app VPN on iOS needs MDM profiles, and the desktop TUN stack has no per-app filter.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 | `zeusvpn_1.1.0.1_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **حالت Full VPN در نسخه‌ی دسکتاپ فیکس شد:** قبلاً وصل می‌شد ولی هیچ سایتی باز نمی‌شد (باگ حلقه‌ی مسیریابی). الان آدرس خود سرور VPN از مسیرهای آداپتور مجازی حذف می‌شود و مشکل برطرف شده است.
- **تونل انتخابی اپ‌ها در اندروید:** از تنظیمات ← Full VPN apps می‌توانید فقط اپ‌های دلخواه را از VPN رد کنید (یا همه به‌جز چند اپ خاص).
- نسخه‌های **مک و iOS هنوز بتا** هستند؛ اول تستشان کنید و بعد استفاده کنید.

---

*Previous release notes: [v1.1.0.0](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.0)*
