import Foundation

enum OnboardingError: Error, Equatable {
    case invalidServerURL
    case serverUnhealthy
    case missingUIToken
}

extension OnboardingError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .invalidServerURL:
            return "Укажите полный URL сервера, например https://brain.example.com"
        case .serverUnhealthy:
            return "Сервер работает, но база данных недоступна. Попробуйте позже"
        case .missingUIToken:
            return "Сервер не выдал токен приложения"
        }
    }
}

/// Runs the first-launch setup: checks the server, rotates the three clients,
/// verifies the UI token, then persists tokens to the Keychain, `config.json`
/// and `mcp_token`. Nothing is written locally unless every step succeeded.
struct OnboardingService {
    struct Result: Equatable, Sendable {
        let health: Health
        let serverURL: URL
        /// Client name → id, handy for logs and the Settings screen.
        let clientIDs: [String: Int64]
    }

    private struct Spec {
        let name: String
        let kind: ClientKind
        let scopes: [Scope]
        let account: String
    }

    private let configStore: ConfigStore
    private let keychain: KeychainStore
    private let tokenFile: MCPTokenFile
    private let hostName: String
    private let session: URLSession

    init(
        configStore: ConfigStore = ConfigStore(),
        keychain: KeychainStore = KeychainStore(),
        tokenFile: MCPTokenFile? = nil,
        hostName: String = ProcessInfo.processInfo.hostName,
        session: URLSession = .shared
    ) {
        self.configStore = configStore
        self.keychain = keychain
        self.tokenFile = tokenFile ?? MCPTokenFile(directory: configStore.directory)
        self.hostName = hostName
        self.session = session
    }

    func run(serverURL: URL, adminToken: String) async throws -> Result {
        let admin = APIClient(baseURL: serverURL, token: adminToken, session: session)

        let health = try await admin.health()
        guard health.status == "ok" else {
            throw OnboardingError.serverUnhealthy
        }

        let specs = clientSpecs()
        try await revokeExistingClients(specs: specs, admin: admin)

        var created: [(spec: Spec, response: CreateClientResponse)] = []
        for spec in specs {
            let response = try await createRotatingIfNeeded(spec: spec, admin: admin)
            created.append((spec, response))
        }

        guard let ui = created.first(where: { $0.spec.kind == .ui }) else {
            throw OnboardingError.missingUIToken
        }
        guard let opencode = created.first(where: { $0.spec.kind == .opencode }) else {
            throw OnboardingError.missingUIToken
        }

        // Verify the token the app will actually use before persisting anything.
        let uiClient = APIClient(baseURL: serverURL, token: ui.response.token, session: session)
        _ = try await uiClient.stats()

        for item in created {
            try keychain.setValue(item.response.token, for: item.spec.account)
        }
        try configStore.save(AppConfig(serverURL: serverURL))
        try tokenFile.write(opencode.response.token)

        var ids: [String: Int64] = [:]
        for item in created {
            ids[item.spec.name] = item.response.client.id
        }
        return Result(health: health, serverURL: serverURL, clientIDs: ids)
    }

    static func validateServerURL(_ raw: String) throws -> URL {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed),
              let scheme = url.scheme?.lowercased(),
              scheme == "https" || scheme == "http",
              url.host != nil else {
            throw OnboardingError.invalidServerURL
        }
        return url
    }

    private func clientSpecs() -> [Spec] {
        let slug = Self.hostSlug(hostName)
        let opencodeName = slug.isEmpty ? "opencode" : "opencode-\(slug)"
        return [
            Spec(
                name: opencodeName,
                kind: .opencode,
                scopes: [.ingest, .mcpRead, .mcpWrite],
                account: KeychainStore.Account.opencode
            ),
            Spec(
                name: "jarvis",
                kind: .jarvis,
                scopes: [.ingest, .mcpRead, .mcpWrite],
                account: KeychainStore.Account.jarvis
            ),
            Spec(
                name: "second-brain-app",
                kind: .ui,
                scopes: [.ui, .admin],
                account: KeychainStore.Account.ui
            ),
        ]
    }

    private func revokeExistingClients(specs: [Spec], admin: APIClient) async throws {
        let names = Set(specs.map(\.name))
        let existing = try await admin.listClients()
        for client in existing.items where names.contains(client.name) && client.revokedAt == nil {
            try await admin.revokeClient(id: client.id)
        }
    }

    /// A `409 conflict` means another process created the same name between the
    /// list and the create call. Revoke it and retry once.
    private func createRotatingIfNeeded(spec: Spec, admin: APIClient) async throws -> CreateClientResponse {
        let request = CreateClientRequest(name: spec.name, kind: spec.kind, scopes: spec.scopes)
        do {
            return try await admin.createClient(request)
        } catch let error as APIError {
            guard case .server(let status, _, _, _) = error, status == 409 else {
                throw error
            }
            let existing = try await admin.listClients()
            for client in existing.items where client.name == spec.name && client.revokedAt == nil {
                try await admin.revokeClient(id: client.id)
            }
            return try await admin.createClient(request)
        }
    }

    /// `MacBook.local` → `macbook`; keeps names inside `[A-Za-z0-9._:-]`.
    static func hostSlug(_ hostName: String) -> String {
        let short = hostName.split(separator: ".").first.map(String.init) ?? hostName
        let allowed = CharacterSet.alphanumerics
        return short
            .lowercased()
            .components(separatedBy: allowed.inverted)
            .filter { !$0.isEmpty }
            .joined(separator: "-")
    }
}
