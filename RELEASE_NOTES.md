# Zeus VPN 1.1.0.3

## What's new

- **Honest server pings.** The latency test now completes a full TCP + TLS handshake (the same path a real connection takes) instead of a bare TCP connect, so filters that answer the TCP handshake themselves no longer show a fake single-digit ping. Unreachable servers show *timeout*.
- **Mobile layout fixes.** The title no longer slides under the status bar (battery/clock area) and settings content no longer hides behind the navigation bar: the app now feeds the real system-bar sizes to the page on every Android version, including Android 15's edge-to-edge mode, and in landscape with a display cutout.
- **Release signing for Android (maintainer note).** CI now supports a proper release keystore through repository secrets; with it set, updates install over the previous version and Play Protect warns far less. See *Build from source → Android* in the README for the one-time setup.

Everything from earlier releases is still on board: the Full VPN routing-loop fix on desktop, per-app tunnelling on Android with app icons in the picker.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 | `zeusvpn_1.1.0.3_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **پینگ واقعی:** تست سرعت سرورها حالا تا پایان TLS handshake ادامه پیدا می‌کند؛ دیگر به‌خاطر جواب جعلیِ فیلتر، پینگِ الکی چند میلی‌ثانیه‌ای نشان داده نمی‌شود.
- **چیدمان موبایل فیکس شد:** عنوان برنامه دیگر زیر نوار وضعیت (باتری و ساعت) و انتهای تنظیمات دیگر زیر نوار دکمه‌های گوشی نمی‌رود.
- برای ورود به برنامه [اکانت رایگان موزیلا](https://accounts.firefox.com/signup) بسازید (فقط ایمیل لازم است).
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.2](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.2)*
