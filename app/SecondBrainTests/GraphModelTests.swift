import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class GraphModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testLoadFetchesTheSelectedProjectAndComputesLayout() async throws {
        StubURLProtocol.setHandler { request in
            let components = try XCTUnwrap(
                URLComponents(url: try XCTUnwrap(request.url), resolvingAgainstBaseURL: false)
            )
            switch components.path {
            case "/api/v1/graph":
                XCTAssertEqual(
                    components.queryItems?.first { $0.name == "project_key" }?.value,
                    "project-a"
                )
                XCTAssertEqual(
                    components.queryItems?.first { $0.name == "limit" }?.value,
                    "400"
                )
                return .init(
                    body: GraphFixtures.graphData(
                        nodes: [
                            GraphFixtures.node(id: 1, title: "First", projectKey: "project-a"),
                            GraphFixtures.node(
                                id: 2,
                                title: "Second",
                                projectKey: "project-a",
                                source: "jarvis",
                                kind: "memory"
                            ),
                            GraphFixtures.node(id: 3, title: "Third", projectKey: "project-a"),
                        ],
                        edges: [
                            GraphFixtures.edge(from: 1, to: 2, weight: 0.8),
                            GraphFixtures.edge(from: 2, to: 3, weight: 0.4),
                        ]
                    )
                )
            case "/api/v1/projects":
                return .init(
                    body: try SearchFixtures.projectsData([
                        ("project-a", "Project A", "/tmp/a", 3)
                    ])
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = GraphModel(client: client())
        model.projectKey = "project-a"
        await model.load()

        XCTAssertEqual(model.graph?.nodes.count, 3)
        XCTAssertEqual(model.graph?.edges.count, 2)
        XCTAssertEqual(model.visualNodes.count, 3)
        XCTAssertEqual(model.visualEdges.count, 2)
        XCTAssertEqual(model.projects.map(\.key), ["project-a"])
        XCTAssertEqual(model.displayName(for: "project-a"), "Project A")
        XCTAssertNil(model.error)
        XCTAssertNil(model.projectsError)
        XCTAssertFalse(model.isLoading)
        XCTAssertFalse(model.isLayouting)
        XCTAssertFalse(model.isTruncated)
    }

    func testSourceFilterRemovesNodesAndDanglingEdges() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/projects" {
                return .init(body: try SearchFixtures.projectsData([]))
            }
            return .init(
                body: GraphFixtures.graphData(
                    nodes: [
                        GraphFixtures.node(id: 1, title: "OpenCode note"),
                        GraphFixtures.node(
                            id: 2,
                            title: "Jarvis note",
                            projectKey: "jarvis",
                            source: "jarvis",
                            kind: "memory"
                        ),
                    ],
                    edges: [GraphFixtures.edge(from: 1, to: 2)]
                )
            )
        }

        let model = GraphModel(client: client())
        await model.load()
        XCTAssertEqual(model.visualNodes.count, 2)
        XCTAssertEqual(model.visualEdges.count, 1)

        model.source = .jarvis

        XCTAssertEqual(model.nodes.map(\.id), [2])
        XCTAssertEqual(model.visualNodes.map(\.id), [2])
        XCTAssertTrue(model.visualEdges.isEmpty)
        XCTAssertEqual(model.count(for: .opencode), 1)
        XCTAssertEqual(model.count(for: .jarvis), 1)
    }

    func testGraphFailureShowsTheErrorPaneAndRetryRecovers() async throws {
        let failing = LockedFlag(initiallyTrue: true)
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/projects" {
                return .init(body: try SearchFixtures.projectsData([]))
            }
            if failing.value {
                return .init(
                    status: 500,
                    body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                )
            }
            return .init(
                body: GraphFixtures.graphData(
                    nodes: [GraphFixtures.node(id: 1, title: "Recovered")]
                )
            )
        }

        let model = GraphModel(client: client())
        await model.load()

        XCTAssertNil(model.graph)
        XCTAssertTrue(model.visualNodes.isEmpty)
        XCTAssertNotNil(model.error)
        XCTAssertFalse(model.isLoading)

        failing.set(false)
        await model.load()

        XCTAssertNil(model.error)
        XCTAssertEqual(model.visualNodes.map(\.id), [1])
    }

    func testProjectsFailureKeepsTheGraphAndFallsBackToNodeProjectKeys() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/projects" {
                return .init(
                    status: 403,
                    body: Data(#"{"error":{"code":"forbidden","message":"admin required"}}"#.utf8)
                )
            }
            return .init(
                body: GraphFixtures.graphData(
                    nodes: [
                        GraphFixtures.node(id: 1, title: "First", projectKey: "github.com/example/meme")
                    ]
                )
            )
        }

        let model = GraphModel(client: client())
        await model.load()

        XCTAssertNotNil(model.graph)
        XCTAssertNotNil(model.projectsError)
        XCTAssertEqual(model.projectOptions.map(\.key), ["github.com/example/meme"])
        XCTAssertEqual(model.displayName(for: "github.com/example/meme"), "github.com/example/meme")
    }

    func testTruncatedGraphIsSurfaced() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/projects" {
                return .init(body: try SearchFixtures.projectsData([]))
            }
            return .init(
                body: GraphFixtures.graphData(
                    nodes: [GraphFixtures.node(id: 1, title: "Only node")],
                    truncated: true
                )
            )
        }

        let model = GraphModel(client: client())
        await model.load()

        XCTAssertTrue(model.isTruncated)
    }

    func testSelectingANodeLoadsItsRecordAndDismissClearsTheSheet() async throws {
        let card = SearchFixtures.card(
            id: 7,
            title: "Graph record",
            summary: "Loaded from the graph.",
            projectKey: "project-a"
        )
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/graph":
                return .init(
                    body: GraphFixtures.graphData(
                        nodes: [GraphFixtures.node(id: 7, title: "Graph record", projectKey: "project-a")]
                    )
                )
            case "/api/v1/projects":
                return .init(
                    body: try SearchFixtures.projectsData([
                        ("project-a", "Project A", "/tmp/a", 1)
                    ])
                )
            case "/api/v1/records/7":
                return .init(
                    body: try SearchFixtures.recordData(
                        card: card,
                        rawText: "raw graph text",
                        revision: 2
                    )
                )
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = GraphModel(client: client())
        await model.load()
        model.selectNode(7)

        XCTAssertEqual(model.selectedNodeID, 7)
        XCTAssertTrue(model.isDetailPresented)
        XCTAssertTrue(model.detailLoading)

        try await waitUntil { !model.detailLoading }

        XCTAssertEqual(model.detail?.card.title, "Graph record")
        XCTAssertEqual(model.detail?.revision, 2)
        XCTAssertEqual(model.project(for: "project-a")?.name, "Project A")
        XCTAssertNil(model.detailError)

        // A reload must not leave a card or selection pointing at the old graph.
        await model.load()

        XCTAssertFalse(model.isDetailPresented)
        XCTAssertNil(model.selectedNodeID)
        XCTAssertNil(model.detail)
        model.dismissDetail()

        XCTAssertFalse(model.isDetailPresented)
        XCTAssertNil(model.selectedNodeID)
        XCTAssertNil(model.detail)
    }

    func testDetailFailureExposesAnErrorAndRetryRecovers() async throws {
        let card = SearchFixtures.card(id: 8, title: "Retry me")
        let failing = LockedFlag(initiallyTrue: true)
        StubURLProtocol.setHandler { request in
            switch request.url?.path {
            case "/api/v1/graph":
                return .init(
                    body: GraphFixtures.graphData(
                        nodes: [GraphFixtures.node(id: 8, title: "Retry me")]
                    )
                )
            case "/api/v1/projects":
                return .init(body: try SearchFixtures.projectsData([]))
            case "/api/v1/records/8":
                if failing.value {
                    return .init(
                        status: 500,
                        body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
                    )
                }
                return .init(body: try SearchFixtures.recordData(card: card, rawText: "ok"))
            default:
                return .init(status: 404, body: Data())
            }
        }

        let model = GraphModel(client: client())
        await model.load()
        model.selectNode(8)
        try await waitUntil { !model.detailLoading }

        XCTAssertNil(model.detail)
        XCTAssertNotNil(model.detailError)

        failing.set(false)
        model.retryDetail()
        try await waitUntil { model.detail != nil }

        XCTAssertNil(model.detailError)
        XCTAssertEqual(model.detail?.card.id, 8)
    }

    func testClientSwapResetsGraphState() async throws {
        StubURLProtocol.setHandler { request in
            if request.url?.path == "/api/v1/projects" {
                return .init(body: try SearchFixtures.projectsData([]))
            }
            return .init(
                body: GraphFixtures.graphData(
                    nodes: [GraphFixtures.node(id: 1, title: "First")]
                )
            )
        }

        let model = GraphModel(client: client())
        await model.load()
        XCTAssertNotNil(model.graph)

        model.setClient(
            APIClient(
                baseURL: URL(string: "https://other.example.test")!,
                token: "sb_ui_other",
                session: StubURLProtocol.session()
            )
        )

        XCTAssertNil(model.graph)
        XCTAssertTrue(model.visualNodes.isEmpty)
        XCTAssertNil(model.error)
    }

    private func client() -> APIClient {
        APIClient(baseURL: baseURL, token: "sb_ui_test", session: StubURLProtocol.session())
    }

}
