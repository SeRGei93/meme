import Foundation

enum OpenCodeConfigState: Equatable, Sendable {
    case configured
    case notConfigured
    case error(String)
}

struct OpenCodeConfigStatus: Equatable, Sendable {
    let state: OpenCodeConfigState
    let fileURL: URL?

    var isConfigured: Bool { state == .configured }
}

enum OpenCodeConfigError: Error, Equatable {
    case invalidConfiguration(String)
    case writeFailed(String)
    case verificationFailed
}

extension OpenCodeConfigError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .invalidConfiguration(let detail):
            return "Не удалось разобрать конфигурацию OpenCode: \(detail)"
        case .writeFailed(let detail):
            return "Не удалось обновить opencode.json: \(detail)"
        case .verificationFailed:
            return "opencode.json записан, но проверка MCP-сервера не прошла"
        }
    }
}

/// Owns the `mcp.servers.second-brain` entry in the global OpenCode config.
///
/// The file belongs to the user and may be JSONC with comments and trailing
/// commas, so the service parses it leniently and then rewrites valid JSON while
/// preserving everything else. The bearer token itself never enters the file:
/// OpenCode substitutes it from `mcp_token` through `{file:...}`.
struct OpenCodeConfig {
    static let serverName = "second-brain"

    let directory: URL
    private let fileManager: FileManager

    init(
        directory: URL = OpenCodePaths.defaultDirectory(),
        fileManager: FileManager = .default
    ) {
        self.directory = directory
        self.fileManager = fileManager
    }

    var defaultFileURL: URL {
        directory.appendingPathComponent("opencode.json", isDirectory: false)
    }

    func existingFileURL() -> URL? {
        let json = defaultFileURL
        if fileManager.fileExists(atPath: json.path) {
            return json
        }
        let jsonc = directory.appendingPathComponent("opencode.jsonc", isDirectory: false)
        if fileManager.fileExists(atPath: jsonc.path) {
            return jsonc
        }
        return nil
    }

    func status(serverURL: URL?, tokenPath: String) -> OpenCodeConfigStatus {
        guard let serverURL else {
            return OpenCodeConfigStatus(state: .notConfigured, fileURL: existingFileURL())
        }
        guard let fileURL = existingFileURL() else {
            return OpenCodeConfigStatus(state: .notConfigured, fileURL: nil)
        }

        do {
            let object = try JSONC.object(from: Data(contentsOf: fileURL))
            let expected = expectations(serverURL: serverURL, tokenPath: tokenPath)
            return OpenCodeConfigStatus(
                state: matches(object, expected: expected) ? .configured : .notConfigured,
                fileURL: fileURL
            )
        } catch {
            return OpenCodeConfigStatus(
                state: .error("\(fileURL.path): \(error.localizedDescription)"),
                fileURL: fileURL
            )
        }
    }

    @discardableResult
    func configure(serverURL: URL, tokenPath: String) throws -> URL {
        guard !tokenPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw OpenCodeConfigError.invalidConfiguration("пустой путь к mcp_token")
        }

        let fileURL = existingFileURL() ?? defaultFileURL
        // Do not rewrite an already-correct file: this preserves JSONC comments
        // and avoids clobbering a concurrent edit from OpenCode on every connect.
        if existingFileURL() != nil,
           status(serverURL: serverURL, tokenPath: tokenPath).state == .configured {
            return fileURL
        }

        var object: [String: Any] = [:]

        if fileManager.fileExists(atPath: fileURL.path) {
            do {
                object = try JSONC.object(from: Data(contentsOf: fileURL))
            } catch let error as OpenCodeConfigError {
                throw error
            } catch {
                throw OpenCodeConfigError.invalidConfiguration(
                    "\(fileURL.path): \(error.localizedDescription)"
                )
            }
        }

        object = merging(
            object,
            expected: expectations(serverURL: serverURL, tokenPath: tokenPath)
        )

        let data: Data
        do {
            var encoded = try JSONSerialization.data(
                withJSONObject: object,
                options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
            )
            encoded.append(0x0A)
            data = encoded
        } catch {
            throw OpenCodeConfigError.writeFailed(error.localizedDescription)
        }

        try writeAtomically(data, to: fileURL)

