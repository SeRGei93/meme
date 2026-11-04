import Foundation

enum MCPTokenFileError: Error, Equatable {
    case writeFailed
}

extension MCPTokenFileError: LocalizedError {
    var errorDescription: String? { "Не удалось записать mcp_token" }
}

/// `~/.config/second-brain/mcp_token` is read by OpenCode, which cannot use the
/// Keychain. The token is written for the current user only (`0600`), atomically.
struct MCPTokenFile {
    let fileURL: URL
    private let fileManager: FileManager

    init(directory: URL = ConfigStore.defaultDirectory(), fileManager: FileManager = .default) {
        fileURL = directory.appendingPathComponent("mcp_token", isDirectory: false)
        self.fileManager = fileManager
    }

    func write(_ token: String) throws {
        let directory = fileURL.deletingLastPathComponent()
        do {
            if !fileManager.fileExists(atPath: directory.path) {
                try fileManager.createDirectory(
                    at: directory,
                    withIntermediateDirectories: true,
                    attributes: [.posixPermissions: 0o700]
                )
            }

            let temporaryURL = directory.appendingPathComponent("mcp_token.tmp", isDirectory: false)
            try? fileManager.removeItem(at: temporaryURL)
            guard fileManager.createFile(
                atPath: temporaryURL.path,
                contents: Data(token.utf8),
                attributes: [.posixPermissions: 0o600]
            ) else {
                throw MCPTokenFileError.writeFailed
            }

            if fileManager.fileExists(atPath: fileURL.path) {
                _ = try fileManager.replaceItemAt(fileURL, withItemAt: temporaryURL)
            } else {
                try fileManager.moveItem(at: temporaryURL, to: fileURL)
            }
            try? fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: fileURL.path)
        } catch let error as MCPTokenFileError {
            throw error
        } catch {
            throw MCPTokenFileError.writeFailed
        }
    }

    func read() throws -> String? {
        guard fileManager.fileExists(atPath: fileURL.path) else {
            return nil
        }
        return try String(contentsOf: fileURL, encoding: .utf8)
    }
}
