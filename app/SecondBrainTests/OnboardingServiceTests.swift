import Foundation
import XCTest
@testable import SecondBrain

final class OnboardingServiceTests: XCTestCase {
    private var directory: URL!
    private var server: FakeServer!
    private var keychain: KeychainStore!
    private var service: OnboardingService!
    private let serverURL = URL(string: "https://brain.example.test")!
    private let adminToken = "admin-secret"

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-onboarding-\(UUID().uuidString)", isDirectory: true)
        keychain = KeychainStore(service: "com.serg.secondbrain.tests.\(UUID().uuidString)")
        server = FakeServer(activeClients: [
            .init(
                id: 7, name: "opencode-macbook", kind: "opencode",
                scopes: ["ingest"], source: "opencode",
                createdAt: Date(timeIntervalSince1970: 0),
                lastSeenAt: nil, revokedAt: nil
            ),
        ])
        StubURLProtocol.reset()
        StubURLProtocol.setHandler { [server] request in
            try XCTUnwrap(server).handle(request)
        }
        service = OnboardingService(
            configStore: ConfigStore(directory: directory),
            keychain: keychain,
            tokenFile: MCPTokenFile(directory: directory),
            hostName: "MacBook.local",
            session: StubURLProtocol.session()
        )
    }

    override func tearDownWithError() throws {
        try? keychain.deleteAll()
        try? FileManager.default.removeItem(at: directory)
        StubURLProtocol.reset()
        service = nil
        server = nil
        keychain = nil
        directory = nil
        try super.tearDownWithError()
    }

    func testFirstRunCreatesClientsAndPersistsEverything() async throws {
        try ConfigStore(directory: directory).save(
            AppConfig(spoolDir: "/tmp/second-brain-spool", idleDebounceMilliseconds: 300_000)
        )

        let result = try await service.run(serverURL: serverURL, adminToken: adminToken)

        XCTAssertEqual(result.health.status, "ok")
        XCTAssertEqual(result.serverURL, serverURL)

        XCTAssertEqual(server.created.count, 2)
        XCTAssertEqual(server.created.map(\.kind), [.agent, .ui])
        XCTAssertEqual(server.created[0].scopes, [.ingest, .mcpRead, .mcpWrite])
        XCTAssertEqual(server.created[1].scopes, [.ui, .admin])

        // The legacy `opencode-macbook` client is left alone; the new client
        // gets a unique suffix instead.
        XCTAssertTrue(server.created[0].name.hasPrefix("opencode-macbook-"))
        XCTAssertEqual(server.created[1].name, "second-brain-app")
        XCTAssertTrue(server.revoked.isEmpty)
        XCTAssertTrue(server.isActive(id: 7))

        let opencodeToken = try XCTUnwrap(try keychain.value(for: KeychainStore.Account.opencode))
        let uiToken = try XCTUnwrap(try keychain.value(for: KeychainStore.Account.ui))
        XCTAssertEqual(opencodeToken, server.activeToken(kind: "agent"))
        XCTAssertEqual(uiToken, server.activeToken(kind: "ui"))
        XCTAssertNil(try keychain.value(for: "jarvis"))
        XCTAssertEqual(server.lastStatsToken, uiToken)

        let config = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertEqual(config.serverURL, serverURL)
        XCTAssertEqual(config.spoolDir, "/tmp/second-brain-spool")
        XCTAssertEqual(config.idleDebounceMilliseconds, 300_000)
        XCTAssertEqual(config.appClients?.count, 2)
        XCTAssertEqual(config.appClients?["ui"]?.id, server.client(named: "second-brain-app")?.id)

        let tokenFile = MCPTokenFile(directory: directory)
        XCTAssertEqual(try tokenFile.read(), opencodeToken)
        XCTAssertEqual(try permissions(of: tokenFile.fileURL), 0o600)

        // The bootstrap admin token must never be persisted locally.
        XCTAssertNotEqual(opencodeToken, adminToken)
        XCTAssertNotEqual(uiToken, adminToken)
        let configText = try String(contentsOf: configFileURL, encoding: .utf8)
        XCTAssertFalse(configText.contains(adminToken))
        XCTAssertFalse(try XCTUnwrap(try tokenFile.read()).contains(adminToken))
    }

    func testSecondRunRotatesOwnClientsOnly() async throws {
        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)
        let firstConfig = try XCTUnwrap(try ConfigStore(directory: directory).load())
        let firstClients = try XCTUnwrap(firstConfig.appClients)
        let firstTokens = try tokens()

        let second = try await service.run(serverURL: serverURL, adminToken: adminToken)

        XCTAssertEqual(server.created.count, 4)
        let secondConfig = try XCTUnwrap(try ConfigStore(directory: directory).load())
        let secondClients = try XCTUnwrap(secondConfig.appClients)
        XCTAssertEqual(secondClients.count, 2)

        for role in ["opencode", "ui"] {
            let old = try XCTUnwrap(firstClients[role])
            let new = try XCTUnwrap(secondClients[role])
            XCTAssertNotEqual(old.id, new.id)
            XCTAssertTrue(server.revoked.contains(old.id), "old \(role) client must be revoked")
            XCTAssertTrue(server.isActive(id: new.id))
        }

        XCTAssertTrue(server.isActive(id: 7), "legacy client is not ours and must stay active")
        XCTAssertNotEqual(firstTokens, try tokens())
        XCTAssertEqual(second.clientIDs.count, 2)
    }

    func testFailedRotationKeepsPreviousTokensAndClients() async throws {
        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)
        let configBefore = try XCTUnwrap(try ConfigStore(directory: directory).load())
        let tokensBefore = try tokens()
        let mcpBefore = try XCTUnwrap(try MCPTokenFile(directory: directory).read())
        let idsBefore = Set(try XCTUnwrap(configBefore.appClients).values.map(\.id))

        server.failCreateKinds = ["ui"]

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: adminToken)
        )

        XCTAssertEqual(try tokens(), tokensBefore)
        XCTAssertEqual(try MCPTokenFile(directory: directory).read(), mcpBefore)
        let configAfter = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertEqual(configAfter.appClients, configBefore.appClients)
        for id in idsBefore {
            XCTAssertTrue(server.isActive(id: id), "previous client \(id) must survive a failed rotation")
            XCTAssertFalse(server.revoked.contains(id))
        }

        // Partially created clients are cleaned up.
        XCTAssertFalse(server.activeClients.contains { $0.name.hasPrefix("opencode-macbook-") && !idsBefore.contains($0.id) })
    }

    func testPendingRevocationsAreRetriedAndRemoved() async throws {
        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)
        let firstIDs = Set(try XCTUnwrap(try ConfigStore(directory: directory).load()?.appClients).values.map(\.id))

        server.failRevokeIDs = firstIDs
        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)

        var config = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertEqual(Set(config.pendingRevocations ?? []), firstIDs)
        for id in firstIDs {
            XCTAssertTrue(server.isActive(id: id))
        }

        server.failRevokeIDs = []
        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)

        config = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertNil(config.pendingRevocations)
        let raw = try String(contentsOf: configFileURL, encoding: .utf8)
        XCTAssertFalse(raw.contains("pending_revocations"))
        for id in firstIDs {
            XCTAssertFalse(server.isActive(id: id), "pending id \(id) must be revoked on the next run")
        }
    }

    func testRevokeOfUnknownPreviousClientIsIgnored() async throws {
        // Simulate a config that points at a client the server no longer knows.
        try ConfigStore(directory: directory).save(
            AppConfig(
                serverURL: serverURL,
                appClients: ["ui": StoredClient(id: 999, name: "gone")]
            )
        )

        _ = try await service.run(serverURL: serverURL, adminToken: adminToken)

        let config = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertNil(config.pendingRevocations)
    }

    func testUnauthorizedAdminTokenMapsToInvalidAdminToken() async throws {
        server.rejectAdmin = true

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: "wrong")
        ) { error in
            XCTAssertEqual(error as? OnboardingError, .invalidAdminToken)
        }

        XCTAssertNil(try keychain.value(for: KeychainStore.Account.ui))
        XCTAssertNil(try ConfigStore(directory: directory).load())
        XCTAssertNil(try? MCPTokenFile(directory: directory).read())
        XCTAssertTrue(server.created.isEmpty)
    }

    func testDegradedServerIsRejectedBeforeCreatingClients() async throws {
        server.healthStatus = 503
        server.healthBody = """
        {"status":"degraded","version":"1","schema_version":3,
         "time":"2026-11-04T19:30:10Z",
         "database":{"status":"error","fts5":false,"error":"disk full"}}
        """

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: adminToken)
        ) { error in
            XCTAssertEqual(error as? OnboardingError, .serverUnhealthy)
        }
        XCTAssertTrue(server.created.isEmpty)
        XCTAssertNil(try ConfigStore(directory: directory).load())
    }

    func testUnreachableServerKeepsOnboardingIncomplete() async throws {
        StubURLProtocol.setHandler { _ in throw URLError(.cannotConnectToHost) }

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: adminToken)
        ) { error in
            guard case APIError.transport = error else {
                return XCTFail("expected .transport, got \(error)")
            }
        }
        XCTAssertNil(try keychain.value(for: KeychainStore.Account.ui))
    }

    func testServerURLRequiresTLSExceptLoopback() throws {
        XCTAssertEqual(
            try OnboardingService.validateServerURL("https://brain.example.com"),
            URL(string: "https://brain.example.com")
        )
        XCTAssertEqual(
            try OnboardingService.validateServerURL(" http://localhost:8080 "),
            URL(string: "http://localhost:8080")
        )
        XCTAssertThrowsError(try OnboardingService.validateServerURL("http://192.168.1.2:8080"))
        XCTAssertThrowsError(try OnboardingService.validateServerURL("brain.example.com"))
        XCTAssertThrowsError(try OnboardingService.validateServerURL("https://brain.example.com?token=leak"))
        XCTAssertThrowsError(try OnboardingService.validateServerURL("https://brain.example.com#fragment"))
    }

    func testHostSlugIsASCIIAndBounded() {
        XCTAssertEqual(OnboardingService.hostSlug("MacBook-Pro-serg.local"), "macbook-pro-serg")
        XCTAssertEqual(OnboardingService.hostSlug("Маков.local"), "")
        XCTAssertLessThanOrEqual(OnboardingService.hostSlug(String(repeating: "a", count: 200)).count, 60)
    }

    private var configFileURL: URL {
        directory.appendingPathComponent("config.json")
    }

    private func tokens() throws -> [String] {
        try [
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.opencode)),
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.ui)),
        ]
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}


