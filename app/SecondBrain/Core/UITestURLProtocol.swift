#if DEBUG
import Foundation

/// In-process API used by UI tests. The macOS UI-test runner is sandboxed and
/// cannot listen on a local port, so the app serves the same JSON shapes from a
/// URLProtocol instead. Release builds never compile this file.
final class UITestURLProtocol: URLProtocol {
    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [UITestURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let url = request.url ?? URL(string: "https://ui.test")!
        let path = url.path
        let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        let query = Dictionary(
            uniqueKeysWithValues: (components?.queryItems ?? []).compactMap { item in
                item.value.map { (item.name, $0) }
            }
        )

        let payload: (Int, Data)
        switch path {
        case "/health":
            payload = (200, Data(Self.healthJSON.utf8))
        case "/api/v1/stats":
            payload = (200, Data(Self.statsJSON.utf8))
        case "/api/v1/search":
            if query["q"] == "fail" {
                payload = (
                    500,
                    Data(#"{"error":{"code":"internal_error","message":"ui-test failure"}}"#.utf8)
                )
            } else {
                payload = (200, Self.searchResponse(query: query))
            }
        case "/api/v1/projects":
            payload = (200, Self.projectsResponse())
        default:
            if path.hasPrefix("/api/v1/records/"),
               let id = Int64(path.split(separator: "/").last ?? ""),
               let record = Self.recordResponse(id: id) {
                payload = (200, record)
            } else {
                payload = (404, Data(#"{"error":{"code":"not_found","message":"Not found"}}"#.utf8))
            }
        }

        let response = HTTPURLResponse(
            url: url,
            statusCode: payload.0,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: payload.1)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    // MARK: - Fixtures

    private static let cards: [[String: Any]] = [
        [
            "id": 1,
            "source": "jarvis",
            "kind": "memory",
            "project_key": "jarvis",
            "title": "Jarvis SQLite notes",
            "summary": "SQLite migration ordering memo.",
            "questions": ["How should migrations be ordered?"],
            "keywords": ["sqlite", "ordering"],
            "atoms": ["Numeric prefixes keep migrations stable."],
            "is_final": true,
            "started_at": "2026-11-04T10:00:00Z",
            "updated_at": "2026-11-04T10:00:00Z",
            "card_status": "ok",
        ],
        [
            "id": 2,
            "source": "opencode",
            "kind": "session",
            "project_key": "github.com/example/meme",
            "title": "SQLite migration ordering",
            "summary": "Migrations were sorted lexicographically.",
            "questions": ["Why did migration 004 run first?"],
            "keywords": ["sqlite", "migration"],
            "atoms": ["Sort migrations by their numeric prefix."],
            "is_final": true,
            "started_at": "2026-11-05T10:00:00Z",
            "updated_at": "2026-11-05T10:00:00Z",
            "card_status": "ok",
        ],
        [
            "id": 3,
            "source": "opencode",
            "kind": "session",
            "project_key": "github.com/example/meme",
            "title": "Spool upload retries",
            "summary": "Bundles retry with full jitter.",
            "questions": [],
            "keywords": ["spool", "upload"],
            "atoms": ["Retry transient failures only."],
            "is_final": true,
            "started_at": "2026-11-05T11:00:00Z",
            "updated_at": "2026-11-05T11:00:00Z",
            "card_status": "ok",
        ],
    ]

    private static let rawTexts: [Int64: String] = [
        1: """
        Jarvis note about SQLite
        We keep migration ordering numeric.
        Another line.
        """,
        2: """
        SQLite migration ordering
        Migrations now sort by numeric prefix.
        Use the migration test.
        """,
        3: """
        Spool upload retries
        Use full jitter and Retry-After.
        """,
    ]

    private static func searchResponse(query: [String: String]) -> Data {
        let terms = (query["q"] ?? "")
            .lowercased()
            .split(whereSeparator: \.isWhitespace)
            .map(String.init)
        let source = query["source"]
        let project = query["project_key"]
        let since = query["since"].flatMap(APIDate.parse)
        let until = query["until"].flatMap(APIDate.parse)
        let limit = Int(query["limit"] ?? "20") ?? 20

        let matches = cards.filter { card in
            if let source, card["source"] as? String != source { return false }
            if let project, card["project_key"] as? String != project { return false }
            if let updated = APIDate.parse(card["updated_at"] as? String ?? "") {
                if let since, updated < since { return false }
                if let until, updated > until { return false }
            }
            guard !terms.isEmpty else { return true }
            let haystack = [
                card["title"] as? String ?? "",
                card["summary"] as? String ?? "",
                (card["keywords"] as? [String] ?? []).joined(separator: " "),
                (card["questions"] as? [String] ?? []).joined(separator: " "),
                (card["atoms"] as? [String] ?? []).joined(separator: " "),
            ].joined(separator: " ").lowercased()
            return terms.allSatisfy(haystack.contains)
        }
        .sorted { lhs, rhs in
            (lhs["updated_at"] as? String ?? "") > (rhs["updated_at"] as? String ?? "")
        }

        let items = matches.prefix(limit).map { card -> [String: Any] in
            var copy = card
            copy["score"] = 4.82
            return copy
        }
        let body: [String: Any] = [
            "items": Array(items),
            "total": matches.count,
            "query": query["q"] ?? "",
            "limit": limit,
        ]
        return (try? JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    private static func recordResponse(id: Int64) -> Data? {
        guard let card = cards.first(where: {
            ($0["id"] as? NSNumber)?.int64Value == id
        }) else {
            return nil
        }
        var object = card
        object["raw_text"] = rawTexts[id] ?? ""
        object["revision"] = 1
        object["metadata"] = [:]
        object["indexed_at"] = card["updated_at"]
        return try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    }

    private static func projectsResponse() -> Data {
        let body: [String: Any] = [
            "items": [
                [
                    "key": "github.com/example/meme",
                    "name": "meme",
                    "path": "/tmp/meme",
                    "record_count": 2,
                    "last_activity_at": "2026-11-05T11:00:00Z",
                    "created_at": "2026-10-01T12:00:00Z",
                ],
                [
                    "key": "jarvis",
                    "name": "Jarvis",
                    "path": "",
                    "record_count": 1,
                    "last_activity_at": "2026-11-04T10:00:00Z",
                    "created_at": "2026-10-01T12:00:00Z",
                ],
            ]
        ]
        return (try? JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    private static let healthJSON = """
    {"status":"ok","version":"ui-test","schema_version":3,
     "time":"2026-11-05T10:00:00Z",
     "database":{"status":"ok","fts5":true}}
    """

    private static let statsJSON = """
    {"records":{"total":3,"final":3,"draft":0,
      "by_source":{"opencode":2,"jarvis":1},
      "by_card_status":{"heuristic":0,"queued":0,"ok":3,"failed":0}},
     "projects":2,"clients":1,
     "jobs":{"queued":0,"running":0,"done":3,"failed":0},
     "database":{"status":"ok","schema_version":3,"size_bytes":2048}}
    """
}
#endif
