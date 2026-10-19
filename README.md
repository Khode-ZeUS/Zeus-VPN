<p align="center">
  <img src="docs/banner.png" alt="Zeus VPN for Windows" width="100%">
</p>

<h1 align="center">⚡ Zeus VPN</h1>

<p align="center">
  A native, installable Windows client for Mozilla's VPN network.<br>
  Sign in with a free Mozilla account, pick a country, press one button.
</p>

<p align="center">
  <img alt="Platform" src="https://img.shields.io/badge/platform-Windows%2010%20%7C%2011-0078D6?logo=windows">
  <img alt="Go" src="https://img.shields.io/badge/built%20with-Go-00ADD8?logo=go&logoColor=white">
  <img alt="License" src="https://img.shields.io/badge/license-MIT-7c5cff">
</p>

---

## Features

- **Two connection modes**
  - **System Proxy**: routes browsers and most apps; no administrator rights needed.
  - **Full VPN (TUN)**: a virtual network adapter that tunnels *every* app on the system, like WireGuard or v2rayN's TUN mode.
- **Mozilla account sign-in** with email verification codes, confirmation links and unblock codes all handled in the app.
- **Multiple accounts with auto-switch**: add as many Mozilla accounts as you like. When one runs out of its monthly data, Zeus VPN moves to the account with the most data left, without dropping the connection.
- **30+ countries and cities**, with live latency tests, search and an automatic *Recommended* option.
- **Anti-filtering**: TLS ClientHello fragmentation hides server names from SNI filters (automatic when a handshake is blocked), and VPN server addresses are resolved over DNS-over-HTTPS.
- **DNS over HTTPS** through the tunnel in Full VPN mode, so DNS lookups are not visible to or poisoned by the local network.
- **Smart routing**: optionally keep `.ir` sites and your local network (printers, router) off the VPN.
- **Live stats**: download/upload speed, exit IP and your remaining monthly data (50 GB free per month).
- **Local HTTP + SOCKS5 proxy** on `127.0.0.1:10820` for apps that need manual proxy settings (Telegram, browsers, git, ...).
- **Safe by design**: your previous proxy settings (for example v2rayN's) are restored on disconnect, even after a crash; sign-in tokens are encrypted with Windows DPAPI.
- **Kill switch**: if the VPN drops, the Windows firewall blocks all other traffic until Zeus VPN reconnects or you disconnect, so nothing leaks outside the tunnel (optional, needs administrator).
- **Self-healing connection**: dropped connections are detected and restored automatically (moving to another server if needed), the short-lived proxy pass is renewed in the background, and a rejected pass is replaced on the spot.
- Single small executable, start with Windows, connect on launch, logs saved to `%APPDATA%\ZeusVPN\zeusvpn.log`.

<p align="center">
  <img src="docs/screen-connected.png" width="200" alt="Connected">
  &nbsp;
  <img src="docs/screen-settings.png" width="200" alt="Accounts">
  &nbsp;
  <img src="docs/screen-servers.png" width="200" alt="Locations">
  &nbsp;
  <img src="docs/screen-login.png" width="200" alt="Sign in">
</p>

## Install

1. Download `ZeusVPN-amd64-installer.exe` from the [Releases](https://github.com/Amir-Mohammd-Asadjoo/Zeus-VPN/releases) page.
2. Run it. The installer adds Start menu and desktop shortcuts, and installs Microsoft Edge WebView2 if it is missing.
3. Open **Zeus VPN**, sign in with your [Mozilla account](https://accounts.firefox.com/signup) and press the power button.

> **Full VPN mode** needs administrator rights to create the network adapter. Zeus VPN asks to restart itself as administrator when you select it.

## How it works

```
 apps ──► System proxy (HTTP/SOCKS5 on 127.0.0.1:10820) ─┐
                                                        ├─► HTTP/2 CONNECT tunnel ─► Mozilla VPN server ─► internet
 apps ──► Zeus VPN TUN adapter ─► userspace TCP/IP stack ─┘     (TLS, bearer proxy-pass)
```

1. Zeus VPN signs in to **Firefox Accounts** (the same onepw protocol Firefox uses) and obtains an OAuth token for the VPN scope.
2. It asks Mozilla's **Guardian** service for a short-lived *proxy pass* and renews it automatically before it expires.
3. Every TCP connection is carried as an **HTTP/2 CONNECT** stream to the selected Mozilla server; plain HTTP is forwarded as proxy requests.
4. In Full VPN mode a [WinTun](https://www.wintun.net/) adapter captures all traffic. Destinations are recovered from the TLS SNI / HTTP Host, so sites are resolved on the VPN side, and DNS is answered with DNS over HTTPS through the tunnel.

## Build from source

Requirements: Go 1.26+, the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation), and [NSIS](https://nsis.sourceforge.io/) for the installer.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build -tags with_gvisor -trimpath -ldflags "-s -w" -nsis
```

The app is written to `build/bin/ZeusVPN.exe` and the installer to `build/bin/ZeusVPN-amd64-installer.exe`.

| Path | What it does |
|---|---|
| `internal/mozilla` | Firefox Accounts login, Fastly challenge solver, Guardian proxy pass, server list |
| `internal/tunnel` | HTTP/2 CONNECT session pool, local HTTP/SOCKS proxy |
| `internal/vpn` | TUN mode: adapter, routing, traffic sniffing, DNS over HTTPS |
| `internal/sysproxy` | Windows system proxy with backup and restore |
| `internal/core` | Connection lifecycle used by the UI |
| `frontend/dist` | The UI (plain HTML, CSS and JavaScript) |

## راهنمای فارسی

**Zeus VPN** یک برنامه‌ی ویندوزی برای وصل شدن به سرورهای VPN موزیلا است. فقط با یک اکانت رایگان موزیلا وارد می‌شوید و ماهانه ۵۰ گیگابایت ترافیک رایگان دارید. دیگر نیازی به پیدا کردن کانفیگ v2ray نیست.

- **System Proxy**: مرورگرها و بیشتر برنامه‌ها از VPN رد می‌شوند و دسترسی ادمین لازم ندارد.
- **Full VPN**: همه‌ی برنامه‌های سیستم (حتی بازی‌ها و برنامه‌هایی که پروکسی را پشتیبانی نمی‌کنند) از VPN رد می‌شوند. اولین بار برنامه برای دسترسی ادمین سؤال می‌کند.
- **چند اکانت**: می‌توانید چند اکانت موزیلا اضافه کنید (تنظیمات ← Add another account). وقتی حجم یکی تمام شود، برنامه خودش بدون قطع شدن اتصال به اکانت بعدی سوییچ می‌کند.
- **کیل سوییچ (Kill switch)**: اگر VPN قطع شود، اینترنت کل سیستم قطع می‌شود تا وقتی که VPN دوباره وصل شود یا خودتان آن را خاموش کنید؛ پس هیچ ترافیکی بیرون از VPN رد نمی‌شود. از تنظیمات فعال می‌شود و دسترسی ادمین لازم دارد.
- **ضد فیلترینگ**: اگر فیلترینگ اتصال به سرورها را ببندد، برنامه خودش TLS را تکه‌تکه (fragment) می‌فرستد تا اسم سرور (SNI) دیده نشود.
- سایت‌های `.ir` به‌صورت پیش‌فرض مستقیم باز می‌شوند (قابل تغییر در تنظیمات).
- DNS در حالت Full VPN از داخل تونل و به‌صورت رمزنگاری‌شده (DoH) انجام می‌شود، پس آلودگی DNS مشکلی ایجاد نمی‌کند.
- برای برنامه‌هایی مثل تلگرام می‌توانید پروکسی `127.0.0.1:10820` (HTTP یا SOCKS5) را دستی تنظیم کنید.

**نصب:** فایل `ZeusVPN-amd64-installer.exe` را از بخش Releases دانلود و اجرا کنید، وارد اکانت موزیلا شوید و دکمه‌ی اتصال را بزنید.

## Credits

- [Vauth/FoxyVPN](https://github.com/Vauth/FoxyVPN): the original FoxyVPN client whose protocol work this project is based on.
- [WireGuard for Windows](https://git.zx2c4.com/wireguard-windows/): the Windows Filtering Platform code behind the kill switch (MIT).
- [Mozilla VPN](https://www.mozilla.org/products/vpn/): the VPN network and account system.
- [Wails](https://wails.io/), [sing-tun](https://github.com/SagerNet/sing-tun), [WinTun](https://www.wintun.net/) and [flag-icons](https://github.com/lipis/flag-icons).

## Disclaimer

Zeus VPN is an independent open-source project and is not affiliated with or endorsed by Mozilla. "Mozilla" and "Firefox" are trademarks of the Mozilla Foundation. Use of Mozilla's service is subject to Mozilla's terms.

## License

[MIT](LICENSE)
