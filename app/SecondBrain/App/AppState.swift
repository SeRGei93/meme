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

    // MARK: OpenCode integration

    var pluginStatus: PluginInstallStatus?
    var mcpStatus: OpenCodeConfigStatus?
    /// Keychain account → whether a token is present.
    var tokenPresence: [String: Bool] = [:]
    var settingsNotice: String?
    var settingsError: String?
    var integrationBusy = false

    private let configStore: ConfigStore
    private let keychain: KeychainStore
    private let mcpTokenFile: MCPTokenFile
    private let session: URLSession
    private let openCodeConfig: OpenCodeConfig
    private let pluginInstaller: PluginInstaller
    private var didBootstrap = false

    init(
        configStore: ConfigStore = ConfigStore(),
        keychain: KeychainStore = KeychainStore(),
        session: URLSession = .shared,
        openCodeDirectory: URL = OpenCodePaths.defaultDirectory(),
        pluginSourceDirectory: URL? = PluginInstaller.defaultSourceDirectory()
    ) {
        self.configStore = configStore
        self.keychain = keychain
        mcpTokenFile = MCPTokenFile(directory: configStore.directory)
        self.session = session
        openCodeConfig = OpenCodeConfig(directory: openCodeDirectory)
        pluginInstaller = PluginInstaller(
            openCodeDirectory: openCodeDirectory,
            sourceDirectory: pluginSourceDirectory
        )
    }

    // MARK: Lifecycle

    /// Loads persisted configuration and verifies the stored token. On a clean
    /// install (or after a manual reset) it presents onboarding.
    func bootstrap() async {
        guard !didBootstrap else { return }
        didBootstrap = true

        let stored = try? configStore.load()
        serverURL = stored?.serverURL
        refreshIntegrationStatus()

        #if DEBUG
        // UI tests need a deterministic window without onboarding.
        if ProcessInfo.processInfo.environment["SECOND_BRAIN_SKIP_ONBOARDING"] == "1" {
            isConfigured = true
            isOnboardingPresented = false
            return
        }
        #endif

        guard let url = stored?.serverURL,
              let token = try? keychain.value(for: KeychainStore.Account.ui) else {
            isConfigured = false
            isOnboardingPresented = true
            statusText = "Требуется настройка"
            return
        }

        isConfigured = true
        isOnboardingPresented = false
        await checkHealth(using: APIClient(baseURL: url, session: session))
        await loadStats(using: APIClient(baseURL: url, token: token, session: session))
        if requiresReauthentication {
            // A stored token was revoked (for example a half-finished rotation):
            // send the user straight to the rotation form.
            isOnboardingPresented = true
        }
    }

    /// Runs onboarding and persists tokens only after the server verified them.
    @discardableResult
    func connect(serverURLText: String, adminToken: String) async -> Bool {
        guard !onboardingBusy else { return false }
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
            // Installing the plugin is best effort: a permissions problem must
            // not undo a successful token rotation. Settings shows the result.
            installOpenCodeIntegration()
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

    // MARK: OpenCode integration

    /// Effective spool path shown in Settings: the value from `config.json`, or
    /// the plugin default when the key is absent.
    var spoolPathText: String {
        if let config = try? configStore.load(),
           let configured = config.spoolDir,
           !configured.isEmpty {
            return configured
        }
        return configStore.directory.appendingPathComponent("spool", isDirectory: true).path
    }

    /// One-line state for the main window and the menu bar.
    var integrationSummary: String {
        guard let pluginStatus else {
            return "OpenCode: статус неизвестен"
        }
        switch pluginStatus.state {
        case .installed:
            switch mcpStatus?.state {
            case .configured:
                return mcpTokenAvailable
                    ? "OpenCode: плагин и MCP готовы"
                    : "OpenCode: плагин и MCP настроены, но mcp_token отсутствует"
            case .error(let message):
                return "OpenCode: MCP настроен с ошибкой — \(message)"
            case .notConfigured, .none:
                return "OpenCode: плагин установлен, MCP не настроен"
            }
        case .notInstalled:
            return "OpenCode: плагин не установлен"
        case .outdated:
            return "OpenCode: плагин нужно обновить"
        case .error(let message):
            return "OpenCode: \(message)"
        }
    }

    /// Cached at refresh time so `integrationSummary` does not read the token
    /// file on every SwiftUI render.
    private(set) var mcpTokenAvailable = false

    /// Re-reads plugin, MCP and Keychain state without changing anything.
    func refreshIntegrationStatus() {
        pluginStatus = pluginInstaller.status()
        mcpStatus = openCodeConfig.status(
            serverURL: effectiveServerURL,
            tokenPath: tokenPathForMCP
        )
        if let token = (try? mcpTokenFile.read()) ?? nil {
            mcpTokenAvailable = !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        } else {
            mcpTokenAvailable = false
        }
        var presence: [String: Bool] = [:]
        for account in KeychainStore.Account.all {
            presence[account] = (try? keychain.value(for: account)) != nil
        }
        tokenPresence = presence
    }

    /// Installs (or reinstalls) the plugin and rewrites the MCP entry. A failure
    /// is reported through `settingsError` and never throws into SwiftUI.
    @discardableResult
    func installOpenCodeIntegration() -> Bool {
        guard !integrationBusy else { return false }
        integrationBusy = true
        defer { integrationBusy = false }
        settingsError = nil
        settingsNotice = nil

        do {
            pluginStatus = try pluginInstaller.install()

            if let effectiveServerURL {
                serverURL = effectiveServerURL
                try configureMCP(serverURL: effectiveServerURL)
                settingsNotice = "Плагин OpenCode установлен, MCP config настроен"
            } else {
                settingsNotice = "Плагин OpenCode установлен. MCP config появится после указания URL сервера"
            }
            refreshIntegrationStatus()
            return true
        } catch {
            settingsError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            refreshIntegrationStatus()
            return false
        }
    }

    /// Saves a new server URL, rewrites the MCP entry and verifies the server.
    /// Tokens are left untouched: if the new server rejects them, the UI asks
    /// for a rotation instead of silently deleting credentials.
    @discardableResult
    func updateServerURL(_ raw: String) async -> Bool {
        settingsError = nil
        settingsNotice = nil

        let url: URL
        do {
            url = try OnboardingService.validateServerURL(raw)
        } catch {
            settingsError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            return false
        }

        do {
            var config = try configStore.load() ?? AppConfig()
            config.serverURL = url
            try configStore.save(config)
            serverURL = url
        } catch {
            settingsError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            return false
        }

        do {
            try configureMCP(serverURL: url)
            settingsNotice = "URL сохранён, MCP config обновлён"
        } catch {
            settingsError = "URL сохранён, но MCP config не обновлён: "
                + ((error as? LocalizedError)?.errorDescription ?? error.localizedDescription)
        }

        await checkHealth(using: APIClient(baseURL: url, session: session))
        if let token = try? keychain.value(for: KeychainStore.Account.ui) {
            await loadStats(using: APIClient(baseURL: url, token: token, session: session))
        }
        refreshIntegrationStatus()
        return true
    }

    /// Saves the spool directory and creates it when missing. A relative path is
    /// rejected because the watcher and the plugin would resolve it differently.
    @discardableResult
    func updateSpoolPath(_ raw: String) -> Bool {
        settingsError = nil
        settingsNotice = nil

        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let expanded = (trimmed as NSString).expandingTildeInPath
        guard expanded.hasPrefix("/") else {
            settingsError = "Укажите абсолютный путь к spool (например, ~/.config/second-brain/spool)"
            return false
        }

        let directory = URL(fileURLWithPath: expanded, isDirectory: true)
        do {
            try ensureDirectory(directory)
            var config = try configStore.load() ?? AppConfig()
            config.spoolDir = directory.path
            try configStore.save(config)
        } catch {
            settingsError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            return false
        }

        settingsNotice = "Путь Spool сохранён"
        return true
    }

    private var tokenPathForMCP: String {
        OpenCodePaths.tildePath(for: mcpTokenFile.fileURL)
    }

    /// `serverURL` is loaded by `bootstrap`, but Settings can run before it on a
    /// UI-test launch. Fall back to the shared config so installing the MCP
    /// entry does not depend on the order of calls.
    private var effectiveServerURL: URL? {
        serverURL ?? (try? configStore.load())?.serverURL
    }

    private func configureMCP(serverURL: URL) throws {
        try openCodeConfig.configure(serverURL: serverURL, tokenPath: tokenPathForMCP)
    }

    private func ensureDirectory(_ directory: URL) throws {
        let fileManager = FileManager.default
        var isDirectory: ObjCBool = false
        if fileManager.fileExists(atPath: directory.path, isDirectory: &isDirectory) {
            guard isDirectory.boolValue else {
                throw NSError(
                    domain: "SecondBrain.Settings",
                    code: 1,
                    userInfo: [NSLocalizedDescriptionKey: "Путь занят файлом: \(directory.path)"]
                )
            }
            return
        }
        try fileManager.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        try? fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
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
