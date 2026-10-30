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
        window.webkit.messageHandlers.zeusWebLogin.postMessage(JSON.stringify(
          {command: cmd, token: token, email: data.email || '', verified: verified}));
        if (msg.messageId) reply(msg.messageId, cmd, {ok:true});
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
