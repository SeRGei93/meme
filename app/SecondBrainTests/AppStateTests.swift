import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class AppStateTests: XCTestCase {
    private var client: APIClient!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        client = APIClient(
            configuration: .init(baseURL: URL(string: "https://brain.example.test")!, token: "sb_ui_test"),
            session: StubURLProtocol.session()
        )
    }

    override func tearDown() {
        StubURLProtocol.reset()
        client = nil
        super.tearDown()
    }

    func testStartsIdle() {
        let state = AppState()

        XCTAssertEqual(state.statusText, "Готов к работе")
        XCTAssertNil(state.lastSync)
        XCTAssertNil(state.health)
        XCTAssertFalse(state.requiresReauthentication)
        XCTAssertEqual(state.connection, .unknown)
    }

    func testSuccessfulHealthCheckUpdatesObservableState() async {
        StubURLProtocol.setHandler { _ in .init(body: Data(Fixtures.healthJSON.utf8)) }
        let state = AppState()

        await state.checkHealth(using: client)

        XCTAssertEqual(state.connection, .healthy(Fixtures.health))
        XCTAssertEqual(state.health, Fixtures.health)
        XCTAssertEqual(state.serverURL, URL(string: "https://brain.example.test"))
        XCTAssertEqual(state.statusText, "Подключено")
        XCTAssertNotNil(state.lastSync)
        XCTAssertFalse(state.requiresReauthentication)
        XCTAssertNil(state.errorMessage)
    }

    func testDegradedHealthStillReportsServerDetails() async {
        let body = Data("""
        {"status":"degraded","version":"1","schema_version":3,
         "time":"2026-11-04T19:30:10Z",
         "database":{"status":"error","fts5":true,"error":"disk full"}}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 503, body: body) }
        let state = AppState()

        await state.checkHealth(using: client)

        XCTAssertEqual(state.statusText, "Сервер работает с ограничениями")
        XCTAssertEqual(state.health?.database.error, "disk full")
    }

    func testUnreachableServerShowsRetryableError() async {
        StubURLProtocol.setHandler { _ in throw URLError(.cannotConnectToHost) }
        let state = AppState()

        await state.checkHealth(using: client)

        guard case .failed(let message) = state.connection else {
            return XCTFail("expected .failed, got \(state.connection)")
        }
        XCTAssertFalse(message.isEmpty)
        XCTAssertFalse(state.requiresReauthentication)
        XCTAssertNotNil(state.errorMessage)
    }

    func testUnauthorizedResponseAsksForANewToken() async {
        let body = Data(#"{"error":{"code":"unauthorized","message":"revoked"}}"#.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 401, body: body) }
        let state = AppState()

        await state.loadStats(using: client)

        XCTAssertTrue(state.requiresReauthentication)
        XCTAssertEqual(state.statusText, "Нужен новый токен")
        XCTAssertEqual(state.connection, .unauthenticated)
        XCTAssertNotNil(state.errorMessage)
    }

    func testSuccessfulStatsClearsReauthentication() async {
        StubURLProtocol.setHandler { _ in .init(body: Data(Fixtures.statsJSON.utf8)) }
        let state = AppState()
        state.requiresReauthentication = true

        await state.loadStats(using: client)

        XCTAssertFalse(state.requiresReauthentication)
        XCTAssertEqual(state.stats?.records.total, 12)
        XCTAssertNil(state.errorMessage)
    }
}

@MainActor
final class AppStateOnboardingTests: XCTestCase {
    private var directory: URL!
    private var keychain: KeychainStore!

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-state-\(UUID().uuidString)", isDirectory: true)
        keychain = KeychainStore(service: "com.serg.secondbrain.tests.\(UUID().uuidString)")
        StubURLProtocol.reset()
    }

    override func tearDownWithError() throws {
        try? keychain.deleteAll()
        try? FileManager.default.removeItem(at: directory)
        StubURLProtocol.reset()
        keychain = nil
        directory = nil
        try super.tearDownWithError()
    }

    func testBootstrapWithoutConfigShowsOnboarding() async {
        let state = AppState(
            configStore: ConfigStore(directory: directory),
            keychain: keychain,
            session: StubURLProtocol.session()
        )

        await state.bootstrap()

        XCTAssertFalse(state.isConfigured)
        XCTAssertTrue(state.isOnboardingPresented)
        XCTAssertEqual(state.statusText, "Требуется настройка")
    }

    func testConnectRejectsMalformedURLWithoutNetwork() async {
        let state = AppState(
            configStore: ConfigStore(directory: directory),
            keychain: keychain,
            session: StubURLProtocol.session()
        )

        let connected = await state.connect(serverURLText: "brain.example.com", adminToken: "admin")

        XCTAssertFalse(connected)
        XCTAssertEqual(state.onboardingError, "Укажите полный URL сервера, например https://brain.example.com")
        XCTAssertFalse(state.isConfigured)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testBootstrapWithStoredConfigVerifiesToken() async throws {
        let url = URL(string: "https://brain.example.test")!
        try ConfigStore(directory: directory).save(AppConfig(serverURL: url))
        try keychain.setValue("sb_ui_stored", for: KeychainStore.Account.ui)
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/stats" {
                return .init(body: Data(Fixtures.statsJSON.utf8))
            }
            return .init(body: Data(Fixtures.healthJSON.utf8))
        }

        let state = AppState(
            configStore: ConfigStore(directory: directory),
            keychain: keychain,
            session: StubURLProtocol.session()
        )
        await state.bootstrap()

        XCTAssertTrue(state.isConfigured)
        XCTAssertFalse(state.isOnboardingPresented)
        XCTAssertEqual(state.serverURL, url)
        XCTAssertEqual(state.statusText, "Подключено")
        XCTAssertEqual(state.health?.version, "0aa1357")
        XCTAssertEqual(state.stats?.records.total, 12)
        XCTAssertNil(state.errorMessage)
    }
}

private enum Fixtures {
    static let health = Health(
        status: "ok",
        version: "0aa1357",
        schemaVersion: 3,
        time: Date(timeIntervalSince1970: 1_793_820_610),
        database: .init(status: "ok", fts5: true, error: nil)
    )

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
