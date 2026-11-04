import Foundation

enum OnboardingError: Error, Equatable {
    case invalidServerURL
    case invalidAdminToken
    case serverUnhealthy
    case verificationFailed
    case missingToken
}

extension OnboardingError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .invalidServerURL:
            return "Укажите URL сервера: https://… (http разрешён только для localhost)"
        case .invalidAdminToken:
            return "Admin token отклонён сервером. Проверьте BRAIN_ADMIN_TOKEN"
        case .serverUnhealthy:
            return "Сервер работает, но база данных недоступна. Попробуйте позже"
        case .verificationFailed:
            return "Сервер выдал токен, но не принял его. Попробуйте повторить подключение"
        case .missingToken:
            return "Сервер не выдал один из токенов"
        }
    }
}

/// Runs the first-launch setup and token rotation.
///
/// Rotation follows `CONTRACTS.md` §4.1: create new clients, persist their
/// tokens, verify them, and only then revoke the previous clients. If anything
/// fails, the previous Keychain/config/mcp_token state is restored and the
/// freshly created clients are revoked. Only clients recorded in `app_clients`
/// are ever revoked by this installation.
struct OnboardingService {
    struct Result: Equatable, Sendable {
        let health: Health
        let serverURL: URL
        /// Client name → id.
        let clientIDs: [String: Int64]
    }

    private struct Spec {
        let role: String
        let baseName: String
        let kind: ClientKind
        let scopes: [Scope]
        let account: String
    }

    private struct Snapshot {
        let config: AppConfig?
        /// `nil` when the Keychain could not be read; rollback then leaves it
        /// untouched instead of deleting values it failed to read.
        let tokens: [String: String?]?
        let mcpToken: String?
    }

    private let configStore: ConfigStore
    private let keychain: KeychainStore
    private let tokenFile: MCPTokenFile
    private let hostName: String
    private let session: URLSession
    private let makeSuffix: @Sendable () -> String

    init(
        configStore: ConfigStore = ConfigStore(),
        keychain: KeychainStore = KeychainStore(),
        tokenFile: MCPTokenFile? = nil,
        hostName: String = ProcessInfo.processInfo.hostName,
        session: URLSession = .shared,
        makeSuffix: @escaping @Sendable () -> String = {
            String(UUID().uuidString.prefix(8)).lowercased()
        }
    ) {
        self.configStore = configStore
        self.keychain = keychain
        self.tokenFile = tokenFile ?? MCPTokenFile(directory: configStore.directory)
        self.hostName = hostName
        self.session = session
        self.makeSuffix = makeSuffix
    }

    func run(serverURL: URL, adminToken: String) async throws -> Result {
        let admin = APIClient(baseURL: serverURL, token: adminToken, session: session)
        let snapshot = takeSnapshot()

        let health = try await admin.health()
        guard health.status == "ok" else {
            throw OnboardingError.serverUnhealthy
        }

        let existing = try await withAdminErrors {
            try await admin.listClients()
        }
        var activeNames = Set(existing.items.filter { $0.revokedAt == nil }.map(\.name))

        var pending = snapshot.config?.pendingRevocations ?? []
        pending = await retryPendingRevocations(pending, admin: admin)

        let previousClients = snapshot.config?.appClients ?? [:]
        let specs = clientSpecs()

        var created: [(spec: Spec, response: CreateClientResponse)] = []
        do {
            for spec in specs {
                let response = try await withAdminErrors {
                    try await createClient(spec: spec, activeNames: activeNames, admin: admin)
                }
                activeNames.insert(response.client.name)
                created.append((spec, response))
            }

            guard let ui = created.first(where: { $0.spec.role == "ui" }) else {
                throw OnboardingError.missingToken
            }

            // Verify the token the app will actually use before touching disk.
            let uiClient = APIClient(baseURL: serverURL, token: ui.response.token, session: session)
            do {
                _ = try await uiClient.stats()
            } catch let error as APIError where error.requiresReauthentication {
                throw OnboardingError.verificationFailed
            }

            let newIDs = Set(created.map(\.response.client.id))
            let previousIDs = Set(previousClients.values.map(\.id)).subtracting(newIDs)
            let pendingToPersist = uniqueIDs(pending + Array(previousIDs))
            try persist(
                created: created,
                snapshot: snapshot,
                serverURL: serverURL,
                pending: pendingToPersist
            )
            // Old ids are already recorded in `pending_revocations`, so a crash
            // before this loop is recovered on the next run.
            pending = await revoke(ids: previousIDs, pending: pendingToPersist, admin: admin)
        } catch {
            await revokeCreatedClients(created.map(\.response.client.id), admin: admin)
            restore(snapshot)
            throw error
        }

        // Rewrite the key even when everything was revoked, so a stale
        // `pending_revocations` is removed from the shared file.
        if var config = try? configStore.load() {
            config.pendingRevocations = pending.isEmpty ? nil : pending
            try? configStore.save(config)
        }

        var ids: [String: Int64] = [:]
        for item in created {
            ids[item.response.client.name] = item.response.client.id
        }
        return Result(health: health, serverURL: serverURL, clientIDs: ids)
    }

    // MARK: - URL

