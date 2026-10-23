import Foundation

/// Values shared by the app and its network extension. Change them together
/// with the bundle identifiers in project.yml and the entitlements.
enum Shared {
    static let appGroup = "group.com.zeusvpn.app"
    static let tunnelBundleID = "com.zeusvpn.app.tunnel"

    /// Accounts and settings live in the app group container, so the
    /// extension connects with what the app saved.
    static var dataDir: URL {
        let base = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
            ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("zeus", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }
}