        guard status(serverURL: serverURL, tokenPath: tokenPath).state == .configured else {
            throw OpenCodeConfigError.verificationFailed
        }
        return fileURL
    }

    // MARK: - Merging

    private struct Expectations {
        let url: String
        let authorization: String
    }

    private func expectations(serverURL: URL, tokenPath: String) -> Expectations {
        Expectations(
            url: serverURL.appendingPathComponent("mcp", isDirectory: false).absoluteString,
            authorization: "Bearer {file:\(tokenPath)}"
        )
    }

    private func matches(_ object: [String: Any], expected: Expectations) -> Bool {
        guard let mcp = object["mcp"] as? [String: Any],
              let servers = mcp["servers"] as? [String: Any],
              let entry = servers[Self.serverName] as? [String: Any],
              entry["type"] as? String == "remote",
              entry["url"] as? String == expected.url,
              entry["oauth"] as? Bool == false,
              entry["codemode"] as? Bool == false,
              let headers = entry["headers"] as? [String: Any],
              headers["Authorization"] as? String == expected.authorization else {
            return false
        }
        if entry["enabled"] != nil {
            return false
        }
        if let disabled = entry["disabled"], !(disabled is Bool) {
            return false
        }
        if let timeout = entry["timeout"] {
            guard let object = timeout as? [String: Any], isSupportedTimeout(object) else {
                return false
            }
        }
        return true
    }

    /// OpenCode V2 accepts only these timeout keys, all numeric.
    private func isSupportedTimeout(_ timeout: [String: Any]) -> Bool {
        let allowed: Set<String> = ["startup", "catalog", "execution"]
        return timeout.keys.allSatisfy(allowed.contains)
            && timeout.values.allSatisfy { $0 is NSNumber }
    }

    private func merging(_ object: [String: Any], expected: Expectations) -> [String: Any] {
        var object = object
        var mcp = object["mcp"] as? [String: Any] ?? [:]
        var servers = mcp["servers"] as? [String: Any] ?? [:]
        let previous = servers[Self.serverName] as? [String: Any] ?? [:]

        // The entry is app-owned. Rebuild it from fields OpenCode V2 accepts and
        // keep only user headers and a structured timeout; a stale V1 scalar
        // timeout or unknown keys would make the config schema-invalid.
        var entry: [String: Any] = [:]
        if let headers = previous["headers"] as? [String: Any] {
            entry["headers"] = headers
        }
        if let timeout = previous["timeout"] as? [String: Any], isSupportedTimeout(timeout) {
            entry["timeout"] = timeout
        }
        if let disabled = previous["disabled"] as? Bool {
            entry["disabled"] = disabled
        }
        if let protocolValue = previous["protocol"] as? String {
            entry["protocol"] = protocolValue
        }
        var headers = entry["headers"] as? [String: Any] ?? [:]
        headers["Authorization"] = expected.authorization
        entry["headers"] = headers
        entry["type"] = "remote"
        entry["url"] = expected.url
        entry["oauth"] = false
        entry["codemode"] = false
        servers[Self.serverName] = entry
        mcp["servers"] = servers
        object["mcp"] = mcp
        return object
    }

    // MARK: - Writing

    private func writeAtomically(_ data: Data, to fileURL: URL) throws {
        // `opencode.json` is often a dotfile symlink (stow/chezmoi). Rewrite the
        // target and leave the link in place instead of replacing the link.
        let target = fileURL.resolvingSymlinksInPath()
        let targetDirectory = target.deletingLastPathComponent()

        do {
            if !fileManager.fileExists(atPath: targetDirectory.path) {
                try fileManager.createDirectory(
                    at: targetDirectory,
                    withIntermediateDirectories: true,
                    attributes: [.posixPermissions: 0o755]
                )
            }
        } catch {
            throw OpenCodeConfigError.writeFailed(
                map(error, path: targetDirectory.path).localizedDescription
            )
        }

        let existingMode = (try? fileManager.attributesOfItem(atPath: target.path))?[.posixPermissions]
            as? NSNumber
        let mode = existingMode?.intValue ?? 0o600
        let temporaryURL = targetDirectory.appendingPathComponent(
            ".opencode.json.\(UUID().uuidString).tmp",
            isDirectory: false
        )

        guard fileManager.createFile(
            atPath: temporaryURL.path,
            contents: data,
            attributes: [.posixPermissions: 0o600]
        ) else {
            throw OpenCodeConfigError.writeFailed(fileURL.path)
        }

        do {
            if fileManager.fileExists(atPath: target.path) {
                _ = try fileManager.replaceItemAt(target, withItemAt: temporaryURL)
            } else {
                try fileManager.moveItem(at: temporaryURL, to: target)
            }
            try fileManager.setAttributes([.posixPermissions: mode], ofItemAtPath: target.path)
        } catch {
            try? fileManager.removeItem(at: temporaryURL)
            throw OpenCodeConfigError.writeFailed(map(error, path: target.path).localizedDescription)
        }
    }

    private func map(_ error: Error, path: String) -> OpenCodeConfigError {
        .writeFailed("\(path): \(error.localizedDescription)")
    }
}

