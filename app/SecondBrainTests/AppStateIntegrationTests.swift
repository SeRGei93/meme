import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class AppStateIntegrationTests: XCTestCase {
    private var root: URL!
    private var configDirectory: URL!
    private var openCodeDirectory: URL!
    private var pluginSource: URL!
    private var keychain: KeychainStore!

    override func setUpWithError() throws {
        try super.setUpWithError()
        root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-integration-\(UUID().uuidString)", isDirectory: true)
        configDirectory = root.appendingPathComponent("second-brain", isDirectory: true)
        openCodeDirectory = root.appendingPathComponent("opencode", isDirectory: true)
        pluginSource = root.appendingPathComponent("SecondBrainPlugin", isDirectory: true)
        keychain = KeychainStore(service: "com.serg.secondbrain.tests.\(UUID().uuidString)")
        try writePluginSource()
        StubURLProtocol.reset()
    }

    override func tearDownWithError() throws {
        try? keychain.deleteAll()
        try? FileManager.default.removeItem(at: root)
        StubURLProtocol.reset()
        keychain = nil
        pluginSource = nil
        openCodeDirectory = nil
        configDirectory = nil
        root = nil
        try super.tearDownWithError()
    }

    func testInstallIntegrationCopiesThePluginAndWritesTheMCPConfig() throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://meme.aimerce.by")!)
        )
        let state = makeState()
        state.refreshIntegrationStatus()

        XCTAssertEqual(state.pluginStatus?.state, PluginInstallState.notInstalled)
        XCTAssertEqual(state.mcpStatus?.state, OpenCodeConfigState.notConfigured)

        XCTAssertTrue(state.installOpenCodeIntegration())

        XCTAssertEqual(state.pluginStatus?.state, PluginInstallState.installed)
        XCTAssertEqual(state.mcpStatus?.state, OpenCodeConfigState.configured)
        XCTAssertTrue(state.settingsNotice?.contains("установлен") == true)
        XCTAssertNil(state.settingsError)

        let destination = openCodeDirectory.appendingPathComponent("plugins/second-brain")
        XCTAssertTrue(FileManager.default.fileExists(atPath: destination.appendingPathComponent("index.ts").path))

        let configURL = openCodeDirectory.appendingPathComponent("opencode.json")
        let object = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: configURL)) as? [String: Any]
        )
        let mcp = try XCTUnwrap(object["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        let entry = try XCTUnwrap(servers["second-brain"] as? [String: Any])
        XCTAssertEqual(entry["url"] as? String, "https://meme.aimerce.by/mcp")
    }

    func testInstallReportsABrokenMCPConfigWithoutLosingThePlugin() throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://meme.aimerce.by")!)
        )
        try FileManager.default.createDirectory(at: openCodeDirectory, withIntermediateDirectories: true)
        let broken = openCodeDirectory.appendingPathComponent("opencode.json")
        try Data("{not json".utf8).write(to: broken)
        let state = makeState()

        XCTAssertFalse(state.installOpenCodeIntegration())

        XCTAssertEqual(state.pluginStatus?.state, PluginInstallState.installed)
        guard case .some(.error(let message)) = state.mcpStatus?.state else {
            return XCTFail("expected an MCP error, got \(String(describing: state.mcpStatus))")
        }
        XCTAssertTrue(message.contains("opencode.json") || message.contains("конфигурац"))
        XCTAssertTrue(
            state.settingsError?.contains("opencode.json") == true
                || state.settingsError?.contains("конфигурац") == true
        )
        XCTAssertEqual(try String(contentsOf: broken, encoding: .utf8), "{not json")
        XCTAssertTrue(
            FileManager.default.fileExists(
                atPath: openCodeDirectory.appendingPathComponent("plugins/second-brain/index.ts").path
            )
        )
    }

    func testRefreshIntegrationStatusReportsStoredTokens() throws {
        try keychain.setValue("sb_ui_test", for: KeychainStore.Account.ui)
        let state = makeState()

        state.refreshIntegrationStatus()

        XCTAssertEqual(state.tokenPresence[KeychainStore.Account.ui], true)
        XCTAssertEqual(state.tokenPresence[KeychainStore.Account.opencode], false)
        XCTAssertEqual(state.tokenPresence[KeychainStore.Account.jarvis], false)
    }

    func testInstallWithoutAServerStillInstallsThePlugin() throws {
        let state = makeState()

        XCTAssertTrue(state.installOpenCodeIntegration())

        XCTAssertEqual(state.pluginStatus?.state, PluginInstallState.installed)
        XCTAssertEqual(state.mcpStatus?.state, OpenCodeConfigState.notConfigured)
        XCTAssertTrue(state.settingsNotice?.contains("URL") == true)
        XCTAssertFalse(
            FileManager.default.fileExists(
                atPath: openCodeDirectory.appendingPathComponent("opencode.json").path
            )
        )
    }

    func testInstallReportsAUsefulErrorWhenThePluginPayloadIsMissing() throws {
        let brokenSource = root.appendingPathComponent("missing", isDirectory: true)
        let state = makeState(source: brokenSource)

        XCTAssertFalse(state.installOpenCodeIntegration())

        guard case .some(.error(let message)) = state.pluginStatus?.state else {
            return XCTFail("expected .error, got \(String(describing: state.pluginStatus))")
        }
        XCTAssertTrue(message.contains("плагин"))
        XCTAssertTrue(state.settingsError?.contains("плагин") == true)
    }

    func testUpdateServerURLPersistsItAndRewritesMCP() async throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://old.example.test")!, spoolDir: "/tmp/spool")
        )
        StubURLProtocol.setHandler { _ in .init(body: Data(Fixtures.healthJSON.utf8)) }
        let state = makeState()
        await state.bootstrap()
        _ = state.installOpenCodeIntegration()

        let saved = await state.updateServerURL(" https://new.example.test/ ")

        XCTAssertTrue(saved)
        XCTAssertEqual(state.serverURL?.absoluteString, "https://new.example.test/")
        XCTAssertEqual(
            try ConfigStore(directory: configDirectory).load()?.serverURL,
            URL(string: "https://new.example.test/")
        )
        XCTAssertEqual(state.mcpStatus?.state, OpenCodeConfigState.configured)
        XCTAssertNil(state.settingsError)

        let entry = try mcpEntry()
        XCTAssertEqual(entry["url"] as? String, "https://new.example.test/mcp")
    }

    func testUpdateServerURLRejectsBadInputWithoutTouchingTheConfig() async throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://old.example.test")!)
        )
        let state = makeState()
        await state.bootstrap()

        let saved = await state.updateServerURL("brain.example.test")

        XCTAssertFalse(saved)
        XCTAssertEqual(state.settingsError, OnboardingError.invalidServerURL.errorDescription)
        XCTAssertEqual(
            try ConfigStore(directory: configDirectory).load()?.serverURL,
            URL(string: "https://old.example.test")
        )
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testUpdateSpoolPathExpandsItAndCreatesAPrivateDirectory() async throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://meme.aimerce.by")!)
        )
        let state = makeState()
        await state.bootstrap()
        let spool = root.appendingPathComponent("nested/spool", isDirectory: true)

        XCTAssertTrue(state.updateSpoolPath("  \(spool.path)  "))

        XCTAssertEqual(
            try ConfigStore(directory: configDirectory).load()?.spoolDir,
            spool.path
        )
        XCTAssertEqual(state.spoolPathText, spool.path)
        XCTAssertEqual(try permissions(of: spool), 0o700)
        XCTAssertTrue(state.settingsNotice?.contains("Spool") == true)
    }

    func testUpdateSpoolPathRejectsRelativePaths() async throws {
        let state = makeState()
        await state.bootstrap()

        XCTAssertFalse(state.updateSpoolPath("relative/spool"))
        XCTAssertNotNil(state.settingsError)
        XCTAssertEqual(state.spoolPathText, configDirectory.appendingPathComponent("spool").path)
    }

    func testConnectInstallsThePluginAndMCPConfigAfterOnboarding() async throws {
        StubOnboardingServer.install()
        let state = makeState()

        let connected = await state.connect(
            serverURLText: "https://brain.example.test",
            adminToken: "admin-secret"
        )

        XCTAssertTrue(connected)
        XCTAssertEqual(state.pluginStatus?.state, PluginInstallState.installed)
        XCTAssertEqual(state.mcpStatus?.state, OpenCodeConfigState.configured)
        XCTAssertTrue(
            FileManager.default.fileExists(
                atPath: openCodeDirectory.appendingPathComponent("plugins/second-brain/index.ts").path
            )
        )
        let entry = try mcpEntry()
        XCTAssertEqual(entry["url"] as? String, "https://brain.example.test/mcp")
        let headers = try XCTUnwrap(entry["headers"] as? [String: Any])
        XCTAssertEqual(
            headers["Authorization"] as? String,
            "Bearer {file:\(configDirectory.appendingPathComponent("mcp_token").path)}"
        )
        XCTAssertEqual(try MCPTokenFile(directory: configDirectory).read(), "sb_opencode_1")
    }

    func testBootstrapUploadsExistingSpoolFiles() async throws {
        let serverURL = URL(string: "https://brain.example.test")!
        try ConfigStore(directory: configDirectory).save(AppConfig(serverURL: serverURL))
        try keychain.setValue("sb_ui_test", for: KeychainStore.Account.ui)
        try keychain.setValue("sb_opencode_test", for: KeychainStore.Account.opencode)
        let spool = configDirectory.appendingPathComponent("spool", isDirectory: true)
        let bundle = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/health":
                return .init(body: Data(Fixtures.healthJSON.utf8))
            case "/api/v1/stats":
                return .init(body: Data(Fixtures.statsJSON.utf8))
            case "/api/v1/ingest":
                return .init(
                    status: 202,
                    body: Data(#"{"record_id":1,"job_id":1,"status":"queued","replayed":false}"#.utf8)
                )
            default:
                return .init(status: 404, body: Data())
            }
        }
        let state = makeState()

        await state.bootstrap()

        for _ in 0..<100 {
            if !FileManager.default.fileExists(atPath: bundle.path),
               state.uploadStatus.sent == 1 {
                break
            }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertFalse(FileManager.default.fileExists(atPath: bundle.path))
        XCTAssertEqual(state.uploadStatus.sent, 1)
        XCTAssertEqual(state.uploadStatus.pending, 0)
    }

    func testConnectStillSucceedsWhenThePluginPayloadIsMissing() async throws {
        StubOnboardingServer.install()
        let state = makeState(source: root.appendingPathComponent("missing", isDirectory: true))

        let connected = await state.connect(
            serverURLText: "https://brain.example.test",
            adminToken: "admin-secret"
        )

        XCTAssertTrue(connected)
        XCTAssertNil(state.onboardingError)
        XCTAssertTrue(state.isConfigured)
        guard case .some(.error) = state.pluginStatus?.state else {
            return XCTFail("expected a plugin error, got \(String(describing: state.pluginStatus))")
        }
        XCTAssertNotNil(state.settingsError)
    }

    func testIntegrationSummaryIsVisibleOutsideSettings() throws {
        try ConfigStore(directory: configDirectory).save(
            AppConfig(serverURL: URL(string: "https://meme.aimerce.by")!)
        )
        let state = makeState()
        state.refreshIntegrationStatus()

        XCTAssertTrue(state.integrationSummary.contains("не установлен"))

        _ = state.installOpenCodeIntegration()
        XCTAssertTrue(state.integrationSummary.contains("mcp_token"))

        try MCPTokenFile(directory: configDirectory).write("sb_opencode_test")

        state.refreshIntegrationStatus()
        XCTAssertTrue(state.integrationSummary.contains("готов"))
    }

    private func makeState(
        source: URL? = nil
    ) -> AppState {
        AppState(
            configStore: ConfigStore(directory: configDirectory),
            keychain: keychain,
            session: StubURLProtocol.session(),
            openCodeDirectory: openCodeDirectory,
            pluginSourceDirectory: source ?? pluginSource
        )
    }

    private func writePluginSource() throws {
        try FileManager.default.createDirectory(at: pluginSource, withIntermediateDirectories: true)
        try Data("export default { id: 'second-brain.capture' }\n".utf8)
            .write(to: pluginSource.appendingPathComponent("index.ts"))
        try Data(#"{"name":"second-brain-opencode-plugin","version":"0.1.0"}"#.utf8)
            .write(to: pluginSource.appendingPathComponent("package.json"))
    }

    private func mcpEntry() throws -> [String: Any] {
        let configURL = openCodeDirectory.appendingPathComponent("opencode.json")
        let object = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: configURL)) as? [String: Any]
        )
        let mcp = try XCTUnwrap(object["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        return try XCTUnwrap(servers["second-brain"] as? [String: Any])
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}

@MainActor
private enum StubOnboardingServer {
    static func install() {
        StubURLProtocol.setHandler { request in
            let path = request.url?.path ?? ""
            switch (request.httpMethod ?? "GET", path) {
            case ("GET", "/health"):
                return .init(body: Data(Fixtures.healthJSON.utf8))
            case ("GET", "/api/v1/clients"):
                return .init(body: Data(#"{"items":[]}"#.utf8))
            case ("POST", "/api/v1/clients"):
                let body = try XCTUnwrap(request.bodyData)
                let spec = try JSONDecoder().decode(CreateClientRequest.self, from: body)
                let id: Int64 = spec.kind == .opencode ? 1 : (spec.kind == .jarvis ? 2 : 3)
                let client = ClientInfo(
                    id: id, name: spec.name, kind: spec.kind.rawValue,
                    scopes: spec.scopes.map(\.rawValue),
                    source: spec.kind == .opencode ? "opencode" : nil,
                    createdAt: Date(timeIntervalSince1970: 0),
                    lastSeenAt: nil, revokedAt: nil
                )
                let response = CreateClientResponse(client: client, token: "sb_\(spec.kind.rawValue)_\(id)")
                let encoder = JSONEncoder()
                encoder.dateEncodingStrategy = .iso8601
                return .init(status: 201, body: try encoder.encode(response))
            case ("GET", "/api/v1/stats"):
                return .init(body: Data(Fixtures.statsJSON.utf8))
            default:
                return .init(status: 404, body: Data())
            }
        }
    }
}

private enum Fixtures {
    static let healthJSON = """
    {"status":"ok","version":"0aa1357","schema_version":3,
     "time":"2026-11-04T19:30:10Z",
     "database":{"status":"ok","fts5":true}}
    """

    static let statsJSON = """
    {"records":{"total":12,"final":10,"draft":2,
      "by_source":{"opencode":9,"jarvis":3},
      "by_card_status":{"heuristic":1,"queued":1,"ok":9,"failed":1}},
     "projects":2,"clients":3,
     "jobs":{"queued":0,"running":0,"done":12,"failed":0},
     "database":{"status":"ok","schema_version":3,"size_bytes":2048}}
    """
}
