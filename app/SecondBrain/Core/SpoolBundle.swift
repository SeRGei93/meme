import Foundation

enum SpoolBundleError: Error, Equatable {
    case unreadable(String)
    case invalidJSON(String)
    case invalid(String)
    case tooLarge(Int)
}

extension SpoolBundleError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .unreadable(let detail):
            return "Не удалось прочитать бандл: \(detail)"
        case .invalidJSON(let detail):
            return "Бандл не является корректным JSON: \(detail)"
        case .invalid(let detail):
            return "Бандл не соответствует контракту: \(detail)"
        case .tooLarge(let bytes):
            return "Бандл слишком большой: \(bytes) байт (лимит запроса 10 MiB)"
        }
    }
}

enum SpoolMessageRole: String, Sendable {
    case user
    case assistant
    case system
    case tool
}

struct SpoolToolCall: Encodable, Equatable, Sendable {
    let name: String
    let command: String?
    let path: String?

    enum CodingKeys: String, CodingKey {
        case name, command, path
    }
}

struct SpoolMessage: Encodable, Equatable, Sendable {
    let id: String
    let role: SpoolMessageRole
    let text: String
    let at: String
    let toolCalls: [SpoolToolCall]?

    enum CodingKeys: String, CodingKey {
        case id, role, text, at
        case toolCalls = "tool_calls"
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(role.rawValue, forKey: .role)
        try container.encode(text, forKey: .text)
        try container.encode(at, forKey: .at)
        try container.encodeIfPresent(toolCalls, forKey: .toolCalls)
    }
}

struct SpoolMetadata: Encodable, Equatable, Sendable {
    let agent: String?
    let model: String?
    let branch: String?
    let opencodeVersion: String?

    enum CodingKeys: String, CodingKey {
        case agent, model, branch
        case opencodeVersion = "opencode_version"
    }
}

/// One validated spool bundle. `ingestBody()` is the exact request body for
/// `POST /api/v1/ingest`: `schema_version` and `source` are server-owned and are
/// omitted from the request.
struct SpoolBundle: Equatable, Sendable {
    let schemaVersion: Int?
    let bundleID: String
    let externalID: String
    let projectKey: String
    let projectPath: String
    let title: String
    let final: Bool
    let startedAt: String
    let updatedAt: String
    let messages: [SpoolMessage]
    let metadata: SpoolMetadata

    var idempotencyKey: String { bundleID }

    var updatedDate: Date? {
        SpoolBundleLoader.parseDate(updatedAt)
    }

    func ingestBody() throws -> Data {
        let payload = IngestPayload(
            bundleID: bundleID,
            externalID: externalID,
            projectKey: projectKey,
            projectPath: projectPath,
            title: title,
            final: final,
            startedAt: startedAt,
            updatedAt: updatedAt,
            messages: messages,
            metadata: metadata
        )
        let data: Data
        do {
            data = try JSONEncoder().encode(payload)
        } catch {
            throw SpoolBundleError.invalid("не удалось закодировать запрос: \(error.localizedDescription)")
        }
        guard data.count <= SpoolBundleLoader.maxBodyBytes else {
            throw SpoolBundleError.tooLarge(data.count)
        }
        return data
    }

    private struct IngestPayload: Encodable {
        let bundleID: String
        let externalID: String
        let projectKey: String
        let projectPath: String
        let title: String
        let final: Bool
        let startedAt: String
        let updatedAt: String
        let messages: [SpoolMessage]
        let metadata: SpoolMetadata

        enum CodingKeys: String, CodingKey {
            case bundleID = "bundle_id"
            case externalID = "external_id"
            case projectKey = "project_key"
            case projectPath = "project_path"
            case title, final
            case startedAt = "started_at"
            case updatedAt = "updated_at"
            case messages, metadata
        }
    }
}

/// Strict validator for `docs/spool.schema.json`. The server rejects unknown
/// fields, so the app rejects them locally and moves the bundle to `failed/`
/// instead of filling the queue with requests that can never succeed.
enum SpoolBundleLoader {
    static let maxBodyBytes = 10 * 1024 * 1024
    /// Refuse absurd files before reading them into memory. The actual request
    /// size is checked after removing server-owned fields.
    static let maxFileBytes = 64 * 1024 * 1024
    static let maxMessages = 10_000
    static let maxMessageText = 200_000
    static let maxTitle = 500
    static let maxProjectKey = 500
    static let maxProjectPath = 1_000
    static let maxExternalID = 200
    static let maxToolCalls = 500
    static let maxToolField = 2_000
    static let maxMetadataField = 200

