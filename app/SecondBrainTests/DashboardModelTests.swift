import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class DashboardModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testLoadFetchesStatsAndFailedJobs() async throws {
        StubURLProtocol.setHandler { request in
            let components = try XCTUnwrap(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
            )
            switch components.path {
            case "/api/v1/stats":
                return .init(
                    body: StatusFixtures.statsData(
                        recordsTotal: 7,
                        projects: 3,
                        clients: 2,
                        queued: 1,
                        running: 1,
                        failed: 2
                    )
                )
            case "/api/v1/jobs":
                XCTAssertEqual(components.queryItems?.first { $0.name == "status" }?.value, "failed")
                XCTAssertEqual(components.queryItems?.first { $0.name == "limit" }?.value, "20")
                return .init(
                    body: StatusFixtures.jobsData([
                        StatusFixtures.job(id: 9, clientID: 2, error: "upstream timeout")
                    ])
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = DashboardModel(client: client())
        await model.load()

        XCTAssertEqual(model.stats?.records.total, 7)
        XCTAssertEqual(model.stats?.projects, 3)
        XCTAssertEqual(model.stats?.clients, 2)
        XCTAssertEqual(model.stats?.jobs.failed, 2)
        XCTAssertEqual(model.failedJobs.map(\.id), [9])
        XCTAssertEqual(model.failedJobs.first?.error, "upstream timeout")
        XCTAssertNil(model.error)
        XCTAssertNil(model.jobsError)
        XCTAssertNotNil(model.lastUpdated)
        XCTAssertFalse(model.isLoading)
    }

    func testStatsFailureShowsErrorButStillLoadsJobs() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/jobs" {
                return .init(
                    body: StatusFixtures.jobsData([
                        StatusFixtures.job(id: 3, clientID: 2)
                    ])
                )
            }
            return .init(
                status: 500,
                body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
            )
        }

        let model = DashboardModel(client: client())
        await model.load()

        XCTAssertNil(model.stats)
        XCTAssertNotNil(model.error)
        XCTAssertEqual(model.failedJobs.map(\.id), [3])
        XCTAssertNil(model.jobsError)
    }

    func testJobsFailureKeepsStatsAndExposesJobsError() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/stats" {
                return .init(body: StatusFixtures.statsData())
            }
            return .init(
                status: 403,
                body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
            )
        }

        let model = DashboardModel(client: client())
        await model.load()

        XCTAssertNotNil(model.stats)
        XCTAssertNil(model.error)
        XCTAssertNotNil(model.jobsError)
        XCTAssertTrue(model.failedJobs.isEmpty)
    }

    func testClientSwapResetsCachedDashboardState() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/stats" {
                return .init(body: StatusFixtures.statsData())
            }
            return .init(body: StatusFixtures.jobsData([]))
        }
        let model = DashboardModel(client: client())
        await model.load()
        XCTAssertNotNil(model.stats)

        model.setClient(
            APIClient(
                baseURL: URL(string: "https://other.example.test")!,
                token: "sb_ui_other",
                session: StubURLProtocol.session()
            )
        )

        XCTAssertNil(model.stats)
        XCTAssertTrue(model.failedJobs.isEmpty)
        XCTAssertNil(model.error)
        XCTAssertNil(model.lastUpdated)
    }

    private func client() -> APIClient {
        APIClient(baseURL: baseURL, token: "sb_ui_test", session: StubURLProtocol.session())
    }
}