    static func validateServerURL(_ raw: String) throws -> URL {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed),
              let scheme = url.scheme?.lowercased(),
              let host = url.host else {
            throw OnboardingError.invalidServerURL
        }
        if scheme == "https" {
            return url
        }
        if scheme == "http", isLoopback(host) {
            return url
        }
        throw OnboardingError.invalidServerURL
    }

    static func isLoopback(_ host: String) -> Bool {
        ["localhost", "127.0.0.1", "::1"].contains(host.lowercased())
    }

    // MARK: - Clients

    private func clientSpecs() -> [Spec] {
        let slug = Self.hostSlug(hostName)
        let opencodeName = slug.isEmpty ? "opencode" : "opencode-\(slug)"
        return [
            Spec(
                role: "opencode",
                baseName: opencodeName,
                kind: .opencode,
                scopes: [.ingest, .mcpRead, .mcpWrite],
                account: KeychainStore.Account.opencode
            ),
            Spec(
                role: "jarvis",
                baseName: "jarvis",
                kind: .jarvis,
                scopes: [.ingest, .mcpRead, .mcpWrite],
                account: KeychainStore.Account.jarvis
            ),
            Spec(
                role: "ui",
                baseName: "second-brain-app",
                kind: .ui,
                scopes: [.ui, .admin],
                account: KeychainStore.Account.ui
            ),
        ]
    }

    /// Picks a free name for the new client. If the base name is taken (for
    /// example by another machine or a manually created client), a unique suffix
    /// is used instead of revoking somebody else's token.
    private func createClient(
        spec: Spec,
        activeNames: Set<String>,
        admin: APIClient
    ) async throws -> CreateClientResponse {
        var name = activeNames.contains(spec.baseName)
            ? "\(spec.baseName)-\(sanitizedSuffix())"
            : spec.baseName

        for _ in 0..<4 {
            do {
                return try await admin.createClient(
                    CreateClientRequest(name: name, kind: spec.kind, scopes: spec.scopes)
                )
            } catch let error as APIError {
                guard case .server(let status, _, _, _) = error, status == 409 else {
                    throw error
                }
                name = "\(spec.baseName)-\(sanitizedSuffix())"
            }
        }
        throw OnboardingError.missingToken
    }

    private func sanitizedSuffix() -> String {
        let raw = makeSuffix().lowercased()
        let allowed = Set("abcdefghijklmnopqrstuvwxyz0123456789")
        let cleaned = String(raw.filter { allowed.contains($0) }.prefix(8))
        return cleaned.isEmpty ? "rot" : cleaned
    }

    /// `MacBook.local` → `macbook`; ASCII only, ≤ 60 chars.
    static func hostSlug(_ hostName: String) -> String {
        let short = hostName.split(separator: ".").first.map(String.init) ?? hostName
        let allowed = Set("abcdefghijklmnopqrstuvwxyz0123456789")
        let mapped = short.lowercased().map { allowed.contains($0) ? $0 : "-" }
        let collapsed = String(mapped)
            .split(separator: "-", omittingEmptySubsequences: true)
            .joined(separator: "-")
        return String(collapsed.prefix(60))
    }

    // MARK: - Persistence

    private func takeSnapshot() -> Snapshot {
        var tokens: [String: String?] = [:]
        for account in KeychainStore.Account.all {
            do {
                tokens[account] = try keychain.value(for: account)
            } catch {
                tokens = [:]
                return Snapshot(
                    config: try? configStore.load(),
                    tokens: nil,
                    mcpToken: try? tokenFile.read()
                )
            }
        }
        return Snapshot(
            config: try? configStore.load(),
            tokens: tokens,
            mcpToken: try? tokenFile.read()
        )
    }

    private func persist(
        created: [(spec: Spec, response: CreateClientResponse)],
        snapshot: Snapshot,
        serverURL: URL,
        pending: [Int64]
    ) throws {
        for item in created {
            try keychain.setValue(item.response.token, for: item.spec.account)
        }

        var config = snapshot.config ?? AppConfig()
        config.serverURL = serverURL
        config.appClients = Dictionary(uniqueKeysWithValues: created.map {
            ($0.spec.role, StoredClient(id: $0.response.client.id, name: $0.response.client.name))
        })
        config.pendingRevocations = pending.isEmpty ? nil : pending
        try configStore.save(config)

        guard let opencode = created.first(where: { $0.spec.role == "opencode" }) else {
            throw OnboardingError.missingToken
        }
        try tokenFile.write(opencode.response.token)
    }

    private func restore(_ snapshot: Snapshot) {
        if let tokens = snapshot.tokens {
            for (account, value) in tokens {
                if let value {
                    try? keychain.setValue(value, for: account)
                } else {
                    try? keychain.deleteValue(for: account)
                }
            }
        }
        if let config = snapshot.config {
            try? configStore.save(config)
        } else {
            try? configStore.delete()
        }
        if let token = snapshot.mcpToken {
            try? tokenFile.write(token)
        } else {
            try? tokenFile.delete()
        }
    }

    // MARK: - Revocation

    private func retryPendingRevocations(_ ids: [Int64], admin: APIClient) async -> [Int64] {
        var failed: [Int64] = []
        for id in ids {
            do {
                try await admin.revokeClient(id: id)
            } catch APIError.notFound {
                continue
            } catch {
                failed.append(id)
            }
        }
        return failed
    }

    private func revoke(ids: Set<Int64>, pending: [Int64], admin: APIClient) async -> [Int64] {
        var pending = Set(pending)
        for id in ids {
            do {
                try await admin.revokeClient(id: id)
                pending.remove(id)
            } catch APIError.notFound {
                pending.remove(id)
            } catch {
                // Keep it for the next run.
            }
        }
        return pending.sorted()
    }

    private func uniqueIDs(_ ids: [Int64]) -> [Int64] {
        Array(Set(ids)).sorted()
    }

    private func revokeCreatedClients(_ ids: [Int64], admin: APIClient) async {
        for id in ids {
            try? await admin.revokeClient(id: id)
        }
    }

    private func withAdminErrors<T>(_ operation: () async throws -> T) async throws -> T {
        do {
            return try await operation()
        } catch let error as APIError where error.requiresReauthentication {
            throw OnboardingError.invalidAdminToken
        }
    }
}
