import Foundation
import XCTest
@testable import SecondBrain

final class APIClientTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!
    private var client: APIClient!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        client = APIClient(
            configuration: .init(baseURL: baseURL, token: "sb_ui_test"),
            session: StubURLProtocol.session()
        )
    }

    override func tearDown() {
        StubURLProtocol.reset()
        client = nil
        super.tearDown()
    }

    func testHealthDecodesTypedResponse() async throws {
        let body = """
        {
          "status": "ok",
          "version": "0aa1357",
          "schema_version": 3,
          "time": "2026-11-04T19:30:10.123456789Z",
          "database": { "status": "ok", "fts5": true }
        }
        """.data(using: .utf8)!
        StubURLProtocol.setHandler { _ in .init(body: body) }

        let health = try await client.health()

        XCTAssertEqual(health.status, "ok")
        XCTAssertEqual(health.version, "0aa1357")
        XCTAssertEqual(health.schemaVersion, 3)
        XCTAssertTrue(health.database.fts5)
        XCTAssertEqual(health.database.status, "ok")
        XCTAssertNil(health.database.error)
        // Go sends nanosecond precision; the value must survive decoding.
        XCTAssertEqual(health.time.timeIntervalSince1970, 1_793_820_610, accuracy: 1)
    }

    func testHealthIsPublicAndSendsNoAuthorizationHeader() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: Data(Self.healthJSON.utf8))
        }

        _ = try await client.health()

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.url?.absoluteString, "https://brain.example.test/health")
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertNil(request.value(forHTTPHeaderField: "Authorization"))
    }

    func testSearchSendsBearerTokenAndQuery() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: Data(#"{"items":[],"total":0,"query":"spool","limit":5}"#.utf8))
        }

        let response = try await client.search(query: "spool", limit: 5)

        XCTAssertEqual(response.total, 0)
        XCTAssertEqual(response.query, "spool")
        XCTAssertEqual(response.limit, 5)

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer sb_ui_test")
        let components = try XCTUnwrap(URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false))
        XCTAssertEqual(components.path, "/api/v1/search")
        XCTAssertEqual(components.queryItems?.first(where: { $0.name == "q" })?.value, "spool")
        XCTAssertEqual(components.queryItems?.first(where: { $0.name == "limit" })?.value, "5")
    }

    func testSearchSendsSourceAndDateFilters() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: Data(#"{"items":[],"total":0,"query":"x","limit":8}"#.utf8))
        }
        let since = Date(timeIntervalSince1970: 1_793_820_000)
        let until = Date(timeIntervalSince1970: 1_793_906_400)

        _ = try await client.search(
            query: "x",
            projectKey: "github.com/example/repo",
            source: "jarvis",
            since: since,
            until: until
        )

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        let components = try XCTUnwrap(
            URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
        )
        let items = Dictionary(
            uniqueKeysWithValues: (components.queryItems ?? []).compactMap { item in
                item.value.map { (item.name, $0) }
            }
        )
        XCTAssertEqual(items["q"], "x")
        XCTAssertEqual(items["project_key"], "github.com/example/repo")
        XCTAssertEqual(items["source"], "jarvis")
        let parsedSince = try XCTUnwrap(APIDate.parse(try XCTUnwrap(items["since"])))
        let parsedUntil = try XCTUnwrap(APIDate.parse(try XCTUnwrap(items["until"])))
        XCTAssertEqual(parsedSince.timeIntervalSince1970, since.timeIntervalSince1970, accuracy: 0.001)
        XCTAssertEqual(parsedUntil.timeIntervalSince1970, until.timeIntervalSince1970, accuracy: 0.001)
    }

    func testUnauthorizedMapsToUnauthorizedError() async {
        let body = Data(#"{"error":{"code":"unauthorized","message":"missing token","request_id":"req_1"}}"#.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 401, body: body) }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x")) { error in
            guard case APIError.unauthorized(let requestID) = error else {
                return XCTFail("expected .unauthorized, got \(error)")
            }
            XCTAssertEqual(requestID, "req_1")
            XCTAssertEqual((error as? APIError)?.isRetryable, false)
            XCTAssertTrue((error as? APIError)?.requiresReauthentication == true)
        }
    }

    func testValidationErrorCarriesCodeMessageAndRequestID() async {
        let body = Data("""
        {"error":{"code":"validation_failed","message":"limit must be positive","details":{"field":"limit"},"request_id":"req_2"}}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 422, body: body) }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x", limit: 0)) { error in
            guard case APIError.server(let status, let code, let message, let field, let requestID) = error else {
                return XCTFail("expected .server, got \(error)")
            }
            XCTAssertEqual(status, 422)
            XCTAssertEqual(code, "validation_failed")
            XCTAssertEqual(message, "limit must be positive")
            XCTAssertEqual(field, "limit")
            XCTAssertEqual(requestID, "req_2")
        }
    }

    func testRateLimitedIsRetryableAndUsesRetryAfterHeader() async {
        let body = Data(#"{"error":{"code":"rate_limited","message":"slow down"}}"#.utf8)
        StubURLProtocol.setHandler { _ in
            .init(status: 429, headers: ["Content-Type": "application/json", "Retry-After": "7"], body: body)
        }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x")) { error in
            guard case APIError.rateLimited(let retryAfter, let requestID) = error else {
                return XCTFail("expected .rateLimited, got \(error)")
            }
            XCTAssertEqual(retryAfter, 7)
            XCTAssertNil(requestID)
            XCTAssertTrue((error as? APIError)?.isRetryable == true)
        }
    }

    func testRateLimitedFallsBackToEnvelopeDetailsAndKeepsRequestID() async {
        let body = Data("""
        {"error":{"code":"rate_limited","message":"slow down","request_id":"req_9",
          "details":{"retry_after_seconds":12}}}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 429, body: body) }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x")) { error in
            guard case APIError.rateLimited(let retryAfter, let requestID) = error else {
                return XCTFail("expected .rateLimited, got \(error)")
            }
            XCTAssertEqual(retryAfter, 12)
            XCTAssertEqual(requestID, "req_9")
        }
    }

    func testForbiddenAsksForReauthentication() async {
        StubURLProtocol.setHandler { _ in .init(status: 403, body: Data()) }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x")) { error in
            guard case APIError.forbidden(let requestID) = error else {
                return XCTFail("expected .forbidden, got \(error)")
            }
            XCTAssertNil(requestID)
            XCTAssertEqual((error as? APIError)?.requiresReauthentication, true)
        }
    }

    func testNotFoundIsNotRetryable() async {
        StubURLProtocol.setHandler { _ in .init(status: 404, body: Data()) }

        await XCTAssertThrowsErrorAsync(try await client.record(id: 7)) { error in
            guard case APIError.notFound = error else {
                return XCTFail("expected .notFound, got \(error)")
            }
            XCTAssertEqual((error as? APIError)?.isRetryable, false)
        }
    }

    func testAuthenticatedRequestWithoutTokenFailsBeforeNetwork() async {
        let anonymous = APIClient(
            configuration: .init(baseURL: baseURL),
            session: StubURLProtocol.session()
        )

        await XCTAssertThrowsErrorAsync(try await anonymous.stats()) { error in
            guard case APIError.unauthorized(let requestID) = error else {
                return XCTFail("expected .unauthorized, got \(error)")
            }
            XCTAssertNil(requestID)
        }
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testHealthDecodes503BodyAsDegraded() async throws {
        let body = Data("""
        {"status":"degraded","version":"1","schema_version":3,
         "time":"2026-11-04T19:30:10Z",
         "database":{"status":"error","fts5":false,"error":"database is locked"}}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 503, body: body) }

        let health = try await client.health()

        XCTAssertEqual(health.status, "degraded")
        XCTAssertEqual(health.database.status, "error")
        XCTAssertEqual(health.database.error, "database is locked")
    }

    func testSearchSendsProjectKey() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: Data(#"{"items":[],"total":0,"query":"x","limit":10}"#.utf8))
        }

        _ = try await client.search(query: "x", projectKey: "opencode:meme")

        let request = try XCTUnwrap(StubURLProtocol.requests.first)
        let components = try XCTUnwrap(URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false))
        XCTAssertEqual(components.queryItems?.first(where: { $0.name == "project_key" })?.value, "opencode:meme")
        XCTAssertNil(components.queryItems?.first(where: { $0.name == "project" }))
    }

    func testServerErrorMarkedRetryable() async {
        StubURLProtocol.setHandler { _ in .init(status: 503, body: Data()) }

        await XCTAssertThrowsErrorAsync(try await client.stats()) { error in
            guard case APIError.server(let status, _, _, _, _) = error else {
                return XCTFail("expected .server, got \(error)")
            }
            XCTAssertEqual(status, 503)
            XCTAssertTrue((error as? APIError)?.isRetryable == true)
        }
    }

    func testNotImplementedIsNotRetryable() async {
        StubURLProtocol.setHandler { _ in .init(status: 501, body: Data()) }

        await XCTAssertThrowsErrorAsync(try await client.stats()) { error in
            XCTAssertEqual((error as? APIError)?.isRetryable, false)
        }
    }

    func testTransportErrorIsWrapped() async {
        StubURLProtocol.setHandler { _ in throw URLError(.notConnectedToInternet) }

        await XCTAssertThrowsErrorAsync(try await client.health()) { error in
            guard case APIError.transport(localized: _, retryable: true) = error else {
                return XCTFail("expected .transport, got \(error)")
            }
        }
    }

    func testInvalidJSONMapsToDecodingError() async {
        StubURLProtocol.setHandler { _ in .init(body: Data(#"{"status":42}"#.utf8)) }

        await XCTAssertThrowsErrorAsync(try await client.health()) { error in
            guard case APIError.decoding = error else {
                return XCTFail("expected .decoding, got \(error)")
            }
        }
    }

    func testCreateClientPostsContractBodyAndReturnsToken() async throws {
        StubURLProtocol.setHandler { request in
            .init(status: 201, body: Data("""
            {"client":{"id":12,"name":"opencode-mac","kind":"opencode",
              "scopes":["ingest","mcp:read","mcp:write"],"source":"opencode",
              "created_at":"2026-11-04T19:30:10Z"},
             "token":"sb_opencode_secret"}
            """.utf8))
        }

        let request = CreateClientRequest(
            name: "opencode-mac",
            kind: .opencode,
            scopes: [.ingest, .mcpRead, .mcpWrite]
        )
        let response = try await client.createClient(request)

        XCTAssertEqual(response.token, "sb_opencode_secret")
        XCTAssertEqual(response.client.id, 12)
        XCTAssertEqual(response.client.scopes, ["ingest", "mcp:read", "mcp:write"])
        XCTAssertEqual(response.client.source, "opencode")

        let sent = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(sent.httpMethod, "POST")
        XCTAssertEqual(sent.url?.path, "/api/v1/clients")
        XCTAssertEqual(sent.value(forHTTPHeaderField: "Authorization"), "Bearer sb_ui_test")
        XCTAssertEqual(sent.value(forHTTPHeaderField: "Content-Type"), "application/json")

        let body = try XCTUnwrap(sent.bodyData)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(object["name"] as? String, "opencode-mac")
        XCTAssertEqual(object["kind"] as? String, "opencode")
        XCTAssertEqual(object["scopes"] as? [String], ["ingest", "mcp:read", "mcp:write"])
        XCTAssertNil(object["source"])
    }

    func testListClientsDecodesRevokedClients() async throws {
        let body = Data("""
        {"items":[
          {"id":1,"name":"jarvis","kind":"jarvis","scopes":["ingest"],"source":"jarvis",
           "created_at":"2026-11-01T10:00:00Z","last_seen_at":null,"revoked_at":null},
          {"id":2,"name":"old","kind":"ui","scopes":["ui"],"source":null,
           "created_at":"2026-11-01T10:00:00Z","revoked_at":"2026-11-02T10:00:00Z"}
        ]}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(body: body) }

        let list = try await client.listClients()

        XCTAssertEqual(list.items.count, 2)
        XCTAssertNil(list.items[0].revokedAt)
        XCTAssertNotNil(list.items[1].revokedAt)
        XCTAssertNil(list.items[1].source)
    }

    func testRevokeClientSendsDeleteAndAccepts204() async throws {
        StubURLProtocol.setHandler { _ in .init(status: 204, body: Data()) }

        try await client.revokeClient(id: 12)

        let sent = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(sent.httpMethod, "DELETE")
        XCTAssertEqual(sent.url?.path, "/api/v1/clients/12")
    }

    func testCreateClientConflictIsReported() async {
        let body = Data(#"{"error":{"code":"conflict","message":"name already exists"}}"#.utf8)
        StubURLProtocol.setHandler { _ in .init(status: 409, body: body) }

        await XCTAssertThrowsErrorAsync(
            try await client.createClient(.init(name: "jarvis", kind: .jarvis, scopes: [.ingest]))
        ) { error in
            guard case APIError.server(let status, let code, _, _, _) = error else {
                return XCTFail("expected .server, got \(error)")
            }
            XCTAssertEqual(status, 409)
            XCTAssertEqual(code, "conflict")
        }
    }

    func testJobsSendsStatusAndLimitAndDecodesTheQueue() async throws {
        let body = Data("""
        {"items":[
          {"id":9,"client_id":2,"kind":"ingest","status":"failed","attempts":3,
           "external_id":"ses_9","record_id":null,"next_retry_at":null,
           "error":"upstream timeout","created_at":"2026-11-05T10:00:00Z",
           "updated_at":"2026-11-05T10:05:00Z"}
        ],"next_cursor":"abc"}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(body: body) }

        let list = try await client.jobs(status: .failed, limit: 20, cursor: "abc")

        XCTAssertEqual(list.items.count, 1)
        XCTAssertEqual(list.items[0].clientID, 2)
        XCTAssertEqual(list.items[0].status, .failed)
        XCTAssertEqual(list.items[0].error, "upstream timeout")
        XCTAssertEqual(list.nextCursor, "abc")

        let sent = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(sent.url?.path, "/api/v1/jobs")
        let components = try XCTUnwrap(
            URLComponents(url: try XCTUnwrap(sent.url), resolvingAgainstBaseURL: false)
        )
        XCTAssertEqual(components.queryItems?.first { $0.name == "status" }?.value, "failed")
        XCTAssertEqual(components.queryItems?.first { $0.name == "limit" }?.value, "20")
        XCTAssertEqual(components.queryItems?.first { $0.name == "cursor" }?.value, "abc")
    }

    func testGraphSendsFiltersAndDecodesNodesAndEdges() async throws {
        let body = Data("""
        {"nodes":[
          {"id":1,"title":"SQLite migration ordering","project_key":"github.com/example/meme",
           "source":"opencode","kind":"session","updated_at":"2026-11-05T10:00:00Z"}
        ],"edges":[
          {"from_id":1,"to_id":2,"weight":0.75,"reason":"keywords"}
        ],"truncated":true}
        """.utf8)
        StubURLProtocol.setHandler { _ in .init(body: body) }

        let graph = try await client.graph(
            projectKey: "github.com/example/meme",
            limit: 400,
            minWeight: 0.2
        )

        XCTAssertEqual(graph.nodes.count, 1)
        XCTAssertEqual(graph.nodes[0].projectKey, "github.com/example/meme")
        XCTAssertEqual(graph.nodes[0].source, "opencode")
        XCTAssertEqual(graph.edges.count, 1)
        XCTAssertEqual(graph.edges[0].fromID, 1)
        XCTAssertEqual(graph.edges[0].toID, 2)
        XCTAssertEqual(graph.edges[0].weight, 0.75)
        XCTAssertEqual(graph.edges[0].reason, .keywords)
        XCTAssertTrue(graph.truncated)

        let sent = try XCTUnwrap(StubURLProtocol.requests.first)
        XCTAssertEqual(sent.url?.path, "/api/v1/graph")
        let components = try XCTUnwrap(
            URLComponents(url: try XCTUnwrap(sent.url), resolvingAgainstBaseURL: false)
        )
        XCTAssertEqual(
            components.queryItems?.first { $0.name == "project_key" }?.value,
            "github.com/example/meme"
        )
        XCTAssertEqual(components.queryItems?.first { $0.name == "limit" }?.value, "400")
        XCTAssertEqual(components.queryItems?.first { $0.name == "min_weight" }?.value, "0.2")
    }

    private static let healthJSON = """
    {
      "status": "ok",
      "version": "test",
      "schema_version": 3,
      "time": "2026-11-04T19:30:10Z",
      "database": { "status": "ok", "fts5": true }
    }
    """
}

/// Small async-friendly replacement for `XCTAssertThrowsError`.
func XCTAssertThrowsErrorAsync<T>(
    _ expression: @autoclosure () async throws -> T,
    file: StaticString = #filePath,
    line: UInt = #line,
    _ handler: (Error) -> Void = { _ in }
) async {
    do {
        _ = try await expression()
        XCTFail("expected an error to be thrown", file: file, line: line)
    } catch {
        handler(error)
    }
}
