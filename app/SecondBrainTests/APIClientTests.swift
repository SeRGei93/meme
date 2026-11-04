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
            guard case APIError.server(let status, let code, let message, let requestID) = error else {
                return XCTFail("expected .server, got \(error)")
            }
            XCTAssertEqual(status, 422)
            XCTAssertEqual(code, "validation_failed")
            XCTAssertEqual(message, "limit must be positive")
            XCTAssertEqual(requestID, "req_2")
        }
    }

    func testRateLimitedIsRetryableAndUsesRetryAfterHeader() async {
        let body = Data(#"{"error":{"code":"rate_limited","message":"slow down"}}"#.utf8)
        StubURLProtocol.setHandler { _ in
            .init(status: 429, headers: ["Content-Type": "application/json", "Retry-After": "7"], body: body)
        }

        await XCTAssertThrowsErrorAsync(try await client.search(query: "x")) { error in
            guard case APIError.rateLimited(let retryAfter) = error else {
                return XCTFail("expected .rateLimited, got \(error)")
            }
            XCTAssertEqual(retryAfter, 7)
            XCTAssertTrue((error as? APIError)?.isRetryable == true)
        }
    }

    func testServerErrorMarkedRetryable() async {
        StubURLProtocol.setHandler { _ in .init(status: 503, body: Data()) }

        await XCTAssertThrowsErrorAsync(try await client.health()) { error in
            guard case APIError.server(let status, _, _, _) = error else {
                return XCTFail("expected .server, got \(error)")
            }
            XCTAssertEqual(status, 503)
            XCTAssertTrue((error as? APIError)?.isRetryable == true)
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
