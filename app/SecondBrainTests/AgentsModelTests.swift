import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class AgentsModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testLoadResolvesLatestJobAndActiveClientsPerKind() async throws {
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(opencode: 5, jarvis: 2))
            case "/api/v1/jobs":
                return .init(
                    body: StatusFixtures.jobsData([
                        StatusFixtures.job(
                            id: 1,
                            clientID: 2,
                            externalID: "ses_new",
                            error: nil,
                            createdAt: "2026-11-05T10:00:00Z"
                        ),
                        StatusFixtures.job(
                            id: 2,
                            clientID: 4,
                            externalID: "ses_old",
                            error: nil,
                            createdAt: "2026-11-05T12:00:00Z"
                        ),
                        StatusFixtures.job(
                            id: 3,
                            clientID: 3,
                            kind: "memory",
                            externalID: "mem_1",
                            error: nil,
                            createdAt: "2026-11-05T09:00:00Z"
                        ),
                    ])
                )
            case "/api/v1/clients":
                return .init(
                    body: StatusFixtures.clientsData([
                        StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
                        StatusFixtures.client(id: 2, name: "opencode-macbook", kind: "opencode"),
                        StatusFixtures.client(id: 4, name: "opencode-old", kind: "opencode", revokedAt: "2026-11-01T00:00:00Z"),
                        StatusFixtures.client(id: 3, name: "jarvis", kind: "jarvis"),
                    ])
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertEqual(model.opencode?.recordCount, 5)
        XCTAssertEqual(model.opencode?.lastExternalID, "ses_old")
        XCTAssertEqual(model.opencode?.activeClientNames, ["opencode-macbook"])
        XCTAssertEqual(model.opencode?.revokedClientCount, 1)
        XCTAssertEqual(model.jarvis?.recordCount, 2)
        XCTAssertEqual(model.jarvis?.lastExternalID, "mem_1")
        XCTAssertEqual(model.jarvis?.activeClientNames, ["jarvis"])
        XCTAssertEqual(model.queue?.queued, 1)
        XCTAssertNil(model.error)
        XCTAssertNil(model.jobsError)
        XCTAssertNil(model.clientsError)
    }

    func testJobsPaginationFindsASecondKindBeyondTheFirstPage() async throws {
        StubURLProtocol.setHandler { request in
            let components = try XCTUnwrap(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
            )
            switch components.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(opencode: 1, jarvis: 1))
            case "/api/v1/jobs":
                let cursor = components.queryItems?.first { $0.name == "cursor" }?.value
                if cursor == "page2" {
                    return .init(
                        body: StatusFixtures.jobsData(
                            [
                                StatusFixtures.job(
                                    id: 1,
                                    clientID: 3,
                                    kind: "memory",
                                    externalID: "mem_deep",
                                    error: nil,
                                    createdAt: "2026-11-01T09:00:00Z"
                                )
                            ]
                        )
                    )
                }
                return .init(
                    body: StatusFixtures.jobsData(
                        [
                            StatusFixtures.job(
                                id: 9,
                                clientID: 2,
                                externalID: "ses_new",
                                error: nil,
                                createdAt: "2026-11-05T10:00:00Z"
                            )
                        ],
                        nextCursor: "page2"
                    )
                )
            default:
                return .init(
                    body: StatusFixtures.clientsData([
                        StatusFixtures.client(id: 2, name: "opencode-macbook", kind: "opencode"),
                        StatusFixtures.client(id: 3, name: "jarvis", kind: "jarvis"),
                    ])
                )
            }
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertEqual(model.opencode?.lastExternalID, "ses_new")
        XCTAssertEqual(model.jarvis?.lastExternalID, "mem_deep")
    }

    func testClientsFailureStillShowsRecordCounts() async throws {
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(opencode: 5, jarvis: 2))
            case "/api/v1/jobs":
                return .init(body: StatusFixtures.jobsData([]))
            default:
                return .init(
                    status: 403,
                    body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
                )
            }
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertEqual(model.opencode?.recordCount, 5)
        XCTAssertNil(model.opencode?.lastActivityAt)
        XCTAssertTrue(model.opencode?.activeClientNames.isEmpty == true)
        XCTAssertNotNil(model.clientsError)
        XCTAssertNil(model.error)
    }

    func testStatsFailureUsesTheFullErrorState() async throws {
        StubURLProtocol.setHandler { _ in
            .init(
                status: 500,
                body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
            )
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertNil(model.opencode)
        XCTAssertNil(model.jarvis)
        XCTAssertNotNil(model.error)
        XCTAssertFalse(model.isLoading)
    }

    func testNoJobsLeavesLastActivityEmpty() async throws {
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData())
            case "/api/v1/jobs":
                return .init(body: StatusFixtures.jobsData([]))
            default:
                return .init(body: StatusFixtures.clientsData([]))
            }
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertNotNil(model.opencode)
        XCTAssertNil(model.opencode?.lastActivityAt)
        XCTAssertNil(model.opencode?.lastExternalID)
    }

    private func client() -> APIClient {
        APIClient(baseURL: baseURL, token: "sb_ui_test", session: StubURLProtocol.session())
    }
}
