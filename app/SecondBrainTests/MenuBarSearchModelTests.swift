import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class MenuBarSearchModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testSearchSendsQueryAndLimitAndPublishesResults() async throws {
        let cards = [
            SearchFixtures.card(id: 1, title: "SQLite migration ordering"),
            SearchFixtures.card(id: 2, title: "Spool upload retries"),
        ]
        StubURLProtocol.setHandler { request in
            let components = try XCTUnwrap(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
            )
            XCTAssertEqual(components.path, "/api/v1/search")
            XCTAssertEqual(components.queryItems?.first { $0.name == "q" }?.value, "sqlite")
            XCTAssertEqual(components.queryItems?.first { $0.name == "limit" }?.value, "8")
            return .init(body: try SearchFixtures.searchData(items: cards, query: "sqlite"))
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "sqlite"
        try await waitUntil { !model.isSearching && model.results.count == 2 }

        XCTAssertEqual(model.results.map(\.id), [1, 2])
        XCTAssertNil(model.error)
        XCTAssertTrue(model.hasQuery)
    }

    func testBlankQueryClearsWithoutNetwork() async throws {
        StubURLProtocol.setHandler { _ in
            XCTFail("a blank query must not hit the server")
            return .init()
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "  \n "

        XCTAssertTrue(model.results.isEmpty)
        XCTAssertFalse(model.isSearching)
        XCTAssertNil(model.error)
        XCTAssertFalse(model.hasQuery)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testSearchFailureExposesErrorAndRetryRecovers() async throws {
        let failing = LockedFlag(initiallyTrue: true)
        StubURLProtocol.setHandler { _ in
            if failing.value {
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            }
            return .init(
                body: try SearchFixtures.searchData(
                    items: [SearchFixtures.card(id: 3, title: "Recovered")],
                    query: "sqlite"
                )
            )
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "sqlite"
        try await waitUntil { model.error != nil }

        XCTAssertTrue(model.results.isEmpty)
        XCTAssertFalse(model.isSearching)

        failing.set(false)
        model.retry()
        try await waitUntil { model.results.count == 1 }

        XCTAssertNil(model.error)
        XCTAssertEqual(model.results.first?.id, 3)
    }

    func testClearResetsResults() async throws {
        StubURLProtocol.setHandler { _ in
            .init(
                body: try SearchFixtures.searchData(
                    items: [SearchFixtures.card(id: 4, title: "Node")],
                    query: "node"
                )
            )
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "node"
        try await waitUntil { model.results.count == 1 }

        model.clear()

        XCTAssertEqual(model.query, "")
        XCTAssertTrue(model.results.isEmpty)
        XCTAssertFalse(model.isSearching)
    }

    func testClientSwapResetsResults() async throws {
        StubURLProtocol.setHandler { _ in
            .init(
                body: try SearchFixtures.searchData(
                    items: [SearchFixtures.card(id: 5, title: "Old result")],
                    query: "old"
                )
            )
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "old"
        try await waitUntil { model.results.count == 1 }

        model.setClient(nil)

        XCTAssertTrue(model.results.isEmpty)
        try await waitUntil { model.error != nil }
        XCTAssertFalse(model.isSearching)
    }

    func testPunctuationOnlyQueryDoesNotTouchNetwork() async throws {
        StubURLProtocol.setHandler { _ in
            XCTFail("an invalid query must not hit the server")
            return .init()
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "!!!"
        try await Task.sleep(for: .milliseconds(30))

        XCTAssertFalse(model.isQueryValid)
        XCTAssertTrue(model.results.isEmpty)
        XCTAssertFalse(model.isSearching)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testResetForDismissalClearsQueryAndResults() async throws {
        StubURLProtocol.setHandler { _ in
            .init(
                body: try SearchFixtures.searchData(
                    items: [SearchFixtures.card(id: 6, title: "Reset me")],
                    query: "reset"
                )
            )
        }

        let model = MenuBarSearchModel(client: client(), debounce: .zero)
        model.query = "reset"
        try await waitUntil { model.results.count == 1 }

        model.resetForDismissal()

        XCTAssertEqual(model.query, "")
        XCTAssertTrue(model.results.isEmpty)
        XCTAssertFalse(model.isSearching)
        XCTAssertNil(model.error)
    }

    private func client() -> APIClient {
        APIClient(baseURL: baseURL, token: "sb_ui_test", session: StubURLProtocol.session())
    }
}
