import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class SearchModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testSearchSendsQueryFiltersAndSelectsTheFirstResult() async throws {
        let first = SearchFixtures.card(
            id: 1,
            title: "SQLite migration ordering",
            summary: "Migrations were sorted lexicographically.",
            questions: ["Why did 004 run first?"],
            keywords: ["sqlite", "migration"],
            atoms: ["Sort by numeric prefix."],
            score: 4.82
        )
        let second = SearchFixtures.card(id: 2, title: "Second result")
        let detail = try SearchFixtures.recordData(
            card: first,
            rawText: "first line\nmigration ordering\nlast line",
            revision: 3
        )
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/search":
                XCTAssertEqual(request.httpMethod, "GET")
                let items = try searchQueryItems(in: request)
                XCTAssertEqual(items["q"], "sqlite migration")
                XCTAssertEqual(items["limit"], "20")
                XCTAssertEqual(items["project_key"], "github.com/example/repo")
                XCTAssertEqual(items["source"], "opencode")
                XCTAssertNotNil(APIDate.parse(try XCTUnwrap(items["since"])))
                XCTAssertEqual(
                    request.value(forHTTPHeaderField: "Authorization"),
                    "Bearer sb_ui_test"
                )
                return .init(
                    body: try SearchFixtures.searchData(
                        items: [first, second],
                        query: "sqlite migration"
                    )
                )
            case "/api/v1/records/1":
                return .init(body: detail)
            default:
                return .init(status: 404, body: Data())
            }
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.projectKey = "github.com/example/repo"
        model.source = .opencode
        model.dateRange = .week
        model.query = "  sqlite migration  "

        await model.searchNow()

        XCTAssertEqual(model.results.map(\.id), [1, 2])
        XCTAssertEqual(model.total, 2)
        XCTAssertFalse(model.isLoading)
        XCTAssertNil(model.error)
        XCTAssertEqual(model.selectedID, 1)

        try await waitUntil { model.detail != nil }
        XCTAssertEqual(model.detail?.rawText, "first line\nmigration ordering\nlast line")
        XCTAssertEqual(model.detail?.revision, 3)
        XCTAssertEqual(model.detail?.card.title, "SQLite migration ordering")
        XCTAssertEqual(model.selectedProject, nil)
    }

    func testEmptyQueryClearsResultsWithoutARequest() async throws {
        StubURLProtocol.setHandler { _ in
            try .init(body: SearchFixtures.searchData(items: [], query: ""))
        }
        let model = SearchModel(client: client(), debounce: .zero)
        defer { model.cancelPendingRequests() }
        model.query = "sqlite"

        model.query = "   "
        await model.searchNow()

        XCTAssertTrue(model.results.isEmpty)
        XCTAssertTrue(model.isIdle)
        XCTAssertNil(model.error)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testPunctuationOnlyAndOverlongQueriesDoNotSearch() async throws {
        StubURLProtocol.setHandler { _ in
            try .init(body: SearchFixtures.searchData(items: [], query: "x"))
        }
        let model = SearchModel(client: client(), debounce: .zero)
        defer { model.cancelPendingRequests() }

        model.query = " - !!! "
        await model.searchNow()
        XCTAssertFalse(model.isQueryValid)
        XCTAssertTrue(model.results.isEmpty)

        model.query = String(repeating: "a", count: 501)
        await model.searchNow()
        XCTAssertFalse(model.isQueryValid)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testLateFirstResponseCannotOverwriteTheSecondQuery() async throws {
        let firstCard = SearchFixtures.card(id: 10, title: "First")
        let secondCard = SearchFixtures.card(id: 20, title: "Second")
        let started = LockedFlag()
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/search" {
                let components = try XCTUnwrap(
                    URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
                )
                let q = components.queryItems?.first(where: { $0.name == "q" })?.value
                if q == "first" {
                    started.set()
                    Thread.sleep(forTimeInterval: 0.3)
                    return .init(
                        body: try SearchFixtures.searchData(items: [firstCard], query: "first")
                    )
                }
                return .init(
                    body: try SearchFixtures.searchData(items: [secondCard], query: "second")
                )
            }
            let id = Int64(request.url?.lastPathComponent ?? "") ?? 0
            let card = id == 10 ? firstCard : secondCard
            return .init(body: try SearchFixtures.recordData(card: card, rawText: "raw"))
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.query = "first"

        let firstSearch = Task { await model.searchNow() }
        while !started.value {
            try await Task.sleep(for: .milliseconds(5))
        }

        model.query = "second"
        await model.searchNow()
        await firstSearch.value

        XCTAssertEqual(model.results.map(\.id), [20])
        XCTAssertEqual(model.selectedID, 20)
        try await waitUntil { model.detail?.card.id == 20 }
    }

    func testClientSwapClearsStaleResultsAndSelection() async throws {
        let card = SearchFixtures.card(id: 5, title: "Old client")
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/search" {
                return .init(body: try SearchFixtures.searchData(items: [card], query: "old"))
            }
            if request.url?.path == "/api/v1/projects" {
                return .init(body: try SearchFixtures.projectsData([]))
            }
            return .init(body: try SearchFixtures.recordData(card: card, rawText: "raw"))
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.query = "old"
        await model.searchNow()
        XCTAssertEqual(model.results.count, 1)
        XCTAssertEqual(model.selectedID, 5)

        let replacement = APIClient(
            baseURL: URL(string: "https://other.example.test")!,
            token: "sb_ui_test",
            session: StubURLProtocol.session()
        )
        model.setClient(replacement)

        XCTAssertTrue(model.results.isEmpty)
        XCTAssertNil(model.selectedID)
        XCTAssertNil(model.detail)
    }

    func testDebounceKeepsOnlyTheLastQuery() async throws {
        let result = SearchFixtures.card(id: 7, title: "Debounced")
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/records/7" {
                return .init(body: try SearchFixtures.recordData(card: result, rawText: "raw"))
            }
            return .init(
                body: try SearchFixtures.searchData(items: [result], query: "last")
            )
        }
        let model = SearchModel(client: client(), debounce: .milliseconds(80))
        defer { model.cancelPendingRequests() }

        model.query = "l"
        model.query = "la"
        model.query = "last"

        try await waitUntil(timeout: 3) { !model.isLoading && model.results.count == 1 }

        let searchRequests = StubURLProtocol.requests.filter { $0.url?.path == "/api/v1/search" }
        XCTAssertEqual(searchRequests.count, 1)
        XCTAssertEqual(model.results.first?.id, 7)
        try await waitUntil { model.detail?.card.id == 7 }
    }

    func testKeyboardSelectionWrapsAndLoadsDetails() async throws {
        let cards = [
            SearchFixtures.card(id: 1, title: "One"),
            SearchFixtures.card(id: 2, title: "Two"),
            SearchFixtures.card(id: 3, title: "Three"),
        ]
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/search" {
                return .init(
                    body: try SearchFixtures.searchData(items: cards, query: "item")
                )
            }
            let id = Int64(
                request.url?.lastPathComponent ?? ""
            ) ?? 0
            let card = cards.first { ($0["id"] as? Int64) == id }
                ?? cards.first { ($0["id"] as? Int) == Int(id) }
                ?? cards[0]
            return .init(
                body: try SearchFixtures.recordData(card: card, rawText: "raw \(id)")
            )
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.query = "item"
        await model.searchNow()
        XCTAssertEqual(model.selectedID, 1)

        model.moveSelection(by: 1)
        XCTAssertEqual(model.selectedID, 2)
        try await waitUntil { model.detail?.card.id == 2 }

        model.moveSelection(by: -1)
        XCTAssertEqual(model.selectedID, 1)
        try await waitUntil { model.detail?.card.id == 1 }

        model.moveSelection(by: -1)
        XCTAssertEqual(model.selectedID, 3, "up from the first result wraps to the last")
        try await waitUntil { model.detail?.card.id == 3 }
    }

    func testSearchErrorIsVisibleAndRetryRecovers() async throws {
        let card = SearchFixtures.card(id: 4, title: "Recovered")
        let attempts = LockedCounter()
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/records/4" {
                return .init(body: try SearchFixtures.recordData(card: card, rawText: "raw"))
            }
            return attempts.next() == 1
                ? .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
                : .init(
                    body: try SearchFixtures.searchData(items: [card], query: "recover")
                )
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.query = "recover"

        await model.searchNow()

        XCTAssertTrue(model.results.isEmpty)
        XCTAssertNotNil(model.error)
        XCTAssertNil(model.selectedID)

        model.retrySearch()
        try await waitUntil { model.results.count == 1 }

        XCTAssertNil(model.error)
        XCTAssertEqual(model.results.first?.id, 4)
        try await waitUntil { model.detail?.card.id == 4 }
    }

    func testProjectsAreLoadedWhenTheClientIsAttached() async throws {
        StubURLProtocol.setHandler { request in
            guard request.url?.path == "/api/v1/projects" else {
                return .init(status: 404, body: Data())
            }
            return .init(
                body: try SearchFixtures.projectsData([
                    ("github.com/example/repo", "repo", "/tmp/repo", 4),
                    ("jarvis", "Jarvis", "", 2),
                ])
            )
        }
        let model = SearchModel(debounce: .seconds(60))
        defer { model.cancelPendingRequests() }

        model.setClient(client())
        try await waitUntil { model.projects.count == 2 }

        XCTAssertEqual(model.projects.first?.key, "github.com/example/repo")
        XCTAssertEqual(model.selectedProject, nil)
    }

    func testFiltersAndClearingThemScheduleSearches() async throws {
        let card = SearchFixtures.card(id: 9, title: "Filtered", source: "jarvis")
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/records/9" {
                return .init(body: try SearchFixtures.recordData(card: card, rawText: "raw"))
            }
            return .init(body: try SearchFixtures.searchData(items: [card], query: "query"))
        }
        let model = SearchModel(client: client(), debounce: .seconds(60))
        defer { model.cancelPendingRequests() }
        model.query = "query"
        await model.searchNow()

        model.source = .jarvis
        model.projectKey = "jarvis"
        model.dateRange = .month
        XCTAssertTrue(model.hasActiveFilters)

        await model.searchNow()

        let last = try XCTUnwrap(
            StubURLProtocol.requests.last { $0.url?.path == "/api/v1/search" }
        )
        let items = try searchQueryItems(in: last)
        XCTAssertEqual(items["source"], "jarvis")
        XCTAssertEqual(items["project_key"], "jarvis")
        XCTAssertNotNil(items["since"])

        try await waitUntil { model.detail?.card.id == 9 }

        model.resetFilters()
        XCTAssertFalse(model.hasActiveFilters)
    }

    func testDateRangesComputeSince() throws {
        let now = Date(timeIntervalSince1970: 1_793_820_000)
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0)!

        XCTAssertNil(SearchDateRange.any.since(now: now, calendar: calendar))
        XCTAssertEqual(
            SearchDateRange.week.since(now: now, calendar: calendar),
            calendar.date(byAdding: .day, value: -7, to: now)
        )
        XCTAssertEqual(
            SearchDateRange.today.since(now: now, calendar: calendar),
            calendar.startOfDay(for: now)
        )
    }

    private func client() -> APIClient {
        APIClient(
            baseURL: baseURL,
            token: "sb_ui_test",
            session: StubURLProtocol.session()
        )
    }

    private func waitUntil(
        timeout: TimeInterval = 2,
        _ condition: @escaping @MainActor () -> Bool
    ) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTFail("condition was not met within \(timeout) s")
    }
}

private final class LockedCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var value = 0

    func next() -> Int {
        lock.lock()
        defer { lock.unlock() }
        value += 1
        return value
    }
}


private func searchQueryItems(in request: URLRequest) throws -> [String: String] {
    let components = try XCTUnwrap(
        URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
    )
    return Dictionary(
        uniqueKeysWithValues: (components.queryItems ?? []).compactMap { item in
            item.value.map { (item.name, $0) }
        }
    )
}


private final class LockedFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var stored = false

    var value: Bool {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }

    func set() {
        lock.lock()
        stored = true
        lock.unlock()
    }
}
