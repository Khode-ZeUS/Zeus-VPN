# Zeus VPN 1.1.0.5

## What's new

- **Less scary Play Protect.** The app no longer asks to see every installed package (only the launchable apps, the way WireGuard for Android does it). The VPN + all-packages combination is what stalkerware looks like to Google's scanner, so this should stop the "harmful app" flagging for most users.

- **The Android APKs are properly release-signed** (since 1.1.0.4). From this release on, every update installs over the previous version, and Google Play Protect stops flagging each download as an unknown app.

> **One-time step when upgrading from 1.1.0.3 or older:** uninstall the old app first (its signature was a throw-away debug key), then install this release. After that, all future updates install in place — your settings and accounts stay, but the sign-in has to be redone once.

No other changes; 1.1.0.3's honest server pings and mobile layout fixes are on board.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 | `zeusvpn_1.1.0.5_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **هشدار «اپ مخرب» پلی‌پروتکتکت کاهش می‌یابد:** برنامه دیگر دسترسی دیدن همه‌ی اپ‌های نصب‌شده را نمی‌خواهد (فقط اپ‌های قابل اجرا، مثل WireGuard)؛ ترکیب VPN + دسترسی به همه‌ی اپ‌ها نزد اسکنر گوگل شبیه نرم‌افزار جاسوسی بود.
- آپدیت‌ها مستقیم روی نسخه‌ی قبلی نصب می‌شوند (اگر هنوز نسخه‌ی 1.1.0.3 یا قدیمی‌تر را دارید، فقط همان یک بار حذف و نصب کنید).
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.4](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.4)*
