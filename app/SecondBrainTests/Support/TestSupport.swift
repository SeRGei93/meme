import Foundation
import XCTest

/// Thread-safe flag shared by async model tests.
final class LockedFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: Bool

    init(initiallyTrue: Bool = false) {
        stored = initiallyTrue
    }

    var value: Bool {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }

    func set() {
        set(true)
    }

    func set(_ newValue: Bool) {
        lock.lock()
        stored = newValue
        lock.unlock()
    }
}

extension XCTestCase {
    /// Polls a main-actor condition from async model tests without relying on
    /// timing-sensitive sleeps.
    @MainActor
    func waitUntil(
        timeout: TimeInterval = 5,
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
