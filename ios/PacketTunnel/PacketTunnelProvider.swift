import NetworkExtension
import Zeuscore

/// Runs the connection: a Go engine that loads the accounts and settings the
/// app saved in the shared app group, connects to Mozilla's VPN, and carries
/// the device's traffic from the utun interface.
final class PacketTunnelProvider: NEPacketTunnelProvider {
    private lazy var host = TunnelHost(provider: self)

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSError?
            _ = ZeuscoreStartTunnel(Shared.dataDir.path, self.host, &error)
            completionHandler(error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        DispatchQueue.global(qos: .userInitiated).async {
            ZeuscoreStopTunnel()
            completionHandler()
        }
    }

    /// The app asks for "status" every second while it is open.
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        completionHandler?(ZeuscoreTunnelStatus().data(using: .utf8))
    }
}

private final class TunnelHost: NSObject, ZeuscoreHostProtocol {
    private weak var provider: NEPacketTunnelProvider?

    init(provider: NEPacketTunnelProvider) {
        self.provider = provider
    }

    func onEvent(_ name: String?, payload: String?) {}

    func openURL(_ url: String?) {}

    /// Applies the interface settings Go asks for. Returns 0 as the
    /// descriptor: Go finds the utun socket itself.
    func openTun(_ config: String?, ret0_: UnsafeMutablePointer<Int32>?) throws {
        guard let provider = provider,
              let data = config?.data(using: .utf8),
              let cfg = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { throw NSError(domain: "ZeusVPN", code: 1, userInfo: [NSLocalizedDescriptionKey: "bad tunnel configuration"]) }

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        let v4 = NEIPv4Settings(addresses: [cfg["address4"] as? String ?? "172.19.0.1"],
                                subnetMasks: [Self.mask(cfg["prefix4"] as? Int ?? 30)])
        v4.includedRoutes = [NEIPv4Route.default()]
        var excluded4: [NEIPv4Route] = []
        if cfg["bypassLan"] as? Bool == true {
            excluded4 += [
                NEIPv4Route(destinationAddress: "10.0.0.0", subnetMask: "255.0.0.0"),
                NEIPv4Route(destinationAddress: "192.168.0.0", subnetMask: "255.255.0.0"),
                NEIPv4Route(destinationAddress: "169.254.0.0", subnetMask: "255.255.0.0"),
            ]
        }
        var excluded6: [NEIPv6Route] = []
        // Keep the VPN server itself off the tunnel: the engine's own
        // connection must not loop back inside the tunnel it is carrying.
        for case let ip as String in (cfg["protect"] as? [Any]) ?? [] {
            if ip.contains(":") {
                excluded6.append(NEIPv6Route(destinationAddress: ip, networkPrefixLength: 128))
            } else {
                excluded4.append(NEIPv4Route(destinationAddress: ip, subnetMask: "255.255.255.255"))
            }
        }
        v4.excludedRoutes = excluded4
        settings.ipv4Settings = v4
        let v6 = NEIPv6Settings(addresses: [cfg["address6"] as? String ?? "fdfe:dcba:9876::1"],
                                networkPrefixLengths: [NSNumber(value: cfg["prefix6"] as? Int ?? 126)])
        v6.includedRoutes = [NEIPv6Route.default()]
        v6.excludedRoutes = excluded6
        settings.ipv6Settings = v6
        let dns = NEDNSSettings(servers: [cfg["dns"] as? String ?? "172.19.0.2"])
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        settings.mtu = NSNumber(value: cfg["mtu"] as? Int ?? 1500)

        let done = DispatchSemaphore(value: 0)
        var failure: Error?
        provider.setTunnelNetworkSettings(settings) { error in
            failure = error
            done.signal()
        }
        done.wait()
        if let failure = failure { throw failure }
        ret0_?.pointee = 0
    }

    func closeTun() {}

    private static func mask(_ bits: Int) -> String {
        let m = bits == 0 ? UInt32(0) : ~UInt32(0) << UInt32(32 - bits)
        return [24, 16, 8, 0].map { String((m >> UInt32($0)) & 0xFF) }.joined(separator: ".")
    }
}
