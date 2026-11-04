import Foundation
import Security

enum KeychainError: Error, Equatable {
    case status(OSStatus)
    case invalidData
}

extension KeychainError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .status:
            return "Keychain недоступен. Разблокируйте связку ключей и попробуйте снова"
        case .invalidData:
            return "В Keychain сохранено значение в неизвестном формате"
        }
    }
}

/// Stores bearer tokens in the macOS Keychain. Accounts are stable names; the
/// service defaults to the app bundle identifier.
struct KeychainStore: Sendable {
    /// Token roles shared by onboarding, the upload queue and Settings.
    enum Account {
        static let opencode = "opencode"
        static let jarvis = "jarvis"
        static let ui = "ui"
        static let all = [opencode, jarvis, ui]
    }

    static let defaultService = "com.serg.secondbrain"

    let service: String

    init(service: String = KeychainStore.defaultService) {
        self.service = service
    }

    func setValue(_ value: String, for account: String) throws {
        guard let data = value.data(using: .utf8) else {
            throw KeychainError.invalidData
        }

        let query = baseQuery(account: account)
        let attributes: [String: Any] = [
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock,
        ]

        let updateStatus = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        switch updateStatus {
        case errSecSuccess:
            return
        case errSecItemNotFound:
            var insert = query
            insert.merge(attributes) { _, new in new }
            let addStatus = SecItemAdd(insert as CFDictionary, nil)
            guard addStatus == errSecSuccess else {
                throw KeychainError.status(addStatus)
            }
        default:
            throw KeychainError.status(updateStatus)
        }
    }

    func value(for account: String) throws -> String? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data, let value = String(data: data, encoding: .utf8) else {
                throw KeychainError.invalidData
            }
            return value
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.status(status)
        }
    }

    func deleteValue(for account: String) throws {
        let status = SecItemDelete(baseQuery(account: account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }

    /// Removes every account stored under this service. Used by tests and by a
    /// full logout.
    func deleteAll() throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }

    private func baseQuery(account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}
