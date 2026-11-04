import XCTest
@testable import SecondBrain

final class KeychainStoreTests: XCTestCase {
    private var store: KeychainStore!
    private let service = "com.serg.secondbrain.tests.\(UUID().uuidString)"

    override func setUp() {
        super.setUp()
        store = KeychainStore(service: service)
    }

    override func tearDown() {
        try? store.deleteAll()
        store = nil
        super.tearDown()
    }

    func testMissingValueReturnsNil() throws {
        XCTAssertNil(try store.value(for: "admin"))
    }

    func testValueRoundTrip() throws {
        try store.setValue("sb_admin_secret", for: "admin")
        XCTAssertEqual(try store.value(for: "admin"), "sb_admin_secret")
    }

    func testWritingAgainOverwritesValue() throws {
        try store.setValue("first", for: "ui")
        try store.setValue("second", for: "ui")

        XCTAssertEqual(try store.value(for: "ui"), "second")
    }

    func testAccountsAreIndependent() throws {
        try store.setValue("admin-token", for: "admin")
        try store.setValue("mcp-token", for: "mcp")

        XCTAssertEqual(try store.value(for: "admin"), "admin-token")
        XCTAssertEqual(try store.value(for: "mcp"), "mcp-token")
    }

    func testDeleteRemovesValue() throws {
        try store.setValue("temporary", for: "ui")
        try store.deleteValue(for: "ui")

        XCTAssertNil(try store.value(for: "ui"))
    }

    func testDeletingMissingValueIsNotAnError() throws {
        XCTAssertNoThrow(try store.deleteValue(for: "never-stored"))
    }
}
