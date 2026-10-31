import io

p = "ios/ZeusVPN/WebLoginViewController.swift"
s = io.open(p, encoding="utf-8").read()

# 1. drop the hardcoded hook constant
start = s.find("    private static let hook = \"\"\"")
end = s.find('"""', s.find("(function()", start))
assert start != -1 and end != -1
end += 3
if s[end] == "\n":
    end += 1
s = s[:start] + s[end:]

# 2. add the hook property + deliver shim + fetch in viewDidLoad
old = """    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(red: 9 / 255, green: 11 / 255, blue: 26 / 255, alpha: 1)
        webView.configuration.userContentController.add(WeakScript(self), name: "zeusWebLogin")
        webView.customUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
        webView.navigationDelegate = self
        webView.load(URLRequest(url: Self.pageURL))"""
new = """    private var hook = ""

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(red: 9 / 255, green: 11 / 255, blue: 26 / 255, alpha: 1)
        webView.configuration.userContentController.add(WeakScript(self), name: "zeusWebLogin")
        webView.customUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
        webView.navigationDelegate = self
        // The shared hook (WebChannel + in-page exchange) comes from the Go
        // engine so every platform runs the same logic; this side only
        // provides the delivery bridge.
        var hookError: NSError?
        hook = ZeuscoreCall("WebLoginHook",
            jsonArgs(["function(q) { window.webkit.messageHandlers.zeusWebLogin.postMessage(q); }"]), &hookError)
        webView.load(URLRequest(url: Self.pageURL))"""
assert old in s
s = s.replace(old, new)

# 3. didFinish: deliver shim first, then the hook
old = """    func webView(_ wv: WKWebView, didFinish navigation: WKNavigation!) {
        wv.evaluateJavaScript(Self.hook, completionHandler: nil)
    }"""
new = """    func webView(_ wv: WKWebView, didFinish navigation: WKNavigation!) {
        wv.evaluateJavaScript("window.__zeusDeliver = function(q) { window.webkit.messageHandlers.zeusWebLogin.postMessage(q); };", completionHandler: nil)
        wv.evaluateJavaScript(hook, completionHandler: nil)
    }"""
assert old in s
s = s.replace(old, new)

# 4. message handler: deliver(q) dispatch
old = """    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
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
    }"""
new = """    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        // The shared hook delivers result queries ("tokens?...", "session?...",
        // "cancel") — mirror the desktop receiver.
        guard let q = message.body as? String else { return }
        let kind = q.split(separator: "?").first.map(String.init) ?? q
        let queryString = q.contains("?") ? q.split(separator: "?", maxSplits: 1)[1] : ""
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
    }"""
assert old in s
s = s.replace(old, new)

io.open(p, "w", encoding="utf-8", newline="\n").write(s)
print("iOS rewired")
