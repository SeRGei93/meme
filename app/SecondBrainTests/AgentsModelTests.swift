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

    func testLoadAggregatesClientsJobsAndCountsPerSource() async throws {
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(
                    sources: ["opencode": 5, "jarvis": 2, "claude-prod": 3]
                ))
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
                        StatusFixtures.job(
                            id: 4,
                            clientID: 5,
                            kind: "memory",
                            externalID: "claude_note",
                            error: nil,
                            createdAt: "2026-11-05T11:00:00Z"
                        ),
                    ])
                )
            case "/api/v1/clients":
                return .init(
                    body: StatusFixtures.clientsData([
                        StatusFixtures.client(id: 1, name: "second-brain-app", kind: "ui"),
                        StatusFixtures.client(id: 2, name: "opencode-macbook", kind: "agent", source: "opencode"),
                        StatusFixtures.client(id: 4, name: "opencode-old", kind: "agent", source: "opencode", revokedAt: "2026-11-01T00:00:00Z"),
                        StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                        StatusFixtures.client(id: 5, name: "claude", kind: "agent", source: "claude-prod"),
                    ])
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = AgentsModel(client: client())
        model.localOpenCodeClient = StoredClient(id: 2, name: "opencode-macbook")
        await model.load()

        XCTAssertEqual(model.summaries.count, 3)
        let opencode = try XCTUnwrap(summary("opencode", in: model))
        XCTAssertEqual(opencode.recordCount, 5)
        XCTAssertEqual(opencode.lastExternalID, "ses_old")
        XCTAssertEqual(opencode.activeClientNames, ["opencode-macbook"])
        XCTAssertEqual(opencode.revokedClientCount, 1)
        XCTAssertTrue(opencode.isLocalOpenCode)

        let jarvis = try XCTUnwrap(summary("jarvis", in: model))
        XCTAssertEqual(jarvis.recordCount, 2)
        XCTAssertEqual(jarvis.lastExternalID, "mem_1")
        XCTAssertEqual(jarvis.activeClientNames, ["jarvis"])
        XCTAssertFalse(jarvis.isLocalOpenCode)

        let claude = try XCTUnwrap(summary("claude-prod", in: model))
        XCTAssertEqual(claude.recordCount, 3)
        XCTAssertEqual(claude.lastExternalID, "claude_note")
        XCTAssertEqual(claude.activeClientNames, ["claude"])

        XCTAssertEqual(model.queue?.queued, 1)
        XCTAssertNil(model.error)
        XCTAssertNil(model.jobsError)
        XCTAssertNil(model.clientsError)
    }

    func testJobsPaginationFindsASecondSourceBeyondTheFirstPage() async throws {
        StubURLProtocol.setHandler { request in
            let components = try XCTUnwrap(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
            )
            switch components.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(sources: ["opencode": 1, "jarvis": 1]))
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
                        StatusFixtures.client(id: 2, name: "opencode-macbook", kind: "agent", source: "opencode"),
                        StatusFixtures.client(id: 3, name: "jarvis", kind: "agent", source: "jarvis"),
                    ])
                )
            }
        }

        let model = AgentsModel(client: client())
        await model.load()

        XCTAssertEqual(summary("opencode", in: model)?.lastExternalID, "ses_new")
        XCTAssertEqual(summary("jarvis", in: model)?.lastExternalID, "mem_deep")
    }

    func testClientsFailureStillShowsRecordCounts() async throws {
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/stats":
                return .init(body: StatusFixtures.statsData(sources: ["opencode": 5, "jarvis": 2]))
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

        XCTAssertEqual(model.summaries.count, 2)
        XCTAssertEqual(summary("opencode", in: model)?.recordCount, 5)
        XCTAssertNil(summary("opencode", in: model)?.lastActivityAt)
        XCTAssertTrue(summary("opencode", in: model)?.activeClientNames.isEmpty == true)
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

        XCTAssertTrue(model.summaries.isEmpty)
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

        XCTAssertFalse(model.summaries.isEmpty)
        XCTAssertNil(summary("opencode", in: model)?.lastActivityAt)
        XCTAssertNil(summary("opencode", in: model)?.lastExternalID)
    }

    private func summary(_ source: String, in model: AgentsModel) -> AgentSummary? {
        model.summaries.first { $0.source == source }
    }

    private func client() -> APIClient {
        APIClient(
            configuration: .init(baseURL: baseURL, token: "sb_ui_test"),
            session: StubURLProtocol.session()
        )
    }
}
