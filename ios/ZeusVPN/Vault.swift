import Foundation
import Security

/// The account-file key, sealed inside the iOS Keychain: a random 32-byte
/// key wraps the account file, and the Keychain item (which never leaves
/// the device's secure store) keeps IT. The Go engine receives the raw key
/// once at startup, in memory only — the plaintext key file of the data
/// directory is gone.
enum Vault {
    private static let service = "Zeus VPN vault"
    private static let account = "account-key"

    /// Returns the master key as hex, creating and storing it if needed.
    static func keyHex() -> String {
        let key = storedKey() ?? freshKey()
        return key.map { String(format: "%02x", $0) }.joined()
    }

    private static func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }

    private static func storedKey() -> Data? {
        var item: CFTypeRef?
        let status = SecItemCopyMatching(baseQuery() as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data, data.count == 32 else {
            return nil
        }
        return data
    }

    private static func freshKey() -> Data {
        var key = Data(count: 32)
        let ok = key.withUnsafeMutableBytes { buf -> Int32 in
            SecRandomCopyBytes(kSecRandomDefault, 32, buf.baseAddress!)
        }
        if ok == errSecSuccess {
            var add = baseQuery()
            add[kSecValueData as String] = key
            add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            SecItemAdd(add as CFDictionary, nil)
        }
        return key
    }
}