// MARK: - Fake server

private final class FakeServer: @unchecked Sendable {
    private let lock = NSLock()
    private var clients: [ClientInfo]
    private var tokens: [Int64: String] = [:]
    private var nextID: Int64
    private var createdSpecs: [CreateClientRequest] = []
    private var revokedIDs: [Int64] = []
    private var statsToken: String?

    var healthStatus = 200
    var healthBody = """
    {"status":"ok","version":"test","schema_version":4,
     "time":"2026-11-04T19:30:10Z",
     "database":{"status":"ok","fts5":true}}
    """
    var rejectAdmin = false
    var failCreateKinds: Set<String> = []
    var failRevokeIDs: Set<Int64> = []

    init(activeClients: [ClientInfo]) {
        clients = activeClients
        nextID = (activeClients.map(\.id).max() ?? 0) + 1
    }

    var created: [CreateClientRequest] { lock.withLock { createdSpecs } }
    var revoked: [Int64] { lock.withLock { revokedIDs } }
    var lastStatsToken: String? { lock.withLock { statsToken } }
    var activeClients: [ClientInfo] { lock.withLock { clients.filter { $0.revokedAt == nil } } }

    func isActive(id: Int64) -> Bool {
        lock.withLock { clients.first { $0.id == id }?.revokedAt == nil }
    }

