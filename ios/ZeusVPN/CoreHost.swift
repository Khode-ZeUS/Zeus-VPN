import UIKit
import Zeuscore

/// Receives events from the Go engine and forwards them to the web UI.
final class CoreHost: NSObject, ZeuscoreHostProtocol {
    static let shared = CoreHost()

    /// Set by WebViewController while the UI is loaded.
    weak var web: WebViewController?

    func onEvent(_ name: String?, payload: String?) {
        guard let name = name else { return }
        let payload = payload ?? ""
        DispatchQueue.main.async {
            self.web?.emit(name, payload)
        }
    }

    func openURL(_ url: String?) {
        guard let url = url.flatMap(URL.init(string:)) else { return }
        DispatchQueue.main.async {
            UIApplication.shared.open(url)
        }
    }

    // The app process never opens the VPN interface: the extension does.
    func openTun(_ config: String?, ret0_: UnsafeMutablePointer<Int32>?) throws {
        throw NSError(domain: "ZeusVPN", code: 1, userInfo: [NSLocalizedDescriptionKey: "the VPN runs in the network extension"])
    }

    func closeTun() {}
}
