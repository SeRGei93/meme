import Foundation
import XCTest
@testable import SecondBrain

final class UploadQueueTests: XCTestCase {
    private var spool: URL!
    private let serverURL = URL(string: "https://brain.example.test")!
    private let token = "sb_opencode_test"

    override func setUpWithError() throws {
        try super.setUpWithError()
        spool = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-upload-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: spool, withIntermediateDirectories: true)
        StubURLProtocol.reset()
    }

    override func tearDownWithError() throws {
        StubURLProtocol.reset()
        try? FileManager.default.removeItem(at: spool)
        spool = nil
        try super.tearDownWithError()
    }

    func testUploadsTheNewestBundlePerSessionAndDeletesTheGroup() async throws {
        let oldID = UUID().uuidString
        let newID = UUID().uuidString
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(
                externalID: "ses_1", bundleID: oldID, title: "Old",
                updatedAt: Date(timeIntervalSince1970: 100)
            ),
            to: spool,
            name: "old.json"
        )
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(
                externalID: "ses_1", bundleID: newID, title: "New",
                updatedAt: Date(timeIntervalSince1970: 200)
            ),
            to: spool,
            name: "new.json"
        )
        StubURLProtocol.setHandler { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/api/v1/ingest")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Idempotency-Key"), newID)
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer sb_opencode_test")
            let body = try XCTUnwrap(request.bodyData)
            let object = try XCTUnwrap(
                JSONSerialization.jsonObject(with: body) as? [String: Any]
            )
            XCTAssertEqual(object["title"] as? String, "New")
            XCTAssertNil(object["source"])
            XCTAssertNil(object["schema_version"])
            return .init(status: 202, body: Self.ingestResponse())
        }
        let queue = makeQueue()

        await queue.trigger()

        XCTAssertEqual(StubURLProtocol.requests.count, 1)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: spool.path), [])
        let status = await queue.status
        XCTAssertEqual(status.pending, 0)
        XCTAssertEqual(status.sent, 1)
        XCTAssertNil(status.lastError)
    }

    func testServerErrorKeepsTheBundleAndRetryNowSucceeds() async throws {
        let bundleID = UUID().uuidString
        let file = try SpoolFixtures.write(
            SpoolFixtures.bundleData(bundleID: bundleID),
            to: spool
        )
        let attempts = LockedBox(0)
        StubURLProtocol.setHandler { _ in
            attempts.withValue { count in
                count += 1
                return count == 1
                    ? .init(status: 503, body: Data(#"{"error":{"code":"unavailable","message":"down"}}"#.utf8))
                    : .init(status: 202, body: Self.ingestResponse())
            }
        }
        let queue = makeQueue()

        await queue.trigger()

        var status = await queue.status
        XCTAssertEqual(status.pending, 1)
        XCTAssertNotNil(status.lastError)
        XCTAssertNotNil(status.nextRetryAt)
        XCTAssertTrue(FileManager.default.fileExists(atPath: file.path))

        await queue.retryNow()

        status = await queue.status
        XCTAssertEqual(status.pending, 0)
        XCTAssertEqual(status.sent, 1)
        XCTAssertNil(status.nextRetryAt)
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
        XCTAssertEqual(attempts.value, 2)
        let keys = StubURLProtocol.requests.map {
            $0.value(forHTTPHeaderField: "Idempotency-Key")
        }
        XCTAssertEqual(keys.count, 2)
        XCTAssertEqual(keys[0], bundleID)
        XCTAssertEqual(keys[1], bundleID, "a retry must replay the same key")
    }

    func testTransientFailureIsRetriedAutomaticallyAfterBackoff() async throws {
        let file = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        let attempts = LockedBox(0)
        StubURLProtocol.setHandler { _ in
            attempts.withValue { count in
                count += 1
                return count == 1
                    ? .init(status: 503, body: Data(#"{"error":{"code":"unavailable","message":"down"}}"#.utf8))
                    : .init(status: 202, body: Self.ingestResponse())
            }
        }
        let queue = UploadQueue(
            spoolDirectory: spool,
            client: client(),
            retryPolicy: .init(base: 0.01, cap: 0.02, maxAttempts: 5),
            jitter: { 1 }
        )

        await queue.trigger()
        for _ in 0..<200 {
            if !FileManager.default.fileExists(atPath: file.path) { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }

        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
        let status = await queue.status
        XCTAssertEqual(status.sent, 1)
        XCTAssertEqual(attempts.value, 2)
    }

    func testUnauthorizedStopsTheQueueUntilANewTokenIsProvided() async throws {
        let file = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { _ in
            .init(status: 401, body: Data(#"{"error":{"code":"unauthorized","message":"revoked"}}"#.utf8))
        }
        let queue = makeQueue()

        await queue.trigger()

        var status = await queue.status
        XCTAssertTrue(status.requiresAuthentication)
        XCTAssertNil(status.nextRetryAt)
        XCTAssertEqual(status.pending, 1)
        XCTAssertTrue(FileManager.default.fileExists(atPath: file.path))

        StubURLProtocol.setHandler { _ in .init(status: 202, body: Self.ingestResponse()) }
        await queue.updateClient(client())

        status = await queue.status
        XCTAssertFalse(status.requiresAuthentication)
        XCTAssertEqual(status.pending, 0)
        XCTAssertEqual(status.sent, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
    }

    func testReplayedResponseIsTreatedAsSuccess() async throws {
        let file = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { _ in
            .init(status: 200, body: Data(#"{"record_id":42,"job_id":null,"status":"stored","replayed":true}"#.utf8))
        }
        let queue = makeQueue()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.sent, 1)
        XCTAssertEqual(status.pending, 0)
        XCTAssertFalse(FileManager.default.fileExists(atPath: file.path))
    }

    func testPermanentValidationErrorMovesTheBundleToFailed() async throws {
        try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { _ in
            .init(
                status: 422,
                body: Data(#"{"error":{"code":"validation_failed","message":"bad bundle"}}"#.utf8)
            )
        }
        let queue = makeQueue()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.pending, 0)
        XCTAssertEqual(status.failed, 1)
        XCTAssertEqual(status.sent, 0)
        XCTAssertNotNil(status.lastError)
        XCTAssertTrue(try SpoolWatcher.bundleFiles(in: spool).isEmpty)
        let failed = try FileManager.default.contentsOfDirectory(
            atPath: spool.appendingPathComponent("failed").path
        )
        XCTAssertEqual(failed.count, 1)
        XCTAssertEqual(StubURLProtocol.requests.count, 1)
    }

    func testPermanentFailureDoesNotFallBackToAnOlderSnapshot() async throws {
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(
                externalID: "ses_1", title: "Old",
                updatedAt: Date(timeIntervalSince1970: 100)
            ),
            to: spool,
            name: "old.json"
        )
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(
                externalID: "ses_1", title: "New",
                updatedAt: Date(timeIntervalSince1970: 200)
            ),
            to: spool,
            name: "new.json"
        )
        StubURLProtocol.setHandler { _ in
            .init(
                status: 422,
                body: Data(#"{"error":{"code":"validation_failed","message":"bad bundle"}}"#.utf8)
            )
        }
        let queue = makeQueue()

        await queue.trigger()

        XCTAssertEqual(StubURLProtocol.requests.count, 1)
        XCTAssertTrue(try SpoolWatcher.bundleFiles(in: spool).isEmpty)
        let failed = try FileManager.default.contentsOfDirectory(
            atPath: spool.appendingPathComponent("failed").path
        )
        XCTAssertEqual(Set(failed), ["old.json", "new.json"])
    }

    func testInvalidBundleGoesToFailedWithoutTouchingTheNetwork() async throws {
        try SpoolFixtures.write(Data("{not json".utf8), to: spool, name: "broken.json")
        let queue = makeQueue()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.failed, 1)
        XCTAssertEqual(status.pending, 0)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
        XCTAssertEqual(
            try FileManager.default.contentsOfDirectory(
                atPath: spool.appendingPathComponent("failed").path
            ).count,
            1
        )
    }

    func testRateLimitHonoursRetryAfter() async throws {
        try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { _ in
            .init(
                status: 429,
                headers: ["Content-Type": "application/json", "Retry-After": "7"],
                body: Data(#"{"error":{"code":"rate_limited","message":"slow down"}}"#.utf8)
            )
        }
        let queue = UploadQueue(
            spoolDirectory: spool,
            client: client(),
            retryPolicy: .init(base: 0.01, cap: 0.01, maxAttempts: 5),
            jitter: { 0 }
        )
        let before = Date()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.pending, 1)
        XCTAssertNotNil(status.lastError)
        let retry = try XCTUnwrap(status.nextRetryAt)
        XCTAssertGreaterThanOrEqual(retry.timeIntervalSince(before), 7)
    }

    func testTmpFilesAreNeverSent() async throws {
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(),
            to: spool,
            name: "incoming.json.tmp"
        )
        let queue = makeQueue()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.pending, 0)
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
        XCTAssertTrue(
            FileManager.default.fileExists(
                atPath: spool.appendingPathComponent("incoming.json.tmp").path
            )
        )
    }

    func testInvalidBundleIsQuarantinedEvenWithoutAToken() async throws {
        try SpoolFixtures.write(Data("{not json".utf8), to: spool, name: "broken.json")
        let queue = UploadQueue(
            spoolDirectory: spool,
            client: nil,
            retryPolicy: .init(base: 3_600, cap: 3_600, maxAttempts: 5),
            jitter: { 1 }
        )

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.failed, 1)
        XCTAssertEqual(status.pending, 0)
        XCTAssertFalse(status.missingToken, "validation does not need the network")
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testMissingTokenKeepsAValidBundleAndFlagsIt() async throws {
        let file = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        let queue = UploadQueue(
            spoolDirectory: spool,
            client: nil,
            retryPolicy: .init(base: 3_600, cap: 3_600, maxAttempts: 5),
            jitter: { 1 }
        )

        await queue.trigger()

        let status = await queue.status
        XCTAssertTrue(status.missingToken)
        XCTAssertEqual(status.pending, 1)
        XCTAssertNotNil(status.lastError)
        XCTAssertTrue(FileManager.default.fileExists(atPath: file.path))
        XCTAssertTrue(StubURLProtocol.requests.isEmpty)
    }

    func testSuccessfulUploadDoesNotHideAnExistingFailedError() async throws {
        try SpoolFixtures.write(Data("{not json".utf8), to: spool, name: "broken.json")
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(externalID: "ses_good"),
            to: spool,
            name: "good.json"
        )
        StubURLProtocol.setHandler { _ in
            .init(status: 202, body: Self.ingestResponse())
        }
        let queue = makeQueue()

        await queue.trigger()

        let status = await queue.status
        XCTAssertEqual(status.failed, 1)
        XCTAssertEqual(status.sent, 1)
        XCTAssertEqual(status.pending, 0)
        XCTAssertNotNil(status.lastError, "failed files still need an explanation in the UI")
    }

    func testCleanPassClearsATransientError() async throws {
        let file = try SpoolFixtures.write(SpoolFixtures.bundleData(), to: spool)
        StubURLProtocol.setHandler { _ in
            .init(status: 503, body: Data(#"{"error":{"code":"unavailable","message":"down"}}"#.utf8))
        }
        let queue = makeQueue()
        await queue.trigger()
        var status = await queue.status
        XCTAssertNotNil(status.lastError)
        XCTAssertNotNil(status.nextRetryAt)

        try FileManager.default.removeItem(at: file)
        await queue.trigger()

        status = await queue.status
        XCTAssertNil(status.lastError)
        XCTAssertNil(status.nextRetryAt)
        XCTAssertEqual(status.pending, 0)
    }

    func testStatusHandlerSeesProgressDuringAMultiSessionPass() async throws {
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(externalID: "ses_a"),
            to: spool,
            name: "a.json"
        )
        try SpoolFixtures.write(
            SpoolFixtures.bundleData(externalID: "ses_b"),
            to: spool,
            name: "b.json"
        )
        StubURLProtocol.setHandler { _ in
            .init(status: 202, body: Self.ingestResponse())
        }
        let collector = StatusCollector()
        let queue = makeQueue()
        await queue.setStatusHandler { collector.append($0) }

        await queue.trigger()

        let snapshots = collector.values
        XCTAssertTrue(
            snapshots.contains { $0.sent == 1 && $0.pending == 1 },
            "the first delivered session must be visible before the pass ends"
        )
        XCTAssertEqual(snapshots.last?.sent, 2)
        XCTAssertEqual(snapshots.last?.pending, 0)
    }

    func testClearFailedRemovesStoredFailures() async throws {
        try SpoolFixtures.write(Data("{}".utf8), to: spool, name: "broken.json")
        let queue = makeQueue()
        await queue.trigger()
        let before = await queue.status
        XCTAssertEqual(before.failed, 1)

        await queue.clearFailed()

        let status = await queue.status
        XCTAssertEqual(status.failed, 0)
        XCTAssertFalse(
            FileManager.default.fileExists(atPath: spool.appendingPathComponent("failed").path)
        )
    }

    func testBackoffUsesFullJitterAndRetryAfter() {
        let policy = UploadRetryPolicy(base: 1, cap: 60, maxAttempts: 5)

        XCTAssertEqual(policy.delay(attempt: 0, retryAfter: nil, jitter: 0.5), 0.5, accuracy: 0.001)
        XCTAssertEqual(policy.delay(attempt: 1, retryAfter: nil, jitter: 0.5), 1.0, accuracy: 0.001)
        XCTAssertEqual(policy.delay(attempt: 10, retryAfter: nil, jitter: 0.5), 30, accuracy: 0.001)
        XCTAssertEqual(policy.delay(attempt: 2, retryAfter: 7, jitter: 0), 7, accuracy: 0.001)
    }

    private func makeQueue(
        policy: UploadRetryPolicy = .init(base: 3_600, cap: 3_600, maxAttempts: 5)
    ) -> UploadQueue {
        UploadQueue(
            spoolDirectory: spool,
            client: client(),
            retryPolicy: policy,
            jitter: { 1 }
        )
    }

    private func client() -> APIClient {
        APIClient(
            baseURL: serverURL,
            token: token,
            session: StubURLProtocol.session()
        )
    }

    private static func ingestResponse() -> Data {
        Data(#"{"record_id":42,"job_id":17,"status":"queued","replayed":false}"#.utf8)
    }
}

/// Small thread-safe box for request counters used by URLProtocol handlers.
private final class LockedBox<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: Value

    init(_ value: Value) {
        stored = value
    }

    var value: Value {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }

    func withValue<T>(_ body: (inout Value) -> T) -> T {
        lock.lock()
        defer { lock.unlock() }
        return body(&stored)
    }
}

/// Collects status snapshots published by `UploadQueue`.
private final class StatusCollector: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: [UploadStatus] = []

    var values: [UploadStatus] {
        lock.lock()
        defer { lock.unlock() }
        return stored
    }

    func append(_ status: UploadStatus) {
        lock.lock()
        stored.append(status)
        lock.unlock()
    }
}
