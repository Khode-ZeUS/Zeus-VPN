import UIKit
import WebKit
import Zeuscore

/// Shows the shared web UI (frontend/dist, bundled as "dist") and connects
/// it to the Go engine: frontend/dist/bridge.js posts calls to the "zeus"
/// message handler and receives results and events through window.__zeus.
final class WebViewController: UIViewController, WKScriptMessageHandler, WKNavigationDelegate {
    private var webView: WKWebView!
    private let queue = DispatchQueue(label: "zeus.calls", attributes: .concurrent)

    override var preferredStatusBarStyle: UIStatusBarStyle { .lightContent }

    override func loadView() {
        let config = WKWebViewConfiguration()
        config.userContentController.add(WeakHandler(self), name: "zeus")
        webView = WKWebView(frame: .zero, configuration: config)
        webView.isOpaque = false
        webView.backgroundColor = UIColor(red: 9 / 255, green: 11 / 255, blue: 26 / 255, alpha: 1)
        webView.scrollView.backgroundColor = webView.backgroundColor
        webView.scrollView.bounces = false
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        webView.navigationDelegate = self
        view = webView
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        CoreHost.shared.web = self
        if let index = Bundle.main.url(forResource: "index", withExtension: "html", subdirectory: "dist") {
            webView.loadFileURL(index, allowingReadAccessTo: index.deletingLastPathComponent())
        }
    }

    // MARK: - Bridge

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let body = message.body as? String,
              let data = body.data(using: .utf8),
              let msg = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let id = msg["id"] as? Int,
              let method = msg["method"] as? String
        else { return }
        let args = msg["args"] ?? []
        let argsJSON = (try? JSONSerialization.data(withJSONObject: args)).flatMap { String(data: $0, encoding: .utf8) } ?? "[]"
        queue.async {
            var error: NSError?
            let result = ZeuscoreCall(method, argsJSON, &error)
            DispatchQueue.main.async {
                if let error = error {
                    self.resolve(id, ok: false, error.localizedDescription)
                } else {
                    self.resolve(id, ok: true, result)
                }
            }
        }
    }

    private func resolve(_ id: Int, ok: Bool, _ payload: String) {
        webView.evaluateJavaScript("window.__zeus.resolve(\(id), \(ok), \(jsString(payload)))")
    }

    func emit(_ name: String, _ payload: String) {
        webView.evaluateJavaScript("window.__zeus && window.__zeus.emit(\(jsString(name)), \(jsString(payload)))")
    }

    private func jsString(_ s: String) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: [s]),
              let json = String(data: data, encoding: .utf8)
        else { return "\"\"" }
        return String(json.dropFirst().dropLast())
    }

    // Links (sign-up page, GitHub) open in Safari.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let url = action.request.url, url.scheme == "http" || url.scheme == "https" {
            UIApplication.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }
}

/// WKUserContentController keeps its handlers alive; this avoids a cycle.
private final class WeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: WKScriptMessageHandler?
    init(_ target: WKScriptMessageHandler) { self.target = target }
    func userContentController(_ c: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(c, didReceive: message)
    }
}
