# Zeus VPN 1.1.0.7

## What's new

- **Server pings no longer show timeouts while connections work.** The latency probe now races every resolved address of a server (the way real connections do) and retries a handshake the network cut off with fragmented TLS — the same trick live connections use. DNS-over-HTTPS is resolved before the clock starts, so slow resolvers no longer turn into a fake "timeout". Still a real per-server handshake measurement, never a made-up number.
- **New: data usage panel** (Settings). Everything the VPN carried is tracked per destination — websites, APIs, downloads — with a live list, search, totals and a reset button. On Windows, connections are also attributed to the application that made them: tap an app to unfold the exact sites it used, each with its own share. The history survives restarts. It doesn't track anything outside the VPN servers, so its total corresponds to the account's monthly data.
- **In-app confirmation dialogs** replace the browser's native prompts, which rendered off-frame in the WebView.
- **Accurate remaining data.** The monthly data row now counts down live while connected, and the app re-reads the server's remaining data every 5 minutes (new device, browser extension), so shared accounts no longer show stale numbers.

## Downloads

| Platform | File |
|---|---|
| Windows 10/11 | `ZeusVPN-amd64-installer.exe` (or the portable `ZeusVPN.exe`) |
| Android 7+ | `ZeusVPN-android-arm64-v8a.apk` (most phones), `-armeabi-v7a` or `-universal` |
| Linux x86-64 | `zeusvpn_1.1.0.7_amd64.deb` or `ZeusVPN-linux-amd64.tar.gz` |
| macOS (beta) | `ZeusVPN-macos-universal-beta.dmg` |
| iOS / iPadOS (beta) | `ZeusVPN-ios-unsigned-beta.ipa` |

> The **macOS and iOS builds are still beta** and have not been fully tested on this release; the Windows, Linux and Android builds are stable. If something doesn't work, please open an issue with the log (Settings → View logs).

## راهنمای فارسی

- **پینگ سرورها دیگر وقتی اتصال واقعی کار می‌کند، تایم‌اوت نشان نمی‌دهد:** ابزار اندازه‌گیری حالا همه‌ی آدرس‌های یک سرور را با هم مسابقه می‌دهد و اگر هندشیک اول توسط فیلترینگ قطع شود، دقیقاً مثل اتصال واقعی با TLS تکه‌تکه دوباره امتحان می‌کند. پینگ همچنان اندازه‌گیری واقعیِ خودِ سرور است — هیچ عدد ساختگی‌ای نمایش داده نمی‌شود.
- **بخش جدید «مصرف داده» در تنظیمات:** هر مقصدی که نت از طریق VPN مصرف کرده، چقدر دانلود و چقدر آپلود — با جست‌وجو و دکمه‌ی صفر کردن تاریخچه. در ویندوز مصرف به اپ سازنده هم نسبت داده می‌شود: روی هر اپ بزنید تا سایت‌هایی که همان اپ مصرف کرده باز شود. تاریخچه بعد از ری‌استارت هم می‌ماند.
- پنجره‌های تأیید داخل برنامه‌ای شدند (دیالوگ خام WebView دیگر نمایش داده نمی‌شود).
- **دیتای باقی‌مانده دقیق‌تر شد:** عدد ماهانه هنگام اتصال به‌صورت زنده کم می‌شود و هر ۵ دقیقه مقدار واقعی سرور دوباره خوانده می‌شود؛ اگر از همان اکانت در اکستنشن مرورگر یا دستگاه دیگری هم استفاده کنید، حداکثر ۵ دقیقه بعد صحیح می‌شود (سرویس Mozilla هیچ API لحظه‌ای جداگانه‌ای برای مصرف لحظه‌ای ندارد؛ سرچ موارد مشابه هم به همین هدرها متکی است).

---

*Previous release: [v1.1.0.6](https://github.com/Khode-ZeUS/Zeus-VPN/releases/tag/v1.1.0.6)*
