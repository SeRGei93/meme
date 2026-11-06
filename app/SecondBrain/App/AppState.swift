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
    var selectedSection = AppSection.initial()
    var lastSync: Date?
    private(set) var searchFocusRequest = 0
    private var consumedSearchFocus = 0
    /// Record requested by the menu bar quick search. The main window consumes
    /// it once, whether it already exists or is created by the menu bar action.
    private(set) var pendingRecordID: Int64?
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

    /// Clients this installation created, keyed by role. Read from the shared
    /// config; used by the agents screen to mark the local OpenCode source.
    var localAppClients: [String: StoredClient] {
        (try? configStore.load())?.appClients ?? [:]
    }

    // MARK: OpenCode integration

    var pluginStatus: PluginInstallStatus?
    var mcpStatus: OpenCodeConfigStatus?
    /// Keychain account → whether a token is present.
    var tokenPresence: [String: Bool] = [:]
    var settingsNotice: String?
    var settingsError: String?
    var integrationBusy = false
    /// True when the OpenCode plugin is installed and therefore capturing
    /// sessions. MCP is configured independently of this flag.
    private(set) var autoCaptureEnabled = false
    /// Bumped whenever token/keychain presence is re-read, so read-only screens
    /// can rebuild their API client after rotation.
    private(set) var tokenRevision = 0

    // MARK: Upload queue

    var uploadStatus = UploadStatus()

    private let configStore: ConfigStore
    private let keychain: KeychainStore
    private let mcpTokenFile: MCPTokenFile
    private let session: URLSession
    private let openCodeConfig: OpenCodeConfig
    private let pluginInstaller: PluginInstaller
    private let uploadsEnabled: Bool
    private var uploadQueue: UploadQueue?
    private var spoolWatcher: SpoolWatcher?
    private var uploadSetupError: String?
    private var didBootstrap = false

    init(
        configStore: ConfigStore = ConfigStore(),
        keychain: KeychainStore = KeychainStore(),
        session: URLSession = AppState.defaultSession(),
        openCodeDirectory: URL = OpenCodePaths.defaultDirectory(),
        pluginSourceDirectory: URL? = PluginInstaller.defaultSourceDirectory(),
        uploadsEnabled: Bool = ProcessInfo.processInfo.environment["SECOND_BRAIN_DISABLE_UPLOADS"] != "1"
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
        self.uploadsEnabled = uploadsEnabled
    }

    nonisolated private static func defaultSession() -> URLSession {
        #if DEBUG
        if ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_MOCK"] == "1" {
            return UITestURLProtocol.session()
        }
        #endif
        return .shared
    }


    // MARK: Lifecycle

    /// Menu bar / ⌘K entry point: switch to Search and ask the field to focus.
    func focusSearch() {
        selectedSection = .search
        searchFocusRequest &+= 1
    }

    /// True once per focus request, so recreating the search view does not
    /// steal focus again on a later navigation.
    func consumeSearchFocusRequest() -> Bool {
        guard searchFocusRequest > consumedSearchFocus else { return false }
        consumedSearchFocus = searchFocusRequest
        return true
    }

    /// Menu bar quick search: switch to Search and ask the main window to show
    /// this record. The window consumes the id on appearance or on change, so
    /// it works both when the window is open and when it is created on demand.
    func openRecord(_ id: Int64) {
        selectedSection = .search
        pendingRecordID = id
    }

    func consumePendingRecord() -> Int64? {
        defer { pendingRecordID = nil }
        return pendingRecordID
    }

    /// Loads persisted configuration and verifies the stored token. On a clean
    /// install (or after a manual reset) it presents onboarding.
    func bootstrap() async {
        guard !didBootstrap else { return }
        didBootstrap = true

        let stored = try? configStore.load()
        serverURL = stored?.serverURL
        refreshIntegrationStatus()
        if stored?.autoCapture == nil, pluginCapturesSessions {
            // Existing installations keep capturing; record the explicit
            // preference so the next launch does not depend on inference.
            try? persistAutoCapture(true)
        }

        #if DEBUG
        // UI tests need a deterministic window without onboarding.
        if ProcessInfo.processInfo.environment["SECOND_BRAIN_SKIP_ONBOARDING"] == "1" {
            isConfigured = true
            isOnboardingPresented = false
            startUploads()
            return
        }
        #endif

        guard let url = stored?.serverURL,
              let token = readToken(for: KeychainStore.Account.ui) else {
            isConfigured = false
            isOnboardingPresented = true
            statusText = "Требуется настройка"
            return
        }

        isConfigured = true
        isOnboardingPresented = false
        // Start uploads before the network awaits: a cancelled view task must
        // not leave the process without a background upload queue.
        startUploads()
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
    func connect(
        serverURLText: String,
        adminToken: String,
        autoCapture: Bool? = nil
    ) async -> Bool {
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
            // Auto-capture is best effort: a plugin problem must not undo a
            // successful token rotation. MCP is configured even when the
            // plugin is disabled. `nil` keeps the current installation.
            if let autoCapture {
                setAutoCapture(autoCapture)
            } else {
                do {
                    _ = try configureMCPForCurrentServer()
                } catch {
                    settingsError = (error as? LocalizedError)?.errorDescription
                        ?? error.localizedDescription
                }
                refreshIntegrationStatus()
            }
            startUploads()
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
            if mcpStatus?.state == .configured {
                return mcpTokenAvailable
                    ? "OpenCode: MCP готов, автозахват выключен"
                    : "OpenCode: MCP настроен, но mcp_token отсутствует; автозахват выключен"
            }
            return "OpenCode: MCP не настроен, автозахват выключен"
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
        #if DEBUG
        // UI tests must not depend on the developer's real Keychain.
        if let forced = ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_TOKENS"] {
            let accounts = Set(
                forced
                    .split(separator: ",")
                    .map { $0.trimmingCharacters(in: .whitespaces) }
                    .filter { !$0.isEmpty }
            )
            for account in KeychainStore.Account.all {
                presence[account] = accounts.contains(account)
            }
        }
        #endif
        tokenPresence = presence
        autoCaptureEnabled = pluginCapturesSessions
        tokenRevision &+= 1
    }

    private var pluginCapturesSessions: Bool {
        switch pluginStatus?.state {
        case .installed, .outdated:
            return true
        case .error:
            // The status check may fail (for example the bundled source is
            // missing) while an installed plugin is still capturing.
            guard let directory = pluginStatus?.directory else { return false }
            var isDirectory: ObjCBool = false
            return FileManager.default.fileExists(
                atPath: directory.path,
                isDirectory: &isDirectory
            ) && isDirectory.boolValue
        default:
            return false
        }
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
            // A manually installed plugin is an explicit opt-in.
            try? persistAutoCapture(true)

            if try configureMCPForCurrentServer() {
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

    /// Enables or disables automatic OpenCode session capture. MCP stays
    /// configured either way; disabling only removes the app-owned plugin.
    @discardableResult
    func setAutoCapture(_ enabled: Bool) -> Bool {
        guard !integrationBusy else { return false }
        integrationBusy = true
        defer { integrationBusy = false }
        settingsError = nil
        settingsNotice = nil

        var fileActionError: Error?
        do {
            if enabled {
                pluginStatus = try pluginInstaller.install()
            } else {
                try pluginInstaller.uninstall()
                pluginStatus = pluginInstaller.status()
            }
        } catch {
            fileActionError = error
        }

        // MCP is independent of the plugin: configure it even when install or
        // removal failed, so a broken plugin never cuts the agent off.
        var mcpConfigured = false
        var mcpError: Error?
        do {
            mcpConfigured = try configureMCPForCurrentServer()
        } catch {
            mcpError = error
        }

        refreshIntegrationStatus()

        if let fileActionError {
            settingsError = (fileActionError as? LocalizedError)?.errorDescription
                ?? fileActionError.localizedDescription
            return false
        }

        // Only persist what actually happened on disk. A failed enable must not
        // leave `auto_capture=true` with no plugin, and a failed disable must not
        // pretend the plugin stopped capturing.
        do {
            try persistAutoCapture(enabled)
        } catch {
            settingsError = (error as? LocalizedError)?.errorDescription
                ?? error.localizedDescription
            return false
        }

        if let mcpError {
            settingsError = (mcpError as? LocalizedError)?.errorDescription
                ?? mcpError.localizedDescription
            return false
        }

        if enabled {
            settingsNotice = mcpConfigured
                ? "Автозахват включён: плагин установлен, MCP настроен"
                : "Автозахват включён: плагин установлен, MCP config появится после указания URL"
        } else {
            settingsNotice = mcpConfigured
                ? "Автозахват выключен, MCP настроен"
                : "Автозахват выключен. MCP config появится после указания URL"
        }
        return true
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
        restartUploads()
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
        restartUploads()
        return true
    }

    // MARK: - Uploads

    func retryUploadsNow() {
        Task { await uploadQueue?.retryNow() }
    }

    func clearFailedUploads() {
        Task { await uploadQueue?.clearFailed() }
    }

    private func startUploads() {
        guard uploadsEnabled, isConfigured else { return }
        stopUploads()
        uploadSetupError = nil

        let directory = URL(fileURLWithPath: spoolPathText, isDirectory: true)
        let queue = UploadQueue(spoolDirectory: directory, client: makeUploadClient())
        let watcher = SpoolWatcher(directory: directory) { [weak queue] in
            Task { await queue?.trigger() }
        }
        uploadQueue = queue
        spoolWatcher = watcher

        do {
            try watcher.start()
        } catch {
            let message = (error as? LocalizedError)?.errorDescription
                ?? error.localizedDescription
            uploadSetupError = message
            uploadStatus.lastError = message
        }

        Task {
            await queue.setStatusHandler { [weak self, weak queue] status in
                Task { @MainActor [weak self, weak queue] in
                    guard let queue else { return }
                    self?.applyUploadStatus(status, from: queue)
                }
            }
        }
    }

    private func stopUploads() {
        spoolWatcher?.stop()
        spoolWatcher = nil
        if let queue = uploadQueue {
            Task { await queue.shutdown() }
        }
        uploadQueue = nil
        uploadSetupError = nil
    }

    private func restartUploads() {
        if isConfigured {
            startUploads()
        } else {
            stopUploads()
        }
    }

    private func makeUploadClient() -> APIClient? {
        makeClient(for: KeychainStore.Account.opencode)
    }

    /// Read-only client for search, record detail, the dashboard and the agent
    /// screens. Uses the `ui` token (ui + admin scopes) so a revoked OpenCode
    /// token does not break browsing the memory.
    func makeReadClient() -> APIClient? {
        makeClient(for: KeychainStore.Account.ui)
    }

    /// Admin client for client inventory and revocation. It is the same `ui`
    /// token as reads; the name makes the write capability explicit at call
    /// sites so a delete never hides behind a "read" helper.
    func makeAdminClient() -> APIClient? {
        makeClient(for: KeychainStore.Account.ui)
    }

    private func makeClient(for account: String) -> APIClient? {
        guard let config = try? configStore.load(),
              let url = config.serverURL,
              let token = readToken(for: account),
              !token.isEmpty else {
            return nil
        }
        return APIClient(baseURL: url, token: token, session: session)
    }

    private func readToken(for account: String) -> String? {
        #if DEBUG
        // UI tests and previews point the app at a local mock server without
        // touching the real login keychain.
        if account == KeychainStore.Account.ui,
           let override = ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_TOKEN"],
           !override.isEmpty {
            return override
        }
        #endif
        return try? keychain.value(for: account)
    }

    private func applyUploadStatus(_ status: UploadStatus, from queue: UploadQueue) {
        guard uploadQueue === queue else { return }
        var merged = status
        if let uploadSetupError {
            // The queue publishes its clean initial state right after setup; a
            // watcher failure must survive that publish.
            merged.lastError = uploadSetupError
        }
        uploadStatus = merged
        if status.requiresAuthentication {
            requiresReauthentication = true
            connection = .unauthenticated
            statusText = "Нужен новый токен"
            isOnboardingPresented = true
        }
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

    /// Writes the MCP entry when a server URL is known. Returns false while
    /// onboarding has not supplied one yet.
    private func configureMCPForCurrentServer() throws -> Bool {
        guard let effectiveServerURL else { return false }
        serverURL = effectiveServerURL
        try configureMCP(serverURL: effectiveServerURL)
        return true
    }

    private func persistAutoCapture(_ enabled: Bool) throws {
        // Never replace an unreadable config with a blank one: it also holds
        // spool settings, app clients and plugin keys.
        var config = try configStore.load() ?? AppConfig()
        config.autoCapture = enabled
        try configStore.save(config)
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
