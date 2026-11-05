import Foundation

enum GraphFixtures {
    static func graphData(
        nodes: [[String: Any]],
        edges: [[String: Any]] = [],
        truncated: Bool = false
    ) -> Data {
        let body: [String: Any] = [
            "nodes": nodes,
            "edges": edges,
            "truncated": truncated,
        ]
        return (try? JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]))
            ?? Data("{}".utf8)
    }

    static func node(
        id: Int64,
        title: String,
        projectKey: String = "github.com/example/meme",
        source: String = "opencode",
        kind: String = "session",
        updatedAt: String = "2026-11-05T10:00:00Z"
    ) -> [String: Any] {
        [
            "id": id,
            "title": title,
            "project_key": projectKey,
            "source": source,
            "kind": kind,
            "updated_at": updatedAt,
        ]
    }

    static func edge(
        from: Int64,
        to: Int64,
        weight: Double = 0.5,
        reason: String = "keywords"
    ) -> [String: Any] {
        [
            "from_id": from,
            "to_id": to,
            "weight": weight,
            "reason": reason,
        ]
    }
}
