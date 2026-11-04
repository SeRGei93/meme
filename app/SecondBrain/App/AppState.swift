import Foundation
import Observation

/// Shared UI state. One observable object feeds the window, menu bar and
/// Settings, so a failed health check or a revoked token is visible everywhere.
/// Main-actor isolated: SwiftUI reads these properties while rendering.
@MainActor
@Observable
final class AppState {
    enum Connection: Equatable {
        case unknown
        case checking
        case healthy(Health)
        case unauthenticated
        case failed(String)
    }

    var statusText = "Готов к работе"
    var lastSync: Date?
    var serverURL: URL?
    var connection: Connection = .unknown
    var health: Health?
    var stats: Stats?
    var errorMessage: String?
    var requiresReauthentication = false

    // MARK: Onboarding

    var isConfigured = false
    var isOnboardingPresented = false
    var onboardingBusy = false
    var onboardingError: String?

    private let configStore: ConfigStore
    private let keychain: KeychainStore
    private let mcpTokenFile: MCPTokenFile
    private let session: URLSession
    private var didBootstrap = false

    init(
        configStore: ConfigStore = ConfigStore(),
        keychain: KeychainStore = KeychainStore(),
        session: URLSession = .shared
    ) {
        self.configStore = configStore
        self.keychain = keychain
        mcpTokenFile = MCPTokenFile(directory: configStore.directory)
        self.session = session
    }

    // MARK: Lifecycle

    /// Loads persisted configuration and verifies the stored token. On a clean
    /// install (or after a manual reset) it presents onboarding.
    func bootstrap() async {
        guard !didBootstrap else { return }
        didBootstrap = true

        // UI tests need a deterministic window without onboarding.
        if ProcessInfo.processInfo.environment["SECOND_BRAIN_SKIP_ONBOARDING"] == "1" {
            isConfigured = true
            isOnboardingPresented = false
            return
        }

        let stored = try? configStore.load()
        guard let url = stored?.serverURL,
              let token = try? keychain.value(for: KeychainStore.Account.ui) else {
            isConfigured = false
            isOnboardingPresented = true
            statusText = "Требуется настройка"
            return
        }

        serverURL = url
        isConfigured = true
        isOnboardingPresented = false
        await checkHealth(using: APIClient(baseURL: url, session: session))
        await loadStats(using: APIClient(baseURL: url, token: token, session: session))
    }

    /// Runs onboarding and persists tokens only after the server verified them.
    @discardableResult
    func connect(serverURLText: String, adminToken: String) async -> Bool {
        onboardingBusy = true
        onboardingError = nil
        defer { onboardingBusy = false }

        do {
            let url = try OnboardingService.validateServerURL(serverURLText)
            let service = OnboardingService(
                configStore: configStore,
                keychain: keychain,
                tokenFile: mcpTokenFile,
                session: session
            )
            let result = try await service.run(
                serverURL: url,
                adminToken: adminToken.trimmingCharacters(in: .whitespacesAndNewlines)
            )

            serverURL = url
            health = result.health
            connection = .healthy(result.health)
            statusText = "Подключено"
            lastSync = Date()
            errorMessage = nil
            requiresReauthentication = false
            isConfigured = true
            isOnboardingPresented = false

            if let token = try? keychain.value(for: KeychainStore.Account.ui) {
                await loadStats(using: APIClient(baseURL: url, token: token, session: session))
            }
            return true
        } catch {
            onboardingError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            return false
        }
    }

    /// "Rotate tokens" from Settings. Keeps the current server pre-filled but
    /// requires the admin token again, so bootstrap credentials are never stored.
    func presentOnboardingForRotation() {
        onboardingError = nil
        isOnboardingPresented = true
    }

    // MARK: Status

    /// Public endpoint: works before the user has entered any token.
    func checkHealth(using client: APIClient) async {
        statusText = "Проверка подключения…"
        connection = .checking

        do {
            let health = try await client.health()
            self.health = health
            serverURL = client.configuration.baseURL
            lastSync = Date()
            requiresReauthentication = false
            errorMessage = nil
            connection = .healthy(health)
            statusText = health.status == "ok"
                ? "Подключено"
                : "Сервер работает с ограничениями"
        } catch {
            health = nil
            apply(error)
        }
    }

    /// Authenticated call used to verify that the stored token still works.
    func loadStats(using client: APIClient) async {
        do {
            let stats = try await client.stats()
            self.stats = stats
            serverURL = client.configuration.baseURL
            lastSync = Date()
            requiresReauthentication = false
            errorMessage = nil
            connection = health.map(Connection.healthy) ?? .unknown
            statusText = "Подключено"
        } catch {
            apply(error)
        }
    }

    /// Maps any error to the state the UI shows. A `401`/`403` stops the queue
    /// and asks for a new token instead of leaving a generic failure behind.
    func apply(_ error: Error) {
        let apiError = error as? APIError
            ?? .transport(localized: error.localizedDescription, retryable: true)
        errorMessage = apiError.displayMessage

        if apiError.requiresReauthentication {
            requiresReauthentication = true
            connection = .unauthenticated
            statusText = apiError.displayMessage
        } else {
            connection = .failed(apiError.displayMessage)
            statusText = apiError.displayMessage
        }
    }
}
