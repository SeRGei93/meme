import Foundation

/// The subset of `~/.config/second-brain/config.json` the app owns. The file is
/// shared with the OpenCode plugin, so keys stay snake_case.
struct AppConfig: Codable, Equatable, Sendable {
    var serverURL: URL
    var spoolDir: String?
    var idleDebounceMilliseconds: Int?
    var maxMessageChars: Int?
    var maxMessages: Int?
    var maxToolCallsPerMessage: Int?
    var maxToolFieldChars: Int?

    init(
        serverURL: URL,
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

    func save(_ config: AppConfig) throws {
        do {
            if !fileManager.fileExists(atPath: directory.path) {
                try fileManager.createDirectory(
                    at: directory,
                    withIntermediateDirectories: true,
                    attributes: [.posixPermissions: 0o700]
                )
            }

            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
            let data = try encoder.encode(config)
            try data.write(to: fileURL, options: .atomic)
            try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fileURL.path)
            try? fileManager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        } catch {
            throw ConfigStoreError.writeFailed
        }
    }
}
