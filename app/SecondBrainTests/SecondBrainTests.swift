import AppKit
import XCTest
@testable import SecondBrain

final class SecondBrainTests: XCTestCase {
    func testAppStateStartsReady() {
        let state = AppState()
        XCTAssertEqual(state.statusText, "Готов к работе")
        XCTAssertNil(state.lastSync)
    }

    func testAppStateKeepsStatusUpdates() {
        let state = AppState()
        state.statusText = "Загрузка"
        XCTAssertEqual(state.statusText, "Загрузка")
    }

    func testClosingLastWindowDoesNotTerminateTheApp() {
        let delegate = AppDelegate()
        XCTAssertFalse(delegate.applicationShouldTerminateAfterLastWindowClosed(NSApplication.shared))
    }
}
