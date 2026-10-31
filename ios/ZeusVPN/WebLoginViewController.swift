import Foundation
import UIKit
import WebKit
import Zeuscore

/// Signs in on the Firefox Accounts web page. The API password-login route is
/// edge-blocked (HTTP 406), but the page signs in on its own origin and hands
/// the session token to its embedder through the FxA WebChannel; an injected
/// hook forwards it here, and the Go engine exchanges it for tokens.
final class WebLoginViewController: UIViewController, WKScriptMessageHandler, WKNavigationDelegate {
    private var webView: WKWebView!
    private var pendingToken: String?
    private var pendingEmail = ""

    private static let pageURL = URL(string:
        "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email")!

    private static let hook = """
(function() {
  if (window.__zeusHooked) return; window.__zeusHooked = true;
  var reply = function(messageId, command, data) {
    var envelope = {id: 'account_updates', message: {messageId: messageId, command: command, data: data}};
    [window, document].forEach(function(t) {
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: envelope})); } catch (e) {}
      try { t.dispatchEvent(new CustomEvent('WebChannelMessageToContent', {detail: JSON.stringify(envelope)})); } catch (e) {}
    });
  };
  var zeusExchange = function(sessionToken, email) {
    var enc = new TextEncoder();
    var b64 = function(buf) { var s = btoa(String.fromCharCode.apply(null, new Uint8Array(buf))); return s; };
    crypto.subtle.importKey('raw', new Uint8Array(sessionToken.match(/.{2}/g).map(function(h) { return parseInt(h, 16); })), 'HKDF', false, ['deriveBits'])
      .then(function(base) {
        return crypto.subtle.deriveBits({name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(32),
          info: enc.encode('identity.mozilla.com/picl/v1/sessionToken')}, base, 512);
      })
      .then(function(expanded) {
        expanded = new Uint8Array(expanded);
        var tokenID = Array.prototype.map.call(expanded.slice(0, 32), function(x) { return ('0' + x.toString(16)).slice(-2); }).join('');
        var ts = String(Math.floor(Date.now() / 1000));
        var nb = crypto.getRandomValues(new Uint8Array(6));
        var nonce = b64(nb).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
        var body = JSON.stringify({client_id: '5882386c6d801776', grant_type: 'fxa-credentials',
          scope: 'profile https://identity.mozilla.com/apps/vpn', access_type: 'offline'});
        return crypto.subtle.digest('SHA-256', enc.encode('hawk.1.payload\napplication/json\n' + body + '\n'))
          .then(function(ph) {
            var payloadHash = b64(ph);
            var normalized = ['hawk.1.header', ts, nonce, 'POST', '/v1/oauth/token',
              'api.accounts.firefox.com', '443', payloadHash, '', ''].join('\n');
            return crypto.subtle.importKey('raw', expanded.slice(32), {name: 'HMAC', hash: 'SHA-256'}, false, ['sign'])
              .then(function(hk) { return crypto.subtle.sign('HMAC', hk, enc.encode(normalized)); })
              .then(function(mac) {
                var header = 'Hawk id="' + tokenID + '", ts="' + ts + '", nonce="' + nonce +
                  '", mac="' + mac + '", hash="' + payloadHash + '"';
                return fetch('https://api.accounts.firefox.com/v1/oauth/token', {method: 'POST',
                  headers: {'Content-Type': 'application/json', 'Authorization': header}, body: body});
              });
          })
          .then(function(res) {
            if (!res.ok) throw new Error('HTTP ' + res.status);
            return res.json();
          })
          .then(function(out) {
            window.webkit.messageHandlers.zeusWebLogin.postMessage(JSON.stringify(
              {command: 'tokens', access: out.access_token || '', refresh: out.refresh_token || '',
               expires: String(out.expires_in || 86400), email: email}));
          });
      })
      .catch(function(e) {
        window.webkit.messageHandlers.zeusWebLogin.postMessage(JSON.stringify(
          {command: 'login', token: sessionToken, email: email, verified: true}));
      });
  };
  var forward = function(detail) {
    try {
      var host = window.location.hostname;
      if (host !== 'accounts.firefox.com' && !host.endsWith('.firefox.com')) return;
      var msg = {}; try { msg = (typeof detail === 'string' ? JSON.parse(detail) : detail).message || {}; } catch (e) {}
      var cmd = msg.command || ''; var data = msg.data || {};
      if (cmd === 'fxaccounts:fxa_status') { reply(msg.messageId, cmd, {capabilities:{}, signedInUser:null}); return; }
      if (cmd === 'fxaccounts:can_link_account') { reply(msg.messageId, cmd, {ok:true}); return; }
      var token = String(data.sessionToken || data.token || '').trim();
      var verified = (data.verified === undefined) ? true : !!data.verified;
      if (cmd === 'fxaccounts:login' || cmd === 'fxaccounts:oauth_login' || /verified/i.test(cmd)) {
        if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
        if (!token) return;
        if (verified) {
          zeusExchange(token, data.email || '');
        } else {
          window.webkit.messageHandlers.zeusWebLogin.postMessage(JSON.stringify(
            {command: cmd, token: token, email: data.email || '', verified: verified}));
        }
        return;
      }
      if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
    } catch (e) {}
  };
  window.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
  document.addEventListener('WebChannelMessageToChrome', function(e) { forward(e.detail); });
  var pingSuccess = function() {
    try {
      var u = window.location.href.toLowerCase();
      if (u.indexOf('/verify') !== -1 || u.indexOf('/totp') !== -1 || u.indexOf('/signin_confirmed') !== -1 ||
          u.indexOf('connect_another_device') !== -1 || u.indexOf('/settings') !== -1 || u.indexOf('/complete') !== -1) {
        window.webkit.messageHandlers.zeusWebLogin.postMessage(JSON.stringify({command: 'verified', token: '', email: ''}));
      }
    } catch (e) {}
  };
  if (window.fetch) {
    var origFetch = window.fetch;
    window.fetch = function() {
      var url = (arguments[0] && arguments[0].url) || arguments[0] || '';
      return origFetch.apply(this, arguments).then(function(res) {
        try { if (res && res.ok && String(url).indexOf('/session/verify') !== -1) pingSuccess(); } catch (e) {}
        return res;
      });
    };
  }
  var nav = function() { try { pingSuccess(); } catch (e) {} };
  var ps = history.pushState; if (ps) history.pushState = function() { var r = ps.apply(this, arguments); nav(); return r; };
  var rs = history.replaceState; if (rs) history.replaceState = function() { var r = rs.apply(this, arguments); nav(); return r; };
  window.addEventListener('popstate', nav);
})();
"""

