import Foundation
import NetworkExtension
import Zeuscore

/// Starts and stops the PacketTunnel extension (the Go engine calls
/// startTunnel/stopTunnel when the user connects) and reports the
/// extension's state back to the engine while the app is open.
final class TunnelController: NSObject, ZeuscoreTunnelControllerProtocol {
    static let shared = TunnelController()

    private var manager: NETunnelProviderManager?
    private var timer: Timer?
    private var lastStatus: NEVPNStatus = .invalid

    var isIdle: Bool {
        let s = manager?.connection.status ?? .invalid
        return s == .invalid || s == .disconnected
    }

    func load(_ done: @escaping () -> Void = {}) {
        NETunnelProviderManager.loadAllFromPreferences { managers, _ in
            DispatchQueue.main.async {
                self.manager = managers?.first
                self.observe()
                self.refresh()
                done()
            }
        }
    }

    private func observe() {
        NotificationCenter.default.removeObserver(self)
        NotificationCenter.default.addObserver(self, selector: #selector(statusChanged), name: .NEVPNStatusDidChange, object: nil)
        if timer == nil {
            timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in self?.refresh() }
        }
    }

    @objc private func statusChanged() { refresh() }

    /// Creates (the first time, which shows the system "Add VPN
    /// Configuration" prompt) and enables the VPN configuration.
    private func prepareManager(_ done: @escaping (NETunnelProviderManager?, Error?) -> Void) {
        let mgr = manager ?? NETunnelProviderManager()
        let proto = (mgr.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = Shared.tunnelBundleID
        proto.serverAddress = "Mozilla VPN"
        mgr.protocolConfiguration = proto
        mgr.localizedDescription = "Zeus VPN"
        mgr.isEnabled = true
        mgr.saveToPreferences { error in
            if let error = error {
                done(nil, error)
                return
            }
            // Reload before starting, or the first start fails.
            mgr.loadFromPreferences { error in
                DispatchQueue.main.async {
                    self.manager = mgr
                    self.observe()
                    done(error == nil ? mgr : nil, error)
                }
            }
        }
    }

    // Called by Go (on a background thread) when the user connects.
    func startTunnel() throws {
        let done = DispatchSemaphore(value: 0)
        var failure: Error?
        DispatchQueue.main.async {
            self.prepareManager { mgr, error in
                if let mgr = mgr {
                    do {
                        try mgr.connection.startVPNTunnel()
                    } catch {
                        failure = error
                    }
                } else {
                    failure = error ?? NSError(domain: "ZeusVPN", code: 2, userInfo: [NSLocalizedDescriptionKey: "VPN permission was not granted"])
                }
                done.signal()
            }
        }
        done.wait()
        if let failure = failure { throw failure }
        DispatchQueue.main.async { self.refresh() }
    }

    func stopTunnel() {
        DispatchQueue.main.async {
            self.manager?.connection.stopVPNTunnel()
        }
    }

    /// Reports the extension's state to the engine (and so to the UI).
    func refresh() {
        guard let session = manager?.connection as? NETunnelProviderSession else {
            ZeuscoreSetExternalState("")
            return
        }
        let status = session.status
        defer { lastStatus = status }
        switch status {
        case .connecting, .connected, .reasserting, .disconnecting:
            do {
                try session.sendProviderMessage(Data("status".utf8)) { data in
                    let json = data.flatMap { String(data: $0, encoding: .utf8) } ?? ""
                    // The extension's engine may not exist yet while starting.
                    ZeuscoreSetExternalState(json.isEmpty ? Self.placeholder(status) : json)
                }
            } catch {
                ZeuscoreSetExternalState(Self.placeholder(status))
            }
        default:
            if lastStatus != .disconnected && lastStatus != .invalid, #available(iOS 16.0, *) {
                // Show why the tunnel stopped, if it failed.
                session.fetchLastDisconnectError { error in
                    ZeuscoreSetExternalState(error.map { Self.placeholder(.disconnected, error: $0.localizedDescription) } ?? "")
                }
            } else if lastStatus != .disconnected {
                ZeuscoreSetExternalState("")
            }
        }
    }

    private static func placeholder(_ status: NEVPNStatus, error: String = "") -> String {
        let name: String
        switch status {
        case .connected: name = "connected"
        case .disconnecting: name = "disconnecting"
        case .reasserting: name = "reconnecting"
        case .disconnected, .invalid: name = "disconnected"
        default: name = "connecting"
        }
        let state: [String: Any] = ["status": name, "mode": "tun", "error": error]
        let body: [String: Any] = ["state": state, "up": 0, "down": 0]
        guard let data = try? JSONSerialization.data(withJSONObject: body) else { return "" }
        return String(data: data, encoding: .utf8) ?? ""
    }
}
