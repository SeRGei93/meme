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


    // MARK: - Issuance

    func testDeriveSourceMirrorsServerSlugging() {
        XCTAssertEqual(ClientsModel.deriveSource("Claude Laptop"), "claude-laptop")
        XCTAssertEqual(ClientsModel.deriveSource("MacBook.local"), "macbook-local")
        XCTAssertEqual(ClientsModel.deriveSource("  --Jarvis!!  "), "jarvis")
        XCTAssertEqual(ClientsModel.deriveSource("Иван"), "agent")
        XCTAssertEqual(ClientsModel.deriveSource("agent_1"), "agent-1")
        XCTAssertEqual(ClientsModel.deriveSource(String(repeating: "a", count: 100)).count, 64)
    }

    func testValidationRejectsInputTheServerWouldReject() {
        XCTAssertEqual(
            ClientsModel.validationMessage(name: "", source: "jarvis", scopes: [.ingest]),
            "Укажите имя клиента."
        )
        XCTAssertNotNil(ClientsModel.validationMessage(
            name: "bad name", source: "jarvis", scopes: [.ingest]
        ))
        XCTAssertEqual(
            ClientsModel.validationMessage(name: "jarvis", source: "", scopes: [.ingest]),
            "Укажите source."
        )
        XCTAssertNotNil(ClientsModel.validationMessage(
            name: "jarvis", source: "-jarvis", scopes: [.ingest]
        ))
        XCTAssertNotNil(ClientsModel.validationMessage(
            name: "jarvis", source: String(repeating: "a", count: 65), scopes: [.ingest]
        ))
        XCTAssertEqual(
            ClientsModel.validationMessage(name: "jarvis", source: "jarvis", scopes: []),
            "Выберите хотя бы один scope."
        )
        XCTAssertNil(ClientsModel.validationMessage(
            name: "Claude-Laptop", source: "claude-laptop", scopes: [.mcpRead, .mcpWrite]
        ))
    }

    func testCreateIssuesAnAgentClientAndReturnsTheTokenOnce() async throws {
        let store = ClientStore([
            StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
        ])
        StubURLProtocol.setHandler { request in
            switch (request.httpMethod, request.url?.path) {
            case ("GET", "/api/v1/clients"):
                return .init(body: StatusFixtures.clientsData(store.clients))
            case ("POST", "/api/v1/clients"):
                let item = StatusFixtures.client(
                    id: 5,
                    name: "claude-laptop",
                    kind: "agent",
                    source: "claude-laptop",
                    scopes: ["ingest", "mcp:read", "mcp:write"]
                )
                store.clients.append(item)
                let body: [String: Any] = ["client": item, "token": "sb_agent_new"]
                return .init(
                    status: 201,
                    body: (try? JSONSerialization.data(withJSONObject: body)) ?? Data()
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let issued = await model.create(
            name: " Claude-Laptop ",
            source: "Claude.Laptop",
            scopes: [.ingest, .mcpRead, .mcpWrite]
        )

        let token = try XCTUnwrap(issued)
        XCTAssertEqual(token.token, "sb_agent_new")
        XCTAssertEqual(token.client.id, 5)
        XCTAssertEqual(token.client.source, "claude-laptop")

        let create = try XCTUnwrap(
            StubURLProtocol.requests.first { $0.httpMethod == "POST" }
        )
        let body = try XCTUnwrap(create.bodyData)
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: body) as? [String: Any]
        )
        XCTAssertEqual(json["name"] as? String, "Claude-Laptop")
        XCTAssertEqual(json["kind"] as? String, "agent")
        XCTAssertEqual(json["source"] as? String, "claude.laptop")
        XCTAssertEqual(json["scopes"] as? [String], ["ingest", "mcp:read", "mcp:write"])
        XCTAssertEqual(model.clients.map(\.id), [5, 3])
        XCTAssertNil(model.actionError)
    }

    func testCreateConflictUsesAFieldSpecificMessage() async {
        StubURLProtocol.setHandler { request in
            if request.httpMethod == "GET" {
                return .init(body: StatusFixtures.clientsData([]))
            }
            return conflictResponse("client source is already in use", field: "source")
        }
        let sourceModel = ClientsModel(client: client())
        let sourceToken = await sourceModel.create(
            name: "new-agent", source: "jarvis", scopes: [.ingest]
        )
        XCTAssertNil(sourceToken)
        XCTAssertEqual(
            sourceModel.actionError,
            "Этот source уже занят активным клиентом. Выберите другой."
        )

        StubURLProtocol.setHandler { request in
            if request.httpMethod == "GET" {
                return .init(body: StatusFixtures.clientsData([]))
            }
            return conflictResponse("client name is already in use", field: "name")
        }
        let nameModel = ClientsModel(client: client())
        let nameToken = await nameModel.create(
            name: "jarvis", source: "jarvis-2", scopes: [.ingest]
        )
        XCTAssertNil(nameToken)
        XCTAssertEqual(
            nameModel.actionError,
            "Это имя уже занято активным клиентом. Выберите другое."
        )
    }

    func testCreateForbiddenAndValidationErrorsReachTheForm() async {
        StubURLProtocol.setHandler { _ in
            .init(
                status: 403,
                body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
            )
        }
        let forbiddenModel = ClientsModel(client: client())
        let forbidden = await forbiddenModel.create(
            name: "new-agent", source: "new-agent", scopes: [.ingest]
        )
        XCTAssertNil(forbidden)
        XCTAssertEqual(forbiddenModel.actionError, "У токена нет доступа к этому действию")

        StubURLProtocol.setHandler { _ in
            .init(
                status: 422,
                body: Data(
                    #"{"error":{"code":"validation_failed","message":"source must be a lowercase slug","details":{"field":"source"}}}"#.utf8
                )
            )
        }
        let invalidModel = ClientsModel(client: client())
        let invalid = await invalidModel.create(
            name: "new-agent", source: "new-agent", scopes: [.ingest]
        )
        XCTAssertNil(invalid)
        XCTAssertEqual(
            invalidModel.actionError,
            "Сервер отклонил данные: source must be a lowercase slug"
        )
    }

    func testCreateRefusesInvalidInputWithoutCallingTheServer() async {
        let model = ClientsModel(client: client())
        let issued = await model.create(name: "bad name", source: "jarvis", scopes: [.ingest])

        XCTAssertNil(issued)
        XCTAssertNotNil(model.actionError)
        XCTAssertFalse(StubURLProtocol.requests.contains { $0.httpMethod == "POST" })
    }

    func testCreateWithoutAServerExplainsTheProblem() async {
        let model = ClientsModel()
        let issued = await model.create(name: "jarvis", source: "jarvis", scopes: [.ingest])

        XCTAssertNil(issued)
        XCTAssertEqual(
            model.actionError,
            "Нет доступа к серверу. Проверьте подключение в Settings."
        )
    }

    func testRotateRevokesTheOldClientAndKeepsSourceAndScopes() async throws {
        let store = ClientStore([
            StatusFixtures.client(
                id: 3,
                name: "jarvis",
                kind: "agent",
                source: "jarvis",
                scopes: ["ingest", "mcp:read", "mcp:write", "admin"]
            ),
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
                        revokedAt: "2026-11-06T12:00:00Z",
                        scopes: ["ingest", "mcp:read", "mcp:write", "admin"]
                    ),
                ]
                return .init(status: 204, body: Data())
            case ("POST", "/api/v1/clients"):
                let item = StatusFixtures.client(
                    id: 6,
                    name: "jarvis",
                    kind: "agent",
                    source: "jarvis",
                    scopes: ["ingest", "mcp:read", "mcp:write", "admin"]
                )
                store.clients.append(item)
                let body: [String: Any] = ["client": item, "token": "sb_agent_rotated"]
                return .init(
                    status: 201,
                    body: (try? JSONSerialization.data(withJSONObject: body)) ?? Data()
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let old = try XCTUnwrap(model.clients.first)

        let rotated = await model.rotate(old)
        let issued = try XCTUnwrap(rotated)

        XCTAssertEqual(issued.token, "sb_agent_rotated")
        XCTAssertEqual(issued.client.id, 6)
        XCTAssertEqual(model.clients.map(\.id), [6, 3])
        XCTAssertNotNil(model.clients.first { $0.id == 3 }?.revokedAt)
        XCTAssertNil(model.actionError)

        let create = try XCTUnwrap(
            StubURLProtocol.requests.first { $0.httpMethod == "POST" }
        )
        let body = try XCTUnwrap(create.bodyData)
        let json = try XCTUnwrap(
            JSONSerialization.jsonObject(with: body) as? [String: Any]
        )
        XCTAssertEqual(json["name"] as? String, "jarvis")
        XCTAssertEqual(json["source"] as? String, "jarvis")
        XCTAssertEqual(
            json["scopes"] as? [String],
            ["ingest", "mcp:read", "mcp:write", "admin"],
            "rotation must preserve scopes the form never offers"
        )
        let methods = StubURLProtocol.requests.map(\.httpMethod)
        let deleteIndex = methods.firstIndex(of: "DELETE")
        let postIndex = methods.firstIndex(of: "POST")
        XCTAssertNotNil(deleteIndex)
        XCTAssertNotNil(postIndex)
        XCTAssertLessThan(deleteIndex ?? .max, postIndex ?? .max)
    }

    func testRotateRefusesUnknownScopesBeforeRevoking() async {
        let model = ClientsModel(client: client())
        let info = ClientInfo(
            id: 7,
            name: "future-agent",
            kind: "agent",
            scopes: ["ingest", "future:scope"],
            source: "future-agent",
            createdAt: Date(),
            lastSeenAt: nil,
            revokedAt: nil
        )

        let rotated = await model.rotate(info)

        XCTAssertNil(rotated)
        XCTAssertTrue(model.actionError?.contains("CLI") == true)
        XCTAssertFalse(StubURLProtocol.requests.contains { $0.httpMethod == "DELETE" })
    }

    func testRotateExplainsWhenTheReplacementCannotBeCreated() async throws {
        let store = ClientStore([
            StatusFixtures.client(
                id: 3,
                name: "jarvis",
                kind: "agent",
                source: "jarvis",
                scopes: ["ingest"]
            ),
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
                        revokedAt: "2026-11-06T12:00:00Z",
                        scopes: ["ingest"]
                    ),
                ]
                return .init(status: 204, body: Data())
            case ("POST", "/api/v1/clients"):
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = ClientsModel(client: client())
        await model.load()
        let old = try XCTUnwrap(model.clients.first)

        let rotated = await model.rotate(old)
        XCTAssertNil(rotated)
        XCTAssertTrue(model.actionError?.hasPrefix("Старый токен отозван") == true)
        XCTAssertNotNil(model.clients.first { $0.id == 3 }?.revokedAt)
    }

    func testRotateRefusesUIAndSourceLessClients() async {
        let model = ClientsModel(client: client())
        let ui = ClientInfo(
            id: 1,
            name: "second-brain-app",
            kind: "ui",
            scopes: ["ui", "admin"],
            source: nil,
            createdAt: Date(),
            lastSeenAt: nil,
            revokedAt: nil
        )
        XCTAssertFalse(model.canRotate(ui))
        let rotatedUI = await model.rotate(ui)
        XCTAssertNil(rotatedUI)
        XCTAssertNotNil(model.actionError)

        model.localOpenCodeClientID = 2
        let localOpenCode = ClientInfo(
            id: 2,
            name: "opencode-macbook",
            kind: "agent",
            scopes: ["ingest"],
            source: "opencode",
            createdAt: Date(),
            lastSeenAt: nil,
            revokedAt: nil
        )
        XCTAssertFalse(model.canRotate(localOpenCode))
        let rotatedLocal = await model.rotate(localOpenCode)
        XCTAssertNil(rotatedLocal)
        XCTAssertTrue(model.actionError?.contains("Settings") == true)

        let sourceLess = ClientInfo(
            id: 9,
            name: "agent",
            kind: "agent",
            scopes: ["ingest"],
            source: nil,
            createdAt: Date(),
            lastSeenAt: nil,
            revokedAt: nil
        )
        XCTAssertFalse(model.canRotate(sourceLess))
        let rotatedSourceLess = await model.rotate(sourceLess)
        XCTAssertNil(rotatedSourceLess)
        XCTAssertEqual(
            model.actionError,
            "Ротация доступна только активным агентам с source."
        )
        XCTAssertFalse(StubURLProtocol.requests.contains { $0.httpMethod == "DELETE" })
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

private func conflictResponse(_ message: String, field: String) -> StubURLProtocol.Response {
    .init(
        status: 409,
        body: Data(
            ("{\"error\":{\"code\":\"conflict\",\"message\":\"\(message)\","
                + "\"details\":{\"field\":\"\(field)\"}}}").utf8
        )
    )
}
