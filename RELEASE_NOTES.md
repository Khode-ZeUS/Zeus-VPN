# Zeus VPN 1.1.0.6

## What's new

- **More resilient server discovery.** The VPN server's addresses are now collected from all DNS-over-HTTPS resolvers and merged, instead of taking the first answer. If one resolver reports an address that the network has filtered or tampered with, the app automatically tries the other addresses on the next attempt instead of failing with a TLS error.

Everything from earlier releases is on board: the Full VPN routing-loop fix, per-app tunnelling with app icons, honest server pings, mobile layout fixes and release-signed APKs.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 | `zeusvpn_1.1.0.6_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **پایداری بیشتر در پیدا کردن سرور:** آدرس‌های سرور VPN از هر سه DNS رمزنگاری‌شده گرفته و با هم ترکیب می‌شوند؛ اگر یکی از آدرس‌ها توسط فیلترینگ دستکاری شده باشد، برنامه خودش آدرس‌های دیگر را امتحان می‌کند (خطای `certificate signed by unknown authority` گذرای قبلی دیگر قطع‌کننده‌ی اتصال نیست).
- اگر چنین خطایی دیدید، یک بار Disconnect و دوباره Connect کنید — با این نسخه معمولاً بدون تلاش دوباره وصل می‌شود.
- نسخه‌های **مک و iOS هنوز بتا** هستند.

---

*Previous release: [v1.1.0.5](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.5)*
