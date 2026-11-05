import XCTest
@testable import SecondBrain

final class RawTextSearchTests: XCTestCase {
    func testMatchingLinesIsCaseInsensitiveAndTrimsTheQuery() {
        let text = """
        SQLite migration ordering
        Migrations now sort by numeric prefix.
        Use the migration test.
        """

        let matches = RawTextSearch.matchingLines(in: text, query: "  NUMERIC PREFIX  ")

        XCTAssertEqual(matches, ["Migrations now sort by numeric prefix."])
    }

    func testEmptyQueryReturnsNoMatches() {
        XCTAssertEqual(RawTextSearch.matchingLines(in: "line", query: "   "), [])
    }

    func testMultipleMatchingLinesKeepTheirOrder() {
        let matches = RawTextSearch.matchingLines(
            in: "sqlite one\nother\nsqlite two",
            query: "sqlite"
        )

        XCTAssertEqual(matches, ["sqlite one", "sqlite two"])
    }
}