    func client(named name: String) -> ClientInfo? {
        lock.withLock { clients.first { $0.name == name } }
    }

    func activeToken(kind: String) -> String? {
        lock.withLock {
            guard let client = clients.last(where: {
                $0.kind == kind && $0.revokedAt == nil && tokens[$0.id] != nil
            }) else {
                return nil
            }
            return tokens[client.id]
        }
    }

    func handle(_ request: URLRequest) throws -> StubURLProtocol.Response {
        let path = request.url?.path ?? ""
        let method = request.httpMethod ?? "GET"
        let authorization = request.value(forHTTPHeaderField: "Authorization")

        if method == "GET", path == "/health" {
            return .init(status: healthStatus, body: Data(healthBody.utf8))
        }

        if rejectAdmin {
            return unauthorized("bad admin")
        }

        switch (method, path) {
        case ("GET", "/api/v1/clients"):
            let items = lock.withLock { clients }
            return .init(body: try encode(ClientList(items: items)))

        case ("POST", "/api/v1/clients"):
            let body = try XCTUnwrap(request.bodyData)
            let spec = try JSONDecoder().decode(CreateClientRequest.self, from: body)

            if failCreateKinds.contains(spec.kind.rawValue) {
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            }

            if lock.withLock({ clients.contains { $0.name == spec.name && $0.revokedAt == nil } }) {
                return .init(
                    status: 409,
                    body: Data(#"{"error":{"code":"conflict","message":"name already exists"}}"#.utf8)
                )
            }

            let (client, token) = lock.withLock { () -> (ClientInfo, String) in
                let id = nextID
                nextID += 1
                createdSpecs.append(spec)
                let info = ClientInfo(
                    id: id,
                    name: spec.name,
                    kind: spec.kind.rawValue,
                    scopes: spec.scopes.map(\.rawValue),
                    source: spec.kind.isKnowledgeAgent
                        ? (spec.source ?? spec.name.lowercased())
                        : nil,
                    createdAt: Date(timeIntervalSince1970: 0),
                    lastSeenAt: nil,
                    revokedAt: nil
                )
                let token = "sb_\(spec.kind.isKnowledgeAgent ? "agent" : spec.kind.rawValue)_\(id)"
                clients.append(info)
                tokens[id] = token
                return (info, token)
            }
            return .init(
                status: 201,
                body: try encode(CreateClientResponse(client: client, token: token))
            )

        case ("DELETE", _) where path.hasPrefix("/api/v1/clients/"):
            let id = try XCTUnwrap(Int64(path.split(separator: "/").last ?? ""))
            if lock.withLock({ failRevokeIDs.contains(id) }) {
                return .init(status: 500, body: Data(#"{"error":{"code":"internal_error","message":"revoke failed"}}"#.utf8))
            }
            let found = lock.withLock { () -> Bool in
                guard let index = clients.firstIndex(where: { $0.id == id }),
                      clients[index].revokedAt == nil else {
                    return false
                }
                let old = clients[index]
                clients[index] = ClientInfo(
                    id: old.id, name: old.name, kind: old.kind, scopes: old.scopes,
                    source: old.source, createdAt: old.createdAt,
                    lastSeenAt: old.lastSeenAt, revokedAt: Date()
                )
                tokens.removeValue(forKey: id)
                revokedIDs.append(id)
                return true
            }
            return found ? .init(status: 204, body: Data()) : notFound()

        case ("GET", "/api/v1/stats"):
            let token = authorization?.replacingOccurrences(of: "Bearer ", with: "")
            let valid = lock.withLock {
                clients.contains { $0.kind == "ui" && $0.revokedAt == nil && tokens[$0.id] == token }
            }
            guard valid else {
                return unauthorized("bad ui token")
            }
            lock.withLock { statsToken = token }
            return .init(body: Data(Self.statsJSON.utf8))

        default:
            return notFound()
        }
    }

    private func unauthorized(_ message: String) -> StubURLProtocol.Response {
        .init(
            status: 401,
            body: Data("{\"error\":{\"code\":\"unauthorized\",\"message\":\"\(message)\"}}".utf8)
        )
    }

    private func notFound() -> StubURLProtocol.Response {
        .init(
            status: 404,
            body: Data(#"{"error":{"code":"not_found","message":"client not found"}}"#.utf8)
        )
    }

    private func encode<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return try encoder.encode(value)
    }

    private static let statsJSON = """
    {"records":{"total":1,"final":1,"draft":0,
      "by_source":{"opencode":1},
      "by_card_status":{"heuristic":0,"queued":0,"ok":1,"failed":0}},
     "projects":1,"clients":3,
     "jobs":{"queued":0,"running":0,"done":0,"failed":0},
     "database":{"status":"ok","schema_version":4,"size_bytes":1024}}
    """
}
