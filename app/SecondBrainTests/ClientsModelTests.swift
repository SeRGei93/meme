import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class ClientsModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testLoadSortsActiveFirstAndKeepsRevokedHistory() async throws {
        StubURLProtocol.setHandler { request in
            XCTAssertEqual(request.url?.path, "/api/v1/clients")
            return .init(body: StatusFixtures.clientsData([
                StatusFixtures.client(
                    id: 4,
                    name: "old-agent",
                    kind: "agent",
                    source: "old",
                    revokedAt: "2026-11-01T00:00:00Z"
                ),
                StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
                StatusFixtures.client(
                    id: 5,
                    name: "older-agent",
                    kind: "agent",
                    source: "older",
                    revokedAt: "2026-10-01T00:00:00Z"
                ),
                StatusFixtures.client(id: 2, name: "claude", kind: "agent", source: "claude"),
            ]))
        }

        let model = ClientsModel(client: client())
        model.localUIClientID = 1
        model.localOpenCodeClientID = 2
        await model.load()

        XCTAssertNil(model.error)
        XCTAssertFalse(model.isLoading)
        XCTAssertEqual(model.clients.map(\.id), [2, 3, 1, 4, 5])
        XCTAssertEqual(model.activeClients.map(\.id), [2, 3, 1])
        XCTAssertEqual(model.revokedClients.map(\.id), [4, 5])
        XCTAssertTrue(model.isLocalUI(try XCTUnwrap(model.clients.first { $0.id == 1 })))
        XCTAssertFalse(model.canRevoke(try XCTUnwrap(model.clients.first { $0.id == 1 })))
        XCTAssertTrue(model.canRevoke(try XCTUnwrap(model.clients.first { $0.id == 2 })))
        XCTAssertTrue(model.canRevoke(try XCTUnwrap(model.clients.first { $0.id == 3 })))
    }

    private final class ClientStore: @unchecked Sendable {
        var clients: [[String: Any]]

        init(_ clients: [[String: Any]]) {
            self.clients = clients
        }
    }

    func testRevokeDeletesClientAndReloadsTheList() async throws {
        let store = ClientStore([
            StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
        ])
        StubURLProtocol.setHandler { request in
            switch (request.httpMethod, request.url?.path) {
            case ("GET", "/api/v1/clients"):
                return .init(body: StatusFixtures.clientsData(store.clients))
            case ("DELETE", "/api/v1/clients/3"):
                store.clients = [
                    StatusFixtures.client(
                        id: 3,
                        name: "jarvis",
                        kind: "agent",
                        source: "jarvis",
                        revokedAt: "2026-11-06T10:00:00Z"
                    ),
                ]
                return .init(status: 204, body: Data())
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let jarvis = try XCTUnwrap(model.clients.first)

        let revoked = await model.revoke(jarvis)

        XCTAssertTrue(revoked)
        XCTAssertNil(model.actionError)
        XCTAssertEqual(model.clients.first?.revokedAt != nil, true)
        XCTAssertTrue(model.activeClients.isEmpty)
        XCTAssertEqual(model.revokedClients.count, 1)
    }

    func testRevokeRefusesTheLocalUIClientWithoutCallingTheServer() async throws {
        StubURLProtocol.setHandler { request in
            XCTAssertEqual(request.httpMethod, "GET")
            return .init(body: StatusFixtures.clientsData([
                StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
            ]))
        }

        let model = ClientsModel(client: client())
        model.localUIClientID = 1
        await model.load()
        let app = try XCTUnwrap(model.clients.first)

        let revoked = await model.revoke(app)

        XCTAssertFalse(revoked)
        XCTAssertEqual(
            model.actionError,
            "Этот клиент используется приложением и не может быть отозван из него."
        )
        XCTAssertFalse(StubURLProtocol.requests.contains { $0.httpMethod == "DELETE" })
    }

    func testRevokeForbiddenKeepsTheListAndShowsTheServerMessage() async throws {
        StubURLProtocol.setHandler { request in
            switch request.httpMethod {
            case "GET":
                return .init(body: StatusFixtures.clientsData([
                    StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                ]))
            default:
                return .init(
                    status: 403,
                    body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
                )
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let jarvis = try XCTUnwrap(model.clients.first)

        let revoked = await model.revoke(jarvis)

        XCTAssertFalse(revoked)
        XCTAssertEqual(model.actionError, "У токена нет доступа к этому действию")
        XCTAssertEqual(model.clients.count, 1)
        XCTAssertNil(model.clients.first?.revokedAt)
    }

    func testUIProtectionFallsBackWhenConfigIsMissing() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: StatusFixtures.clientsData([
                StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
                StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
            ]))
        }

        let model = ClientsModel(client: client())
        await model.load()

        let app = try XCTUnwrap(model.clients.first { $0.id == 1 })
        let jarvis = try XCTUnwrap(model.clients.first { $0.id == 3 })
        XCTAssertTrue(model.isProtectedUI(app))
        XCTAssertFalse(model.canRevoke(app))
        XCTAssertFalse(model.isLocalUI(app), "without config the app cannot claim a specific client")
        XCTAssertTrue(model.canRevoke(jarvis))
    }

    func testUIProtectionFallsBackWhenTheConfiguredIDIsStale() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: StatusFixtures.clientsData([
                StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
                StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
            ]))
        }

        let model = ClientsModel(client: client())
        model.localUIClientID = 999
        await model.load()

        let app = try XCTUnwrap(model.clients.first { $0.id == 1 })
        XCTAssertTrue(model.isProtectedUI(app))
        XCTAssertFalse(model.canRevoke(app))
    }

    func testRevokeReturnsFalseWhenTheRefreshFails() async throws {
        final class Attempts: @unchecked Sendable {
            var gets = 0
        }
        let attempts = Attempts()
        StubURLProtocol.setHandler { request in
            switch request.httpMethod {
            case "GET":
                attempts.gets += 1
                if attempts.gets == 1 {
                    return .init(body: StatusFixtures.clientsData([
                        StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                    ]))
                }
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            default:
                return .init(status: 204, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let jarvis = try XCTUnwrap(model.clients.first)

        let revoked = await model.revoke(jarvis)

        XCTAssertFalse(revoked)
        XCTAssertNotNil(model.error)
        XCTAssertEqual(model.clients.map(\.id), [3], "the last good list stays visible")
    }

    func testEmptyListIsNotAnError() async {
        StubURLProtocol.setHandler { _ in
            .init(body: StatusFixtures.clientsData([]))
        }

        let model = ClientsModel(client: client())
        await model.load()

        XCTAssertTrue(model.clients.isEmpty)
        XCTAssertTrue(model.activeClients.isEmpty)
        XCTAssertTrue(model.revokedClients.isEmpty)
        XCTAssertNil(model.error)
        XCTAssertFalse(model.isLoading)
    }

    func testRetryRecoversAfterATransientFailure() async {
        final class Attempts: @unchecked Sendable {
            var count = 0
        }
        let attempts = Attempts()
        StubURLProtocol.setHandler { _ in
            attempts.count += 1
            if attempts.count == 1 {
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            }
            return .init(body: StatusFixtures.clientsData([
                StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
            ]))
        }

        let model = ClientsModel(client: client())
        await model.load()
        XCTAssertNotNil(model.error)

        await model.retry()

        XCTAssertNil(model.error)
        XCTAssertEqual(model.clients.map(\.id), [3])
    }

    func testRevokeNotFoundExplainsAndRefreshes() async throws {
        StubURLProtocol.setHandler { request in
            switch request.httpMethod {
            case "GET":
                return .init(body: StatusFixtures.clientsData([]))
            default:
                return .init(
                    status: 404,
                    body: Data(#"{"error":{"code":"not_found","message":"client not found"}}"#.utf8)
                )
            }
        }

        let model = ClientsModel(client: client())
        let stale = ClientInfo(
            id: 9,
            name: "gone",
            kind: "agent",
            scopes: ["ingest"],
            source: "gone",
            createdAt: Date(),
            lastSeenAt: nil,
            revokedAt: nil
        )
        let revoked = await model.revoke(stale)

        XCTAssertFalse(revoked)
        XCTAssertEqual(model.actionError, "Клиента уже нет на сервере.")
        XCTAssertTrue(model.clients.isEmpty)
    }

    func testSuccessfulRefreshClearsAStaleActionError() async throws {
        final class State: @unchecked Sendable {
            var deny = true
        }
        let state = State()
        StubURLProtocol.setHandler { request in
            switch request.httpMethod {
            case "GET":
                return .init(body: StatusFixtures.clientsData([
                    StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                ]))
            default:
                return state.deny
                    ? .init(
                        status: 403,
                        body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
                    )
                    : .init(status: 204, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        _ = await model.revoke(try XCTUnwrap(model.clients.first))
        XCTAssertNotNil(model.actionError)

        state.deny = false
        await model.load()

        XCTAssertNil(model.actionError)
        XCTAssertNil(model.clients.first?.revokedAt)
    }

    func testLoadFailureShowsRetryState() async throws {
        StubURLProtocol.setHandler { _ in
            .init(
                status: 500,
                body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
            )
        }

        let model = ClientsModel(client: client())
        await model.load()

        XCTAssertTrue(model.clients.isEmpty)
        XCTAssertNotNil(model.error)
        XCTAssertFalse(model.isLoading)
    }

    func testNoClientExplainsHowToConnect() async {
        let model = ClientsModel()
        await model.load()

        XCTAssertEqual(
            model.error,
            "Нет доступа к серверу. Проверьте подключение в Settings."
        )
        XCTAssertFalse(model.isLoading)
    }

    private func client() -> APIClient {
        APIClient(
            baseURL: baseURL,
            token: "sb_ui_test",
            session: StubURLProtocol.session()
        )
    }
}
