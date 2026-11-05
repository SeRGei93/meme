import Foundation

// MARK: - Health

struct Health: Codable, Equatable, Sendable {
    struct Database: Codable, Equatable, Sendable {
        let status: String
        let fts5: Bool
        let error: String?
    }

    let status: String
    let version: String
    let schemaVersion: Int
    let time: Date
    let database: Database

    enum CodingKeys: String, CodingKey {
        case status
        case version
        case schemaVersion = "schema_version"
        case time
        case database
    }
}

// MARK: - Cards and records

struct Card: Codable, Equatable, Sendable, Identifiable {
    let id: Int64
    let source: String
    let kind: String
    let projectKey: String
    let title: String
    let summary: String
    let questions: [String]
    let keywords: [String]
    let atoms: [String]
    let isFinal: Bool
    let startedAt: Date?
    let updatedAt: Date
    let finalizedAt: Date?
    let cardStatus: String
    let cardModel: String?
    /// Present only in search results.
    let score: Double?

    enum CodingKeys: String, CodingKey {
        case id
        case source
        case kind
        case projectKey = "project_key"
        case title
        case summary
        case questions
        case keywords
        case atoms
        case isFinal = "is_final"
        case startedAt = "started_at"
        case updatedAt = "updated_at"
        case finalizedAt = "finalized_at"
        case cardStatus = "card_status"
        case cardModel = "card_model"
        case score
    }
}

/// A full record, including the raw text. Flattens the `allOf` shape of the
/// OpenAPI `Record` schema into a card plus the record-only fields.
struct Record: Codable, Equatable, Sendable {
    let card: Card
    let rawText: String
    let revision: Int
    let metadata: [String: JSONValue]
    let indexedAt: Date?

    init(from decoder: Decoder) throws {
        card = try Card(from: decoder)
        let container = try decoder.container(keyedBy: CodingKeys.self)
        rawText = try container.decode(String.self, forKey: .rawText)
        revision = try container.decode(Int.self, forKey: .revision)
        metadata = try container.decodeIfPresent([String: JSONValue].self, forKey: .metadata) ?? [:]
        indexedAt = try container.decodeIfPresent(Date.self, forKey: .indexedAt)
    }

    func encode(to encoder: Encoder) throws {
        try card.encode(to: encoder)
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(rawText, forKey: .rawText)
        try container.encode(revision, forKey: .revision)
        try container.encode(metadata, forKey: .metadata)
        try container.encodeIfPresent(indexedAt, forKey: .indexedAt)
    }

    enum CodingKeys: String, CodingKey {
        case rawText = "raw_text"
        case revision
        case metadata
        case indexedAt = "indexed_at"
    }
}

struct SearchResponse: Codable, Equatable, Sendable {
    let items: [Card]
    let total: Int
    let query: String
    let limit: Int
}

// MARK: - Ingest

struct IngestResponse: Codable, Equatable, Sendable {
    let recordID: Int64
    let jobID: Int64?
    let status: String
    let replayed: Bool

    enum CodingKeys: String, CodingKey {
        case recordID = "record_id"
        case jobID = "job_id"
        case status
        case replayed
    }
}

// MARK: - Projects

struct Project: Codable, Equatable, Sendable, Identifiable {
    var id: String { key }

    let key: String
    let name: String
    let path: String
    let recordCount: Int
    let lastActivityAt: Date?
    let createdAt: Date

    enum CodingKeys: String, CodingKey {
        case key
        case name
        case path
        case recordCount = "record_count"
        case lastActivityAt = "last_activity_at"
        case createdAt = "created_at"
    }
}

struct ProjectList: Codable, Equatable, Sendable {
    let items: [Project]
}

// MARK: - Stats

struct Stats: Codable, Equatable, Sendable {
    struct Records: Codable, Equatable, Sendable {
        struct SourceCounts: Codable, Equatable, Sendable {
            let opencode: Int
            let jarvis: Int
        }

        struct CardStatusCounts: Codable, Equatable, Sendable {
            let heuristic: Int
            let queued: Int
            let ok: Int
            let failed: Int
        }

        let total: Int
        let final: Int
        let draft: Int
        let bySource: SourceCounts
        let byCardStatus: CardStatusCounts

        enum CodingKeys: String, CodingKey {
            case total
            case final
            case draft
            case bySource = "by_source"
            case byCardStatus = "by_card_status"
        }
    }

    struct Jobs: Codable, Equatable, Sendable {
        let queued: Int
        let running: Int
        let done: Int
        let failed: Int
    }

    struct Database: Codable, Equatable, Sendable {
        let status: String
        let schemaVersion: Int
        let sizeBytes: Int

        enum CodingKeys: String, CodingKey {
            case status
            case schemaVersion = "schema_version"
            case sizeBytes = "size_bytes"
        }
    }

    let records: Records
    let projects: Int
    let clients: Int
    let jobs: Jobs
    let lastIngestAt: Date?
    let database: Database

    enum CodingKeys: String, CodingKey {
        case records
        case projects
        case clients
        case jobs
        case lastIngestAt = "last_ingest_at"
        case database
    }
}


// MARK: - Clients

enum ClientKind: String, Codable, Equatable, Sendable {
    case opencode
    case jarvis
    case ui
    case admin
}

enum Scope: String, Codable, Equatable, Sendable, CaseIterable {
    case ingest
    case mcpRead = "mcp:read"
    case mcpWrite = "mcp:write"
    case ui
    case admin
}

struct ClientInfo: Codable, Equatable, Sendable, Identifiable {
    let id: Int64
    let name: String
    let kind: String
    let scopes: [String]
    let source: String?
    let createdAt: Date
    let lastSeenAt: Date?
    let revokedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case kind
        case scopes
        case source
        case createdAt = "created_at"
        case lastSeenAt = "last_seen_at"
        case revokedAt = "revoked_at"
    }
}

struct ClientList: Codable, Equatable, Sendable {
    let items: [ClientInfo]
}

struct CreateClientRequest: Codable, Equatable, Sendable {
    let name: String
    let kind: ClientKind
    let scopes: [Scope]
}

struct CreateClientResponse: Codable, Equatable, Sendable {
    let client: ClientInfo
    let token: String
}

// MARK: - Loose JSON

/// Arbitrary JSON value used for fields such as `metadata` that the server
/// passes through unchanged.
enum JSONValue: Codable, Equatable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }
}