    private static let bundleKeys: Set<String> = [
        "schema_version", "bundle_id", "source", "external_id", "project_key",
        "project_path", "title", "final", "started_at", "updated_at", "messages",
        "metadata",
    ]
    private static let bundleRequired: Set<String> = [
        "bundle_id", "source", "external_id", "project_key", "project_path",
        "title", "final", "started_at", "updated_at", "messages", "metadata",
    ]
    private static let messageKeys: Set<String> = [
        "id", "role", "text", "at", "tool_calls",
    ]
    private static let messageRequired: Set<String> = ["id", "role", "text", "at"]
    private static let toolCallKeys: Set<String> = ["name", "command", "path"]
    private static let metadataKeys: Set<String> = [
        "agent", "model", "branch", "opencode_version",
    ]

    static func load(from url: URL) throws -> SpoolBundle {
        if let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize,
           size > maxFileBytes {
            throw SpoolBundleError.tooLarge(size)
        }

        let data: Data
        do {
            data = try Data(contentsOf: url)
        } catch {
            throw SpoolBundleError.unreadable("\(url.lastPathComponent): \(error.localizedDescription)")
        }
        return try decode(data)
    }

    static func decode(_ data: Data) throws -> SpoolBundle {
        let rootObject: Any
        do {
            rootObject = try JSONSerialization.jsonObject(with: data)
        } catch {
            throw SpoolBundleError.invalidJSON(error.localizedDescription)
        }
        guard let root = rootObject as? [String: Any] else {
            throw SpoolBundleError.invalid("/: корневой элемент должен быть объектом")
        }
        try checkKeys(root, allowed: bundleKeys, required: bundleRequired, path: "/")

        if let rawVersion = root["schema_version"] {
            let version = try int(rawVersion, path: "/schema_version")
            guard version == 1 else {
                throw SpoolBundleError.invalid("/schema_version: поддерживается только 1")
            }
        }

        let bundleID = try string(root["bundle_id"], path: "/bundle_id", max: 200)
        guard UUID(uuidString: bundleID) != nil else {
            throw SpoolBundleError.invalid("/bundle_id: ожидается UUID")
        }
        let source = try string(root["source"], path: "/source", max: 20)
        guard source == "opencode" else {
            throw SpoolBundleError.invalid("/source: ожидается opencode")
        }
        let externalID = try string(root["external_id"], path: "/external_id", max: maxExternalID)
        guard externalID.rangeOfCharacter(from: .whitespacesAndNewlines) == nil else {
            throw SpoolBundleError.invalid("/external_id: пробелы запрещены")
        }
        let projectKey = try string(root["project_key"], path: "/project_key", max: maxProjectKey)
        let projectPath = try string(root["project_path"], path: "/project_path", max: maxProjectPath)
        let title = try string(root["title"], path: "/title", max: maxTitle)
        let final = try bool(root["final"], path: "/final")
        let startedAt = try dateString(root["started_at"], path: "/started_at")
        let updatedAt = try dateString(root["updated_at"], path: "/updated_at")
        guard let started = parseDate(startedAt),
              let updated = parseDate(updatedAt) else {
            throw SpoolBundleError.invalid("/: некорректная дата")
        }
        guard started <= updated else {
            throw SpoolBundleError.invalid("/started_at: позже updated_at")
        }

        let messages = try parseMessages(root["messages"])
        let metadata = try parseMetadata(root["metadata"])

        let bundle = SpoolBundle(
            schemaVersion: root["schema_version"] == nil ? nil : 1,
            bundleID: bundleID,
            externalID: externalID,
            projectKey: projectKey,
            projectPath: projectPath,
            title: title,
            final: final,
            startedAt: startedAt,
            updatedAt: updatedAt,
            messages: messages,
            metadata: metadata
        )
        _ = try bundle.ingestBody()
        return bundle
    }

    // MARK: - Nested values

