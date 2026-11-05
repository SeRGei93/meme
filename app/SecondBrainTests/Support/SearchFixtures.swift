import Foundation

enum SearchFixtures {
    static func card(
        id: Int64,
        title: String,
        summary: String = "",
        source: String = "opencode",
        kind: String = "session",
        projectKey: String = "github.com/example/repo",
        questions: [String] = [],
        keywords: [String] = [],
        atoms: [String] = [],
        isFinal: Bool = true,
        updatedAt: String = "2026-11-03T12:00:00Z",
        cardStatus: String = "ok",
        score: Double? = nil
    ) -> [String: Any] {
        var object: [String: Any] = [
            "id": id,
            "source": source,
            "kind": kind,
            "project_key": projectKey,
            "title": title,
            "summary": summary,
            "questions": questions,
            "keywords": keywords,
            "atoms": atoms,
            "is_final": isFinal,
            "started_at": "2026-11-03T11:00:00Z",
            "updated_at": updatedAt,
            "card_status": cardStatus,
        ]
        if let score {
            object["score"] = score
        }
        return object
    }

    static func searchData(
        items: [[String: Any]],
        total: Int? = nil,
        query: String,
        limit: Int = 20
    ) throws -> Data {
        try JSONSerialization.data(
            withJSONObject: [
                "items": items,
                "total": total ?? items.count,
                "query": query,
                "limit": limit,
            ],
            options: [.sortedKeys]
        )
    }

    static func recordData(
        card: [String: Any],
        rawText: String,
        revision: Int = 1,
        metadata: [String: Any] = [:]
    ) throws -> Data {
        var object = card
        object["raw_text"] = rawText
        object["revision"] = revision
        object["metadata"] = metadata
        object["indexed_at"] = card["updated_at"]
        return try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    }

    static func projectsData(
        _ projects: [(key: String, name: String, path: String, count: Int)]
    ) throws -> Data {
        let items: [[String: Any]] = projects.map { project in
            [
                "key": project.key,
                "name": project.name,
                "path": project.path,
                "record_count": project.count,
                "last_activity_at": "2026-11-03T12:00:00Z",
                "created_at": "2026-10-01T12:00:00Z",
            ]
        }
        return try JSONSerialization.data(
            withJSONObject: ["items": items],
            options: [.sortedKeys]
        )
    }
}
