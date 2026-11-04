import Foundation
import XCTest
@testable import SecondBrain

final class OnboardingServiceTests: XCTestCase {
    private var directory: URL!
    private var server: FakeServer!
    private var keychain: KeychainStore!
    private var service: OnboardingService!
    private let serverURL = URL(string: "https://brain.example.test")!

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

    func testRunCreatesThreeClientsAndPersistsEverything() async throws {
        try ConfigStore(directory: directory).save(
            AppConfig(spoolDir: "/tmp/second-brain-spool", idleDebounceMilliseconds: 300_000)
        )

        let result = try await service.run(serverURL: serverURL, adminToken: "admin-secret")

        XCTAssertEqual(result.health.status, "ok")
        XCTAssertEqual(result.serverURL, serverURL)

        XCTAssertEqual(server.created.map(\.name), [
            "opencode-macbook", "jarvis", "second-brain-app",
        ])
        XCTAssertEqual(server.created.map(\.kind), [.opencode, .jarvis, .ui])
        XCTAssertEqual(server.created[0].scopes, [.ingest, .mcpRead, .mcpWrite])
        XCTAssertEqual(server.created[1].scopes, [.ingest, .mcpRead, .mcpWrite])
        XCTAssertEqual(server.created[2].scopes, [.ui, .admin])

        // The existing active client with the same name is revoked first.
        XCTAssertTrue(server.revoked.contains(7))

        let opencodeToken = try XCTUnwrap(try keychain.value(for: KeychainStore.Account.opencode))
        let jarvisToken = try XCTUnwrap(try keychain.value(for: KeychainStore.Account.jarvis))
        let uiToken = try XCTUnwrap(try keychain.value(for: KeychainStore.Account.ui))
        XCTAssertEqual(opencodeToken, server.token(kind: "opencode"))
        XCTAssertEqual(jarvisToken, server.token(kind: "jarvis"))
        XCTAssertEqual(uiToken, server.token(kind: "ui"))
        XCTAssertEqual(server.lastStatsToken, uiToken)

        let config = try XCTUnwrap(try ConfigStore(directory: directory).load())
        XCTAssertEqual(config.serverURL, serverURL)
        XCTAssertEqual(config.spoolDir, "/tmp/second-brain-spool")
        XCTAssertEqual(config.idleDebounceMilliseconds, 300_000)

        let tokenFile = MCPTokenFile(directory: directory)
        XCTAssertEqual(try tokenFile.read(), opencodeToken)
        XCTAssertEqual(try permissions(of: tokenFile.fileURL), 0o600)
    }

    func testSecondRunRotatesEveryClient() async throws {
        _ = try await service.run(serverURL: serverURL, adminToken: "admin-secret")
        let firstTokens = try [
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.opencode)),
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.jarvis)),
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.ui)),
        ]

        let result = try await service.run(serverURL: serverURL, adminToken: "admin-secret")

        XCTAssertEqual(server.created.count, 6)
        XCTAssertEqual(Set(server.revoked), Set([7, 1, 2, 3]))
        XCTAssertEqual(result.clientIDs.count, 3)

        let secondTokens = try [
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.opencode)),
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.jarvis)),
            XCTUnwrap(try keychain.value(for: KeychainStore.Account.ui)),
        ]
        XCTAssertNotEqual(firstTokens, secondTokens)
    }

    func testUnauthorizedAdminTokenWritesNothing() async throws {
        server.rejectAdmin = true

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: "wrong")
        ) { error in
            guard case APIError.unauthorized = error else {
                return XCTFail("expected .unauthorized, got \(error)")
            }
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
            try await service.run(serverURL: serverURL, adminToken: "admin-secret")
        ) { error in
            XCTAssertEqual(error as? OnboardingError, .serverUnhealthy)
        }
        XCTAssertTrue(server.created.isEmpty)
        XCTAssertNil(try ConfigStore(directory: directory).load())
    }

    func testUnreachableServerKeepsOnboardingIncomplete() async throws {
        StubURLProtocol.setHandler { _ in throw URLError(.cannotConnectToHost) }

        await XCTAssertThrowsErrorAsync(
            try await service.run(serverURL: serverURL, adminToken: "admin-secret")
        ) { error in
            guard case APIError.transport = error else {
                return XCTFail("expected .transport, got \(error)")
            }
        }
        XCTAssertNil(try keychain.value(for: KeychainStore.Account.ui))
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}