    private static func parseMessages(_ value: Any?) throws -> [SpoolMessage] {
        guard let raw = value as? [Any] else {
            throw SpoolBundleError.invalid("/messages: ожидается массив")
        }
        guard (1...maxMessages).contains(raw.count) else {
            throw SpoolBundleError.invalid("/messages: должно быть 1…\(maxMessages) элементов")
        }

        return try raw.enumerated().map { index, item in
            let path = "/messages/\(index)"
            guard let object = item as? [String: Any] else {
                throw SpoolBundleError.invalid("\(path): ожидается объект")
            }
            try checkKeys(object, allowed: messageKeys, required: messageRequired, path: path)

            let id = try string(object["id"], path: "\(path)/id", max: 200)
            let roleText = try string(object["role"], path: "\(path)/role", max: 20)
            guard let role = SpoolMessageRole(rawValue: roleText) else {
                throw SpoolBundleError.invalid("\(path)/role: неизвестная роль \(roleText)")
            }
            let text = try string(
                object["text"],
                path: "\(path)/text",
                max: maxMessageText,
                allowEmpty: true
            )
            let at = try dateString(object["at"], path: "\(path)/at")

            var toolCalls: [SpoolToolCall]?
            if let rawCalls = object["tool_calls"] {
                guard let calls = rawCalls as? [Any] else {
                    throw SpoolBundleError.invalid("\(path)/tool_calls: ожидается массив")
                }
                guard calls.count <= maxToolCalls else {
                    throw SpoolBundleError.invalid("\(path)/tool_calls: максимум \(maxToolCalls)")
                }
                toolCalls = try calls.enumerated().map { callIndex, call in
                    let callPath = "\(path)/tool_calls/\(callIndex)"
                    guard let callObject = call as? [String: Any] else {
                        throw SpoolBundleError.invalid("\(callPath): ожидается объект")
                    }
                    try checkKeys(callObject, allowed: toolCallKeys, required: ["name"], path: callPath)
                    return SpoolToolCall(
                        name: try string(callObject["name"], path: "\(callPath)/name", max: 200),
                        command: try optionalString(
                            callObject["command"], path: "\(callPath)/command", max: maxToolField
                        ),
                        path: try optionalString(
                            callObject["path"], path: "\(callPath)/path", max: maxToolField
                        )
                    )
                }
            }

            return SpoolMessage(id: id, role: role, text: text, at: at, toolCalls: toolCalls)
        }
    }

    private static func parseMetadata(_ value: Any?) throws -> SpoolMetadata {
        guard let object = value as? [String: Any] else {
            throw SpoolBundleError.invalid("/metadata: ожидается объект")
        }
        try checkKeys(object, allowed: metadataKeys, required: [], path: "/metadata")
        return SpoolMetadata(
            agent: try optionalString(object["agent"], path: "/metadata/agent", max: 100),
            model: try optionalString(object["model"], path: "/metadata/model", max: maxMetadataField),
            branch: try optionalString(object["branch"], path: "/metadata/branch", max: maxMetadataField),
            opencodeVersion: try optionalString(
                object["opencode_version"], path: "/metadata/opencode_version", max: 100
            )
        )
    }

    // MARK: - Primitives

    private static func checkKeys(
        _ object: [String: Any],
        allowed: Set<String>,
        required: Set<String>,
        path: String
    ) throws {
        if let unknown = Set(object.keys).subtracting(allowed).sorted().first {
            throw SpoolBundleError.invalid("\(path): неизвестное поле \(unknown)")
        }
        if let missing = required.subtracting(object.keys).sorted().first {
            throw SpoolBundleError.invalid("\(path): отсутствует поле \(missing)")
        }
    }

    private static func string(
        _ value: Any?,
        path: String,
        max: Int,
        allowEmpty: Bool = false
    ) throws -> String {
        guard let string = value as? String else {
            throw SpoolBundleError.invalid("\(path): ожидается строка")
        }
        if !allowEmpty && string.isEmpty {
            throw SpoolBundleError.invalid("\(path): пустая строка")
        }
        guard string.count <= max else {
            throw SpoolBundleError.invalid("\(path): максимум \(max) символов")
        }
        return string
    }

    private static func optionalString(_ value: Any?, path: String, max: Int) throws -> String? {
        guard let value, !(value is NSNull) else { return nil }
        return try string(value, path: path, max: max, allowEmpty: true)
    }

    private static func bool(_ value: Any?, path: String) throws -> Bool {
        guard let number = value as? NSNumber,
              CFGetTypeID(number) == CFBooleanGetTypeID() else {
            throw SpoolBundleError.invalid("\(path): ожидается boolean")
        }
        return number.boolValue
    }

    private static func int(_ value: Any?, path: String) throws -> Int {
        guard let number = value as? NSNumber,
              CFGetTypeID(number) != CFBooleanGetTypeID(),
              number.doubleValue.rounded() == number.doubleValue else {
            throw SpoolBundleError.invalid("\(path): ожидается целое число")
        }
        return number.intValue
    }

    private static func dateString(_ value: Any?, path: String) throws -> String {
        let string = try string(value, path: path, max: 100)
        guard parseDate(string) != nil else {
            throw SpoolBundleError.invalid("\(path): ожидается RFC 3339")
        }
        return string
    }

    /// RFC 3339 permits lowercase `t`/`z`; `APIDate` only handles uppercase.
    static func parseDate(_ string: String) -> Date? {
        if let date = APIDate.parse(string) { return date }
        let normalized = string
            .replacingOccurrences(of: "t", with: "T")
            .replacingOccurrences(of: "z", with: "Z")
        return APIDate.parse(normalized)
    }
}
