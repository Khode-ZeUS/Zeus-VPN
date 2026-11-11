# Cloudflare Worker relay pilot

This Worker is a first hop for the Windows app. It accepts encrypted WebSocket connections and opens TCP connections only to the Mozilla sign-in, server discovery and Fastly VPN endpoints in the allowlist. TLS between the app and Mozilla stays inside the connection.

1. From this directory, run `npx.cmd wrangler login` and sign in to your Cloudflare account. Run `npx.cmd wrangler deploy` to publish the Worker and create its rate limit binding. Wrangler prints the assigned `workers.dev` URL. No VPS or custom domain is required.
2. Close any running instance of Zeus VPN. In the same PowerShell session, set a random pilot secret on the Worker and start the test build. The application reads the secret from its process environment.

   ```powershell
   $env:ZEUS_WORKER_PILOT_TOKEN = node -p "require('node:crypto').randomBytes(32).toString('hex')"
   $env:ZEUS_WORKER_PILOT_TOKEN | npx.cmd wrangler secret put RELAY_TOKEN
   & ..\build\bin\ZeusVPN-Windows-custom.exe
   ```

3. In the Windows app, open **Connection relay** on the sign-in screen or in **Settings** and enter your own Worker URL with the `wss://` scheme. Select **System Proxy** or **Full VPN**. Windows asks for administrator access for Full VPN.
4. Test Firefox sign-in and then connect to the VPN. A Worker HTTP 502 means the Cloudflare runtime could not open the requested TCP destination. Cloudflare blocks raw TCP to destinations on its own IP ranges, so deployment alone does not establish that the Mozilla flow works.

After the Worker first accepts a WebSocket, the Windows test build saves the pilot key with Windows DPAPI for that Worker URL. The app can then be opened normally. To replace a revoked key, repeat step 2. Use the same Worker URL, including its path, so the app can find the saved key.

If Mozilla Guardian returns HTTP 451 with `unavailable_in_region`, retry after deploying the current configuration. It asks Cloudflare to run the Worker near AWS US East. The placement hint does not guarantee the country Mozilla assigns to the Worker's outbound IP. Redeploying the Worker with this change keeps the existing URL and secret.

Each user can deploy their own Worker and set their own key. The Worker limits permitted destinations, connections per source IP, and bytes and duration per connection. It closes sessions after 64 MiB or 15 minutes. Keep the key out of binaries and source control.

## Managed Windows build

The release can point to a project-owned Worker. Users sign in to Mozilla and connect without setting up Cloudflare. A custom relay in Settings still takes precedence; clearing it restores the build default. This route is public when enabled. It rejects browser-origin WebSocket connections, limits traffic to the same destinations as the pilot, and rate limits connections by source IP. It is disabled by default.

From the Worker directory, run `npx.cmd wrangler deploy --var MANAGED_RELAY:enabled` to enable `/managed`. Redeploy without that variable to disable it (do not set the variable persistently in the dashboard). Use the non-personal project hostname. The existing root route still requires its pilot secret.

From the repository root, build Windows with an endpoint selected at build time:

```powershell
wails build -tags with_gvisor -skipbindings -trimpath -ldflags "-s -w -X main.managedRelayURL=wss://zeusvpn-relay.zeus-vpn.workers.dev/managed" -o ZeusVPN-Windows-managed.exe
```

Before sharing the build, confirm that `https://<worker-host>/managed` returns HTTP 426 and test sign-in, System Proxy, and Full VPN on a clean Windows profile. HTTP 426 only confirms that the route is enabled. Mozilla may still ask for human verification or reject the Worker's region.