// MARK: - Fake server

private final class FakeServer: @unchecked Sendable {
    private let lock = NSLock()
    private var activeClients: [ClientInfo]
    private var createdClients: [CreateClientRequest] = []
    private var revokedIDs: [Int64] = []
    private var tokensByKind: [String: String] = [:]
    private var nextID: Int64 = 1

    var healthStatus = 200
    var healthBody = """
    {"status":"ok","version":"test","schema_version":3,
     "time":"2026-11-04T19:30:10Z",
     "database":{"status":"ok","fts5":true}}
    """
    var rejectAdmin = false
    private(set) var lastStatsToken: String?

    init(activeClients: [ClientInfo]) {
        self.activeClients = activeClients
    }

    var created: [CreateClientRequest] { lock.withLock { createdClients } }
    var revoked: [Int64] { lock.withLock { revokedIDs } }

    func token(kind: String) -> String? {
        lock.withLock { tokensByKind[kind] }
    }

    func handle(_ request: URLRequest) throws -> StubURLProtocol.Response {
        let path = request.url?.path ?? ""
        let method = request.httpMethod ?? "GET"
        let authorization = request.value(forHTTPHeaderField: "Authorization")

        if method == "GET", path == "/health" {
            return .init(status: healthStatus, body: Data(healthBody.utf8))
        }

        if rejectAdmin {
            return .init(status: 401, body: Data(#"{"error":{"code":"unauthorized","message":"bad admin"}}"#.utf8))
        }

        switch (method, path) {
        case ("GET", "/api/v1/clients"):
            return .init(body: try encode(ClientList(items: lock.withLock { activeClients })))

        case ("POST", "/api/v1/clients"):
            let body = try XCTUnwrap(request.bodyData)
            let spec = try JSONDecoder().decode(CreateClientRequest.self, from: body)
            let client = lock.withLock { () -> ClientInfo in
                let id = nextID
                nextID += 1
                createdClients.append(spec)
                let info = ClientInfo(
                    id: id,
                    name: spec.name,
                    kind: spec.kind.rawValue,
                    scopes: spec.scopes.map(\.rawValue),
                    source: spec.kind == .opencode ? "opencode" : (spec.kind == .jarvis ? "jarvis" : nil),
                    createdAt: Date(timeIntervalSince1970: 0),
                    lastSeenAt: nil,
                    revokedAt: nil
                )
                tokensByKind[spec.kind.rawValue] = "sb_\(spec.kind.rawValue)_\(id)"
                activeClients.append(info)
                return info
            }
            let token = lock.withLock { tokensByKind[client.kind] } ?? ""
            return .init(
                status: 201,
                body: try encode(CreateClientResponse(client: client, token: token))
            )

        case ("DELETE", _) where path.hasPrefix("/api/v1/clients/"):
            let id = try XCTUnwrap(Int64(path.split(separator: "/").last ?? ""))
            lock.withLock {
                revokedIDs.append(id)
                activeClients.removeAll { $0.id == id }
            }
            return .init(status: 204, body: Data())

        case ("GET", "/api/v1/stats"):
            let token = authorization?.replacingOccurrences(of: "Bearer ", with: "")
            lock.withLock { lastStatsToken = token }
            let expected = lock.withLock { tokensByKind["ui"] }
            guard token == expected else {
                return .init(status: 401, body: Data(#"{"error":{"code":"unauthorized","message":"bad ui token"}}"#.utf8))
            }
            return .init(body: Data(Self.statsJSON.utf8))

        default:
            return .init(status: 404, body: Data())
        }
    }

    private func encode<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return try encoder.encode(value)
    }

    private static let statsJSON = """
    {"records":{"total":1,"final":1,"draft":0,
      "by_source":{"opencode":1,"jarvis":0},
      "by_card_status":{"heuristic":0,"queued":0,"ok":1,"failed":0}},
     "projects":1,"clients":3,
     "jobs":{"queued":0,"running":0,"done":0,"failed":0},
     "database":{"status":"ok","schema_version":3,"size_bytes":1024}}
    """
}
