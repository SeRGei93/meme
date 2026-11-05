import Foundation
import XCTest
@testable import SecondBrain

final class SpoolBundleTests: XCTestCase {
    private var directory: URL!

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-bundle-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
        directory = nil
        try super.tearDownWithError()
    }

    func testLoadingBuildsAnIngestBodyWithoutServerOwnedFields() throws {
        let bundleID = UUID().uuidString
        let url = try SpoolFixtures.write(
            SpoolFixtures.bundleData(bundleID: bundleID, title: "Migration ordering"),
            to: directory
        )

        let bundle = try SpoolBundleLoader.load(from: url)

        XCTAssertEqual(bundle.bundleID, bundleID)
        XCTAssertEqual(bundle.externalID, "ses_01JABCDEF")
        XCTAssertEqual(bundle.idempotencyKey, bundleID)
        XCTAssertEqual(bundle.title, "Migration ordering")
        XCTAssertTrue(bundle.final)
        XCTAssertEqual(bundle.messages.count, 1)
        XCTAssertEqual(bundle.metadata.agent, "build")

        let body = try bundle.ingestBody()
        let object = try SpoolFixtures.bundleObjects(in: body)
        XCTAssertNil(object["schema_version"])
        XCTAssertNil(object["source"])
        XCTAssertEqual(object["bundle_id"] as? String, bundleID)
        XCTAssertEqual(object["external_id"] as? String, "ses_01JABCDEF")
        XCTAssertEqual(object["project_key"] as? String, "github.com/example/repo")
        XCTAssertEqual(object["title"] as? String, "Migration ordering")
        XCTAssertEqual(object["final"] as? Bool, true)
        let messages = try XCTUnwrap(object["messages"] as? [[String: Any]])
        XCTAssertEqual(messages.count, 1)
    }

    func testUnknownTopLevelFieldIsRejected() throws {
        let data = try SpoolFixtures.bundleData(extra: ["surprise": true])
        let url = try SpoolFixtures.write(data, to: directory)

        XCTAssertThrowsError(try SpoolBundleLoader.load(from: url)) { error in
            guard let spoolError = error as? SpoolBundleError,
                  case .invalid(let message) = spoolError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertTrue(message.contains("surprise"))
        }
    }

    func testMissingRequiredFieldIsRejected() throws {
        var object = try SpoolFixtures.bundleObjects(in: SpoolFixtures.bundleData())
        object.removeValue(forKey: "project_key")
        let url = try SpoolFixtures.write(
            JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]),
            to: directory
        )

        XCTAssertThrowsError(try SpoolBundleLoader.load(from: url)) { error in
            guard let spoolError = error as? SpoolBundleError,
                  case .invalid(let message) = spoolError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertTrue(message.contains("project_key"))
        }
    }

    func testInvalidUUIDAndBadDatesAreRejected() throws {
        let badUUID = try SpoolFixtures.bundleData(bundleID: "not-a-uuid")
        let badDate = try SpoolFixtures.bundleData(extra: ["started_at": "yesterday"])

        XCTAssertThrowsError(
            try SpoolBundleLoader.load(from: SpoolFixtures.write(badUUID, to: directory))
        )
        XCTAssertThrowsError(
            try SpoolBundleLoader.load(from: SpoolFixtures.write(badDate, to: directory))
        )
    }

    func testMessageLimitsAndRolesAreRejected() throws {
        let empty = try SpoolFixtures.bundleData(messages: [])
        let badRole = try SpoolFixtures.bundleData(messages: [[
            "id": "msg_1", "role": "narrator", "text": "hi",
            "at": SpoolFixtures.isoFormatter.string(from: Date()),
        ]])
        let tooLong = try SpoolFixtures.bundleData(messages: [[
            "id": "msg_1", "role": "user",
            "text": String(repeating: "a", count: 200_001),
            "at": SpoolFixtures.isoFormatter.string(from: Date()),
        ]])

        for data in [empty, badRole, tooLong] {
            XCTAssertThrowsError(
                try SpoolBundleLoader.load(from: SpoolFixtures.write(data, to: directory))
            )
        }
    }

    func testUnknownMetadataFieldIsRejected() throws {
        let data = try SpoolFixtures.bundleData(metadata: ["unknown": "x"])
        let url = try SpoolFixtures.write(data, to: directory)

        XCTAssertThrowsError(try SpoolBundleLoader.load(from: url)) { error in
            guard let spoolError = error as? SpoolBundleError,
                  case .invalid(let message) = spoolError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertTrue(message.contains("metadata"))
        }
    }

    func testBodiesOverTenMiBareRejected() throws {
        let messages: [[String: Any]] = (0..<60).map { index in
            [
                "id": "msg_\(index)",
                "role": "user",
                "text": String(repeating: "x", count: 200_000),
                "at": SpoolFixtures.isoFormatter.string(from: Date()),
            ]
        }
        let data = try SpoolFixtures.bundleData(messages: messages)
        let url = try SpoolFixtures.write(data, to: directory)

        XCTAssertThrowsError(try SpoolBundleLoader.load(from: url)) { error in
            guard let spoolError = error as? SpoolBundleError,
                  case .tooLarge = spoolError else {
                return XCTFail("unexpected error \(error)")
            }
        }
    }

    func testInvalidJSONIsReported() throws {
        let url = try SpoolFixtures.write(Data("{not json".utf8), to: directory)

        XCTAssertThrowsError(try SpoolBundleLoader.load(from: url)) { error in
            guard let spoolError = error as? SpoolBundleError,
                  case .invalidJSON = spoolError else {
                return XCTFail("unexpected error \(error)")
            }
        }
    }
}
