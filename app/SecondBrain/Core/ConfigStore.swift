import Foundation

/// The subset of `~/.config/second-brain/config.json` the app owns. The file is
/// shared with the OpenCode plugin, so keys stay snake_case and saving merges
/// into the existing JSON instead of replacing it.
struct AppConfig: Codable, Equatable, Sendable {
    /// `nil` until onboarding has connected to a server. The plugin-only file has
    /// no `server_url` yet and must still load.
    var serverURL: URL?
    var spoolDir: String?
    var idleDebounceMilliseconds: Int?
    var maxMessageChars: Int?
    var maxMessages: Int?
    var maxToolCallsPerMessage: Int?
    var maxToolFieldChars: Int?

    init(
        serverURL: URL? = nil,
        spoolDir: String? = nil,
        idleDebounceMilliseconds: Int? = nil,
        maxMessageChars: Int? = nil,
        maxMessages: Int? = nil,
        maxToolCallsPerMessage: Int? = nil,
        maxToolFieldChars: Int? = nil
    ) {
        self.serverURL = serverURL
        self.spoolDir = spoolDir
        self.idleDebounceMilliseconds = idleDebounceMilliseconds
        self.maxMessageChars = maxMessageChars
        self.maxMessages = maxMessages
        self.maxToolCallsPerMessage = maxToolCallsPerMessage
        self.maxToolFieldChars = maxToolFieldChars
    }

    enum CodingKeys: String, CodingKey {
        case serverURL = "server_url"
        case spoolDir = "spool_dir"
        case idleDebounceMilliseconds = "idle_debounce_ms"
        case maxMessageChars = "max_message_chars"
        case maxMessages = "max_messages"
        case maxToolCallsPerMessage = "max_tool_calls_per_message"
        case maxToolFieldChars = "max_tool_field_chars"
    }
}

enum ConfigStoreError: Error, Equatable {
    case invalidConfig
    case writeFailed
}

extension ConfigStoreError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .invalidConfig: return "Файл config.json повреждён"
        case .writeFailed: return "Не удалось сохранить config.json"
        }
    }
}

/// Reads and writes `config.json`. Writes are atomic, the directory is private
/// (`0700`) and the file is only readable by the current user (`0600`).
struct ConfigStore {
    let directory: URL
    private let fileManager: FileManager

    var fileURL: URL {
        directory.appendingPathComponent("config.json", isDirectory: false)
    }

    init(directory: URL = ConfigStore.defaultDirectory(), fileManager: FileManager = .default) {
        self.directory = directory
        self.fileManager = fileManager
    }

    static func defaultDirectory(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        homeDirectory: String = NSHomeDirectory()
    ) -> URL {
        if let override = environment["SECOND_BRAIN_CONFIG_DIR"], !override.isEmpty {
            return URL(fileURLWithPath: override, isDirectory: true)
        }

        let base: URL
        if let xdg = environment["XDG_CONFIG_HOME"], !xdg.isEmpty {
            base = URL(fileURLWithPath: xdg, isDirectory: true)
        } else {
            base = URL(fileURLWithPath: homeDirectory, isDirectory: true)
                .appendingPathComponent(".config", isDirectory: true)
        }
        return base.appendingPathComponent("second-brain", isDirectory: true)
    }

    func load() throws -> AppConfig? {
        guard fileManager.fileExists(atPath: fileURL.path) else {
            return nil
        }
        do {
            let data = try Data(contentsOf: fileURL)
            return try JSONDecoder().decode(AppConfig.self, from: data)
        } catch {
            throw ConfigStoreError.invalidConfig
        }
    }

    /// Writes the app-owned keys, preserving every other key already present in
    /// the shared file (plugin settings, fields added by newer versions).
    func save(_ config: AppConfig) throws {
        do {
            try ensureDirectory()
            let encoded = try JSONEncoder().encode(config)
            var object = existingObject() ?? [:]
            if let patch = try JSONSerialization.jsonObject(with: encoded) as? [String: Any] {
                object.merge(patch) { _, new in new }
            }
            let data = try JSONSerialization.data(
                withJSONObject: object,
                options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
            )
            try writeAtomically(data)
        } catch let error as ConfigStoreError {
            throw error
        } catch {
            throw ConfigStoreError.writeFailed
        }
    }

    private func ensureDirectory() throws {
        if !fileManager.fileExists(atPath: directory.path) {
            try fileManager.createDirectory(
                at: directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
        } else {
            try? fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        }
    }

    private func existingObject() -> [String: Any]? {
        guard let data = try? Data(contentsOf: fileURL) else { return nil }
        return try? JSONSerialization.jsonObject(with: data) as? [String: Any]
    }

    private func writeAtomically(_ data: Data) throws {
        let temporaryURL = directory.appendingPathComponent("config.json.tmp", isDirectory: false)
        try? fileManager.removeItem(at: temporaryURL)

        // Create the temp file with the final mode, so the rename never exposes
        // a world-readable config.json.
        guard fileManager.createFile(
            atPath: temporaryURL.path,
            contents: data,
            attributes: [.posixPermissions: 0o600]
        ) else {
            throw ConfigStoreError.writeFailed
        }

        if fileManager.fileExists(atPath: fileURL.path) {
            _ = try fileManager.replaceItemAt(fileURL, withItemAt: temporaryURL)
        } else {
            try fileManager.moveItem(at: temporaryURL, to: fileURL)
        }
        try? fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fileURL.path)
    }
}
