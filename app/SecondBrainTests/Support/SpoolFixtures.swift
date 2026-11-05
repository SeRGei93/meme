import Foundation
@testable import SecondBrain

enum SpoolFixtures {
    static let isoFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    static func bundleData(
        externalID: String = "ses_01JABCDEF",
        bundleID: String = UUID().uuidString,
        title: String = "Test session",
        final: Bool = true,
        updatedAt: Date = Date(timeIntervalSince1970: 1_793_820_000),
        text: String = "hello",
        schemaVersion: Int? = 1,
        source: String = "opencode",
        metadata: [String: Any] = ["agent": "build"],
        extra: [String: Any] = [:],
        messages: [[String: Any]]? = nil
    ) throws -> Data {
        var object: [String: Any] = [
            "bundle_id": bundleID,
            "source": source,
            "external_id": externalID,
            "project_key": "github.com/example/repo",
            "project_path": "/tmp/example",
            "title": title,
            "final": final,
            "started_at": isoFormatter.string(from: updatedAt.addingTimeInterval(-60)),
            "updated_at": isoFormatter.string(from: updatedAt),
            "messages": messages ?? [
                [
                    "id": "msg_1",
                    "role": "user",
                    "text": text,
                    "at": isoFormatter.string(from: updatedAt),
                ],
            ],
            "metadata": metadata,
        ]
        if let schemaVersion {
            object["schema_version"] = schemaVersion
        }
        for (key, value) in extra {
            object[key] = value
        }
        return try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    }

    @discardableResult
    static func write(
        _ data: Data,
        to directory: URL,
        name: String? = nil
    ) throws -> URL {
        try FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true
        )
        let url = directory.appendingPathComponent(
            name ?? "\(UUID().uuidString).json",
            isDirectory: false
        )
        try data.write(to: url)
        return url
    }

    static func bundleObjects(in data: Data) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }
}
