import { connect } from "cloudflare:sockets";

const HTTPS_HOSTS = new Set([
  "accounts.firefox.com",
  "accounts-static.cdn.mozilla.net",
  "firefox.settings.services.mozilla.com",
  "product-details.mozilla.org",
  "vpn.mozilla.org",
]);
const FRAME_SIZE = 64 * 1024;
const MAX_QUEUED = 512 * 1024;
const MAX_BYTES = 64 * 1024 * 1024;
const SESSION_MS = 15 * 60 * 1000;
const encoder = new TextEncoder();

function authorized(request, token) {
  const expected = encoder.encode("Bearer " + token);
  const provided = encoder.encode(request.headers.get("Authorization") ?? "");
  return expected.length === provided.length && crypto.subtle.timingSafeEqual(expected, provided);
}
function destination(raw) {
  const match = /^([a-z0-9.-]+):(443|2499)$/.exec(raw ?? "");
  if (!match) return null;
  const [, hostname, port] = match;
  if (port === "443" && (HTTPS_HOSTS.has(hostname) ||
      /^[a-z0-9-]+\.accounts\.firefox\.com$/.test(hostname))) {
    return { hostname, port: 443 };
  }
  if (port === "2499" && /^[a-z0-9-]+\.m1\.fastly-masque\.net$/.test(hostname)) {
    return { hostname, port: 2499 };
  }
  return null;
}

function bridge(ws, socket) {
  const reader = socket.readable.getReader();
  const writer = socket.writable.getWriter();
  let closed = false;
  let queue = Promise.resolve();
  let queued = 0;
  let received = 0;
  let sent = 0;
  const timer = setTimeout(() => finish(1008), SESSION_MS);

  function finish(code) {
    if (closed) return;
    closed = true;
    clearTimeout(timer);
    if (ws.readyState === WebSocket.OPEN) ws.close(code);
    socket.close().catch(() => {});
  }

  ws.binaryType = "arraybuffer";
  ws.accept();
  ws.addEventListener("close", () => finish(1000));
  ws.addEventListener("error", () => finish(1011));
  ws.addEventListener("message", ({ data }) => {
    if (closed) return;
    if (!(data instanceof ArrayBuffer) || data.byteLength > FRAME_SIZE) {
      finish(1003);
      return;
    }
    const size = data.byteLength;
    queued += size;
    received += size;
    if (queued > MAX_QUEUED || received + sent > MAX_BYTES) {
      finish(1008);
      return;
    }
    queue = queue.then(async () => {
      if (!closed) await writer.write(new Uint8Array(data));
      queued -= size;
    }).catch(() => finish(1011));
  });

  (async () => {
    while (!closed) {
      const { done, value } = await reader.read();
      if (done || closed) break;
      sent += value.byteLength;
      if (received + sent > MAX_BYTES) {
        finish(1008);
        return;
      }
      for (let i = 0; i < value.byteLength; i += FRAME_SIZE) {
        ws.send(value.subarray(i, i + FRAME_SIZE));
      }
    }
    finish(1000);
  })().catch(() => finish(1011));
  socket.closed.catch(() => finish(1011));
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === "/managed/fxa") {
      if (env.MANAGED_RELAY !== "enabled" || request.headers.has("Origin")) {
        return new Response("Forbidden", { status: 403 });
      }
      let target;
      try { target = new URL(request.headers.get("X-Zeus-Fxa-Target")); }
      catch { return new Response("Invalid target", { status: 400 }); }
      if (target.protocol !== "https:" || target.port || target.username || target.password ||
          !["accounts.firefox.com", "api.accounts.firefox.com"].includes(target.hostname) ||
          !["GET", "POST"].includes(request.method)) {
        return new Response("Destination not allowed", { status: 403 });
      }
      const ip = request.headers.get("CF-Connecting-IP");
      if (!ip || !env.RELAY_LIMITER) return new Response("Rate limit unavailable", { status: 503 });
      if (!(await env.RELAY_LIMITER.limit({ key: "fxa:" + ip })).success) {
        return new Response("Rate limit exceeded", { status: 429 });
      }
      const headers = new Headers();
      for (const name of ["Accept", "Accept-Encoding", "Accept-Language", "Content-Type", "Authorization", "Cookie", "User-Agent", "Origin", "Referer"]) {
        const value = request.headers.get(name === "Origin" ? "X-Zeus-Fxa-Origin" : name);
        if (value !== null) headers.set(name, value);
      }
      let body;
      if (request.method === "POST") {
        const length = Number(request.headers.get("Content-Length"));
        if (!Number.isSafeInteger(length) || length < 1 || length > 65536) {
          return new Response("Invalid body size", { status: 413 });
        }
        body = await request.arrayBuffer();
        if (body.byteLength !== length) return new Response("Invalid body size", { status: 400 });
      }
      try {
        const upstream = await fetch(target, { method: request.method, headers, body, redirect: "manual" });
        const responseHeaders = new Headers(upstream.headers);
        responseHeaders.set("Cache-Control", "no-store");
        responseHeaders.set("X-Zeus-Fxa-Upstream", "1");
        return new Response(upstream.body, { status: upstream.status, headers: responseHeaders });
      } catch {
        return new Response("Firefox Accounts is unavailable", { status: 502 });
      }
    }
    const managed = url.pathname === "/managed";
    if (request.method !== "GET" || (url.pathname !== "/" && !managed)) {
      return new Response("Not found", { status: 404 });
    }
    if (managed && env.MANAGED_RELAY !== "enabled") {
      return new Response("Not found", { status: 404 });
    }
    if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return new Response("Zeus VPN relay is running. Use a WebSocket.", { status: 426 });
    }
    if (managed) {
      if (request.headers.has("Origin") || request.headers.has("Authorization")) {
        return new Response("Forbidden", { status: 403 });
      }
    } else {
      if (!env.RELAY_TOKEN || env.RELAY_TOKEN.length < 32) {
        return new Response("Relay access is not configured", { status: 503 });
      }
      if (!authorized(request, env.RELAY_TOKEN)) {
        return new Response("Unauthorized", { status: 401 });
      }
    }
    const target = destination(url.searchParams.get("target"));
    if (!target) return new Response("Destination not allowed", { status: 403 });
    const ip = request.headers.get("CF-Connecting-IP");
    if (!ip || !env.RELAY_LIMITER) {
      return new Response("Relay rate limit is unavailable", { status: 503 });
    }
    if (!(await env.RELAY_LIMITER.limit({ key: (managed ? "managed:" : "pilot:") + ip })).success) {
      return new Response("Too many connections", { status: 429 });
    }
    let socket;
    let timer;
    try {
      // TLS to Mozilla is established inside this socket by the Windows app.
      socket = connect(target);
      socket.closed.catch(() => {});
      await Promise.race([
        socket.opened,
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("timeout")), 4000); }),
      ]);
    } catch {
      if (socket) socket.close().catch(() => {});
      return new Response("Destination unavailable", { status: 502 });
    } finally {
      clearTimeout(timer);
    }
    const [client, server] = Object.values(new WebSocketPair());
    bridge(server, socket);
    return new Response(null, { status: 101, webSocket: client });
  },
};