// MARK: - JSONC

private enum JSONCError: Error, Equatable, LocalizedError {
    case unterminatedString
    case unterminatedComment
    case notAnObject

    var errorDescription: String? {
        switch self {
        case .unterminatedString:
            return "незакрытая строка"
        case .unterminatedComment:
            return "незакрытый комментарий"
        case .notAnObject:
            return "корневой элемент должен быть объектом"
        }
    }
}

private enum JSONC {
    static func object(from data: Data) throws -> [String: Any] {
        guard var text = String(data: data, encoding: .utf8) else {
            throw JSONCError.notAnObject
        }
        if text.hasPrefix("\u{FEFF}") {
            text.removeFirst()
        }
        if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return [:]
        }

        let withoutComments = try stripComments(text)
        let withoutTrailingCommas = try stripTrailingCommas(withoutComments)
        let object = try JSONSerialization.jsonObject(with: Data(withoutTrailingCommas.utf8))
        guard let dictionary = object as? [String: Any] else {
            throw JSONCError.notAnObject
        }
        return dictionary
    }

    /// Removes `//` and `/* */` comments without touching comment-like text
    /// inside strings.
    private static func stripComments(_ text: String) throws -> String {
        var result = ""
        var index = text.startIndex
        var inString = false
        var escaped = false

        while index < text.endIndex {
            let character = text[index]

            if inString {
                result.append(character)
                if escaped {
                    escaped = false
                } else if character == "\\" {
                    escaped = true
                } else if character == "\"" {
                    inString = false
                }
                index = text.index(after: index)
                continue
            }

            if character == "\"" {
                inString = true
                result.append(character)
                index = text.index(after: index)
                continue
            }

            if character == "/" {
                let next = text.index(after: index)
                if next < text.endIndex, text[next] == "/" {
                    var cursor = next
                    while cursor < text.endIndex,
                          text[cursor] != "\n",
                          text[cursor] != "\r" {
                        cursor = text.index(after: cursor)
                    }
                    index = cursor
                    continue
                }
                if next < text.endIndex, text[next] == "*" {
                    var cursor = text.index(after: next)
                    var closed = false
                    while cursor < text.endIndex {
                        let after = text.index(after: cursor)
                        if text[cursor] == "*", after < text.endIndex, text[after] == "/" {
                            cursor = text.index(after: after)
                            closed = true
                            break
                        }
                        cursor = after
                    }
                    guard closed else { throw JSONCError.unterminatedComment }
                    index = cursor
                    continue
                }
            }

            result.append(character)
            index = text.index(after: index)
        }

        guard !inString else { throw JSONCError.unterminatedString }
        return result
    }

    /// JSONC allows a comma before a closing brace or bracket.
    private static func stripTrailingCommas(_ text: String) throws -> String {
        var result = ""
        var index = text.startIndex
        var inString = false
        var escaped = false

        while index < text.endIndex {
            let character = text[index]

            if inString {
                result.append(character)
                if escaped {
                    escaped = false
                } else if character == "\\" {
                    escaped = true
                } else if character == "\"" {
                    inString = false
                }
                index = text.index(after: index)
                continue
            }

            if character == "\"" {
                inString = true
                result.append(character)
                index = text.index(after: index)
                continue
            }

            if character == "," {
                var cursor = text.index(after: index)
                while cursor < text.endIndex, text[cursor].isWhitespace {
                    cursor = text.index(after: cursor)
                }
                if cursor < text.endIndex, text[cursor] == "}" || text[cursor] == "]" {
                    index = text.index(after: index)
                    continue
                }
            }

            result.append(character)
            index = text.index(after: index)
        }

        guard !inString else { throw JSONCError.unterminatedString }
        return result
    }
}
