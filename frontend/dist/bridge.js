"use strict";

// On Android and iOS the UI runs in the app's WebView and talks to the Go
// library through the native app instead of Wails. This recreates the two
// objects app.js uses: window.go.main.App (calls return promises) and
// window.runtime (events). On the desktop it does nothing.
(function () {
  const android = window.ZeusAndroid;
  const ios = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.zeus;
  if (!android && !ios) return;

  let seq = 0;
  const pending = new Map();
  const listeners = {};
  const post = (msg) => (android ? android.post(JSON.stringify(msg)) : ios.postMessage(JSON.stringify(msg)));
  const parse = (s) => (s === "" || s == null ? null : JSON.parse(s));

  // Called by the native app.
  window.__zeus = {
    resolve(id, ok, payload) {
      const p = pending.get(id);
      if (!p) return;
      pending.delete(id);
      if (ok) p.resolve(parse(payload));
      else p.reject(payload);
    },
    emit(name, payload) {
      const data = parse(payload);
      for (const f of listeners[name] || []) f(data);
    },
  };

  const call = (method, args) =>
    new Promise((resolve, reject) => {
      const id = ++seq;
      pending.set(id, { resolve, reject });
      post({ id, method, args });
    });

  window.go = { main: { App: new Proxy({}, { get: (_, method) => (...args) => call(method, args) }) } };
  window.runtime = {
    EventsOn(name, f) {
      (listeners[name] = listeners[name] || []).push(f);
    },
    WindowMinimise() {},
    Quit() {},
  };
  document.documentElement.classList.add("mobile", android ? "android" : "ios");
})();
