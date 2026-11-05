import Foundation
import XCTest
@testable import SecondBrain

final class SpoolWatcherTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-watcher-\(UUID().uuidString)", isDirectory: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
        directory = nil
        try super.tearDownWithError()
    }

    func testBundleFilesFindsJSONAndIgnoresTmpHiddenAndFailed() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: directory.appendingPathComponent("one.json"))
        try Data("{}".utf8).write(to: directory.appendingPathComponent("two.json"))
        try Data("{}".utf8).write(to: directory.appendingPathComponent("one.json.tmp"))
        try Data("{}".utf8).write(to: directory.appendingPathComponent(".hidden.json"))
        let failed = directory.appendingPathComponent("failed", isDirectory: true)
        try FileManager.default.createDirectory(at: failed, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: failed.appendingPathComponent("bad.json"))

        let files = try SpoolWatcher.bundleFiles(in: directory)

        XCTAssertEqual(files.map(\.lastPathComponent), ["one.json", "two.json"])
    }

    func testStartCreatesAMissingDirectory() throws {
        let watcher = SpoolWatcher(directory: directory) {}
        defer { watcher.stop() }

        try watcher.start()

        XCTAssertTrue(FileManager.default.fileExists(atPath: directory.path))
    }

    func testStartOnARegularFileThrowsAndCanRetryAfterItBecomesADirectory() throws {
        try Data("not a directory".utf8).write(to: directory)
        let watcher = SpoolWatcher(directory: directory) {}
        defer { watcher.stop() }

        XCTAssertThrowsError(try watcher.start())

        try FileManager.default.removeItem(at: directory)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        XCTAssertNoThrow(try watcher.start())
    }

    func testStartPicksUpExistingFiles() throws {
        let expectation = expectation(description: "initial scan")
        let watcher = SpoolWatcher(directory: directory) {
            expectation.fulfill()
        }
        defer { watcher.stop() }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: directory.appendingPathComponent("existing.json"))

        try watcher.start()

        wait(for: [expectation], timeout: 2)
    }

    func testAtomicRenameIntoTheDirectoryIsObserved() async throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let counter = Counter()
        let watcher = SpoolWatcher(directory: directory) {
            counter.increment()
        }
        defer { watcher.stop() }
        try watcher.start()
        XCTAssertEqual(counter.value, 1, "the initial scan must report once")

        let temporary = directory.appendingPathComponent("incoming.json.tmp")
        try Data("{}".utf8).write(to: temporary)
        XCTAssertTrue(
            FileManager.default.fileExists(atPath: temporary.path)
        )
        try FileManager.default.moveItem(
            at: temporary,
            to: directory.appendingPathComponent("incoming.json")
        )

        for _ in 0..<300 {
            if counter.value >= 2 { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertGreaterThanOrEqual(counter.value, 2)
    }

    func testDirectoryDeletionAndRecreationRearmsTheWatcher() async throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let counter = Counter()
        let watcher = SpoolWatcher(directory: directory) {
            counter.increment()
        }
        defer { watcher.stop() }
        try watcher.start()
        let initial = counter.value

        try FileManager.default.removeItem(at: directory)
        // Let the automatic re-arm settle, then prove that the *new* source
        // sees a file written after the directory was recreated.
        try await Task.sleep(nanoseconds: 600_000_000)
        let afterRearm = counter.value
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: directory.appendingPathComponent("after-recreate.json"))

        for _ in 0..<300 {
            if counter.value > afterRearm { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertGreaterThan(counter.value, afterRearm)
        XCTAssertGreaterThan(counter.value, initial)
    }
}

private final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var stored = 0

    var value: Int {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }

    func increment() {
        lock.lock()
        stored += 1
        lock.unlock()
    }
}
