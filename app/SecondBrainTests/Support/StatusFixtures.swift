import Foundation

enum StatusFixtures {
    static func statsData(
        recordsTotal: Int = 7,
        final: Int = 6,
        draft: Int = 1,
        sources: [String: Int] = ["opencode": 5, "jarvis": 2],
        projects: Int = 3,
        clients: Int = 2,
        queued: Int = 1,
        running: Int = 1,
        done: Int = 5,
        failed: Int = 2,
        lastIngestAt: String? = "2026-11-05T10:00:00Z"
    ) -> Data {
        var object: [String: Any] = [
            "records": [
                "total": recordsTotal,
                "final": final,
                "draft": draft,
                "by_source": sources,
                "by_card_status": [
                    "heuristic": 0,
                    "queued": queued,
                    "ok": done,
                    "failed": failed,
                ],
            ],
            "projects": projects,
            "clients": clients,
            "jobs": [
                "queued": queued,
                "running": running,
                "done": done,
                "failed": failed,
            ],
            "database": [
                "status": "ok",
                "schema_version": 4,
                "size_bytes": 4096,
            ],
        ]
        object["last_ingest_at"] = lastIngestAt ?? NSNull()
        return (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    static func jobsData(_ jobs: [[String: Any]], nextCursor: String? = nil) -> Data {
        let body: [String: Any] = ["items": jobs, "next_cursor": nextCursor ?? NSNull()]
        return (try? JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    static func job(
        id: Int64,
        clientID: Int64,
        kind: String = "ingest",
        status: String = "failed",
        attempts: Int = 2,
        externalID: String = "ses_test",
        recordID: Int64? = nil,
        error: String? = "upstream timeout",
        createdAt: String = "2026-11-05T10:00:00Z",
        updatedAt: String = "2026-11-05T10:05:00Z"
    ) -> [String: Any] {
        var object: [String: Any] = [
            "id": id,
            "client_id": clientID,
            "kind": kind,
            "status": status,
            "attempts": attempts,
            "external_id": externalID,
            "created_at": createdAt,
            "updated_at": updatedAt,
        ]
        object["record_id"] = recordID
        object["next_retry_at"] = NSNull()
        object["error"] = error
        return object
    }

    static func clientsData(_ clients: [[String: Any]]) -> Data {
        (try? JSONSerialization.data(withJSONObject: ["items": clients], options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    static func client(
        id: Int64,
        name: String,
        kind: String,
        source: String? = nil,
        revokedAt: String? = nil,
        lastSeenAt: String? = nil,
        scopes: [String] = []
    ) -> [String: Any] {
        var object: [String: Any] = [
            "id": id,
            "name": name,
            "kind": kind,
            "scopes": scopes,
            "created_at": "2026-10-01T12:00:00Z",
        ]
        object["source"] = source ?? (kind == "opencode" || kind == "jarvis" ? kind : nil)
        object["last_seen_at"] = lastSeenAt
        object["revoked_at"] = revokedAt
        return object
    }
}
