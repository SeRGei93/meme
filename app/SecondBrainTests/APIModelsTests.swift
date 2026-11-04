import Foundation
import XCTest
@testable import SecondBrain

final class APIModelsTests: XCTestCase {
    private let decoder = APIClient.decoder
    private lazy var encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }()

    func testRecordFlattensCardFieldsAndKeepsMetadata() throws {
        let record = try decoder.decode(Record.self, from: Data(Self.recordJSON.utf8))

        XCTAssertEqual(record.card.id, 42)
        XCTAssertEqual(record.card.source, "opencode")
        XCTAssertEqual(record.card.kind, "session")
        XCTAssertEqual(record.card.projectKey, "opencode:meme")
        XCTAssertEqual(record.card.title, "Spool upload")
        XCTAssertEqual(record.card.keywords, ["spool", "upload"])
        XCTAssertTrue(record.card.isFinal)
        XCTAssertEqual(record.card.cardStatus, "ok")
        XCTAssertNil(record.card.cardModel)
        XCTAssertNotNil(record.card.startedAt)
        XCTAssertNotNil(record.card.finalizedAt)
        XCTAssertEqual(record.rawText, "hello world")
        XCTAssertEqual(record.revision, 2)
        XCTAssertEqual(record.metadata["agent"], .string("build"))
        XCTAssertEqual(record.metadata["tokens"], .number(123))
        XCTAssertEqual(record.metadata["nested"], .object(["ok": .bool(true)]))
        XCTAssertNotNil(record.indexedAt)
    }

    func testRecordRoundTripsThroughJSON() throws {
        let record = try decoder.decode(Record.self, from: Data(Self.recordJSON.utf8))

        let encoded = try encoder.encode(record)
        let decoded = try decoder.decode(Record.self, from: encoded)

        XCTAssertEqual(decoded, record)
    }

    func testSearchHitCarriesScore() throws {
        let json = """
        {"items":[{
          "id":1,"source":"jarvis","kind":"memory","project_key":"jarvis",
          "title":"T","summary":"S","questions":[],"keywords":[],"atoms":[],
          "is_final":true,"updated_at":"2026-11-04T19:30:10Z",
          "card_status":"heuristic","score":0.75
        }],"total":1,"query":"T","limit":10}
        """

        let response = try decoder.decode(SearchResponse.self, from: Data(json.utf8))

        XCTAssertEqual(response.items.first?.score, 0.75)
        XCTAssertNil(response.items.first?.startedAt)
    }

    func testProjectListDecodesSnakeCaseFields() throws {
        let json = """
        {"items":[{"key":"opencode:meme","name":"meme","path":"/Users/serg/meme",
          "record_count":12,"last_activity_at":null,
          "created_at":"2026-11-01T10:00:00Z"}]}
        """

        let list = try decoder.decode(ProjectList.self, from: Data(json.utf8))

        XCTAssertEqual(list.items.first?.id, "opencode:meme")
        XCTAssertEqual(list.items.first?.recordCount, 12)
        XCTAssertNil(list.items.first?.lastActivityAt)
    }

    private static let recordJSON = """
    {
      "id": 42,
      "source": "opencode",
      "kind": "session",
      "project_key": "opencode:meme",
      "title": "Spool upload",
      "summary": "Uploaded a bundle",
      "questions": ["How does upload work?"],
      "keywords": ["spool", "upload"],
      "atoms": ["bundle_id"],
      "is_final": true,
      "started_at": "2026-11-04T19:30:10Z",
      "updated_at": "2026-11-04T19:31:10Z",
      "finalized_at": "2026-11-04T19:32:10Z",
      "card_status": "ok",
      "card_model": null,
      "raw_text": "hello world",
      "revision": 2,
      "metadata": {
        "agent": "build",
        "tokens": 123,
        "nested": { "ok": true }
      },
      "indexed_at": "2026-11-04T19:33:10Z"
    }
    """
}
