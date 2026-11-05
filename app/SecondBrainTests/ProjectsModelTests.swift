import Foundation
import XCTest
@testable import SecondBrain

@MainActor
final class ProjectsModelTests: XCTestCase {
    private let baseURL = URL(string: "https://brain.example.test")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testLoadAndFilterAreCaseInsensitiveAcrossKeyNameAndPath() async throws {
        StubURLProtocol.setHandler { request in
            XCTAssertEqual(request.url?.path, "/api/v1/projects")
            return .init(
                body: try SearchFixtures.projectsData([
                    ("github.com/example/meme", "meme", "/Users/serg/meme", 5),
                    ("jarvis", "Jarvis", "", 2),
                ])
            )
        }

        let model = ProjectsModel(client: client())
        await model.load()

        XCTAssertEqual(model.projects.map(\.key), ["github.com/example/meme", "jarvis"])
        XCTAssertEqual(model.projects.first?.recordCount, 5)
        XCTAssertNil(model.error)

        model.filter = "MEME"
        XCTAssertEqual(model.filteredProjects.map(\.key), ["github.com/example/meme"])

        model.filter = "jarvis"
        XCTAssertEqual(model.filteredProjects.map(\.key), ["jarvis"])

        model.filter = "/Users/serg"
        XCTAssertEqual(model.filteredProjects.map(\.key), ["github.com/example/meme"])
    }

    func testErrorClearsProjectsAndRetryRecovers() async throws {
        let failing = LockedFlag()
        StubURLProtocol.setHandler { _ in
            if failing.value {
                return .init(
                    body: try SearchFixtures.projectsData([("jarvis", "Jarvis", "", 1)])
                )
            }
            return .init(
                status: 500,
                body: Data(#"{"error":{"code":"internal_error","message":"boom"}}"#.utf8)
            )
        }

        let model = ProjectsModel(client: client())
        await model.load()
        XCTAssertNotNil(model.error)
        XCTAssertTrue(model.projects.isEmpty)

        failing.set()
        await model.retry()
        XCTAssertNil(model.error)
        XCTAssertEqual(model.projects.map(\.key), ["jarvis"])
    }

    func testClientSwapResetsProjects() async throws {
        StubURLProtocol.setHandler { _ in
            .init(body: try SearchFixtures.projectsData([("jarvis", "Jarvis", "", 1)]))
        }
        let model = ProjectsModel(client: client())
        await model.load()
        XCTAssertEqual(model.projects.count, 1)

        model.setClient(nil)

        XCTAssertTrue(model.projects.isEmpty)
        XCTAssertNil(model.error)
    }

    private func client() -> APIClient {
        APIClient(baseURL: baseURL, token: "sb_ui_test", session: StubURLProtocol.session())
    }
}