    override func loadView() {
        webView = WKWebView(frame: .zero)
        view = webView
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(red: 9 / 255, green: 11 / 255, blue: 26 / 255, alpha: 1)
        webView.configuration.userContentController.add(WeakScript(self), name: "zeusWebLogin")
        webView.customUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
        webView.navigationDelegate = self
        webView.load(URLRequest(url: Self.pageURL))
        navigationItem.rightBarButtonItem = UIBarButtonItem(
            title: "Close", style: .plain, target: self, action: #selector(closeTapped))
    }

    @objc private func closeTapped() { dismiss(animated: true) }

    func webView(_ wv: WKWebView, didFinish navigation: WKNavigation!) {
        wv.evaluateJavaScript(Self.hook, completionHandler: nil)
    }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let raw = message.body as? String,
              let msg = try? JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any]
        else { return }
        let command = msg["command"] as? String ?? ""
        let token = (msg["token"] as? String ?? "").trimmingCharacters(in: .whitespaces)
        let email = msg["email"] as? String ?? ""
        let verified = msg["verified"] as? Bool ?? true

        if command == "tokens" {
            let access = msg["access"] as? String ?? ""
            let refresh = msg["refresh"] as? String ?? ""
            let expires = msg["expires"] as? String ?? ""
            completeLoginTokens(email: email, access: access, refresh: refresh, expires: expires)
            return
        }

        let isToken = token.count == 64 && token.allSatisfy { $0.isHexDigit }
        if isToken && (verified || pendingToken != nil) {
            pendingToken = nil
            completeLogin(email: email, sessionToken: token)
        } else if isToken {
            pendingToken = token
            pendingEmail = email
        } else if command == "verified" || command == "fxaccounts:login" {
            if let held = pendingToken, held.count == 64 {
                pendingToken = nil
                completeLogin(email: pendingEmail, sessionToken: held)
            }
        }
    }

    private func completeLogin(email: String, sessionToken: String) {
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSError?
            _ = ZeuscoreCall("CompleteWebLogin", jsonArgs([email, sessionToken]), &error)
            DispatchQueue.main.async { [weak self] in
                self?.dismiss(animated: true)
            }
        }
    }

    private func completeLoginTokens(email: String, access: String, refresh: String, expires: String) {
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSError?
            _ = ZeuscoreCall("CompleteWebLoginToken", jsonArgs([email, access, refresh, expires]), &error)
            DispatchQueue.main.async { [weak self] in
                self?.dismiss(animated: true)
            }
        }
    }
}

private func jsonArgs(_ items: [String]) -> String {
    let data = try? JSONSerialization.data(withJSONObject: items)
    return String(data: data ?? Data("[]".utf8), encoding: .utf8) ?? "[]"
}

/// Keeps the script handler alive while the webview exists.
private final class WeakScript: NSObject, WKScriptMessageHandler {
    weak var target: WKScriptMessageHandler?
    init(_ target: WKScriptMessageHandler) { self.target = target }
    func userContentController(_ c: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(c, didReceive: message)
    }
}
