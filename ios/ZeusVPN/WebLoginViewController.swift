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
    private var hook = ""

    private static let pageURL = URL(string:
        "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email")!

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
        // The shared hook (WebChannel + in-page exchange) comes from the Go
        // engine so every platform runs the same logic; this side only
        // provides the delivery bridge. The nonce argument only matters to
        // the desktop loopback delivery, so it is empty here.
        var hookError: NSError?
        hook = ZeuscoreCall("WebLoginHook",
            jsonArgs(["function(q) { window.webkit.messageHandlers.zeusWebLogin.postMessage(q); }",
                      ""]), &hookError)
        // The hook has to run BEFORE the page's own scripts: the content
        // server only boots its desktop WebChannel flow for a Firefox user
        // agent, so the hook's in-page spoof must land first — a didFinish
        // injection leaves the page spinning on its loading screen. The real
        // Safari HTTP user agent stays: Fastly fingerprints the API
        // password-login route, and the desktop build passes it with its real
        // browser UA.
        let documentStart = WKUserScript(source: hook,
                                         injectionTime: .atDocumentStart,
                                         forMainFrameOnly: true)
        webView.configuration.userContentController.addUserScript(documentStart)
        webView.load(URLRequest(url: Self.pageURL))
        navigationItem.rightBarButtonItem = UIBarButtonItem(
            title: "Close", style: .plain, target: self, action: #selector(closeTapped))
    }

    @objc private func closeTapped() { dismiss(animated: true) }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        // The shared hook delivers result queries ("tokens?...", "session?...",
        // "cancel") — mirror the desktop receiver.
        guard let q = message.body as? String else { return }
        let kind = q.split(separator: "?").first.map(String.init) ?? q
        let queryString = q.contains("?") ? String(q.split(separator: "?", maxSplits: 1)[1]) : ""
        var params: [String: String] = [:]
        for pair in queryString.split(separator: "&") {
            let kv = pair.split(separator: "=", maxSplits: 1)
            if kv.count == 2 {
                params[String(kv[0])] = String(kv[1]).removingPercentEncoding
            }
        }
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSError?
            switch kind {
            case "tokens":
                _ = ZeuscoreCall("CompleteWebLoginToken",
                    jsonArgs([params["email"] ?? "", params["access_token"] ?? "",
                              params["refresh_token"] ?? "", params["expires_in"] ?? "86400"]), &error)
            case "session":
                _ = ZeuscoreCall("CompleteWebLogin",
                    jsonArgs([params["email"] ?? "", params["token"] ?? ""]), &error)
            default:
                break
            }
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
