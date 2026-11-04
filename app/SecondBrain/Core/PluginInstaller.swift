import Foundation

enum PluginInstallState: Equatable, Sendable {
    case installed
    case notInstalled
    case outdated
    case error(String)
}

struct PluginInstallStatus: Equatable, Sendable {
    let state: PluginInstallState
    let directory: URL
}

enum PluginInstallError: Error, Equatable {
    case sourceUnavailable
    case missingSourceFiles([String])
    case permissionDenied(String)
    case pathOccupied(String)
    case installFailed(String)
}

extension PluginInstallError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .sourceUnavailable:
            return "В приложении нет файлов плагина OpenCode. Пересоберите SecondBrain.app"
        case .missingSourceFiles(let files):
            return "В комплекте плагина нет: \(files.joined(separator: ", "))"
        case .permissionDenied(let path):
            return "Нет прав на запись в \(path). Проверьте владельца каталога ~/.config/opencode"
        case .pathOccupied(let path):
            return "Путь занят файлом: \(path). Уберите файл или укажите другой каталог OpenCode"
        case .installFailed(let detail):
            return "Не удалось установить плагин: \(detail)"
        }
    }
}

/// Copies the bundled OpenCode plugin into
/// `~/.config/opencode/plugins/second-brain/`, where OpenCode discovers global
/// plugins automatically. A reinstall swaps the directory atomically, so a
/// failure never leaves a half-copied plugin behind.
struct PluginInstaller {
    static let pluginName = "second-brain"
    static let bundledDirectoryName = "SecondBrainPlugin"
    static let requiredFiles = ["index.ts", "package.json"]

    let openCodeDirectory: URL
    let sourceDirectory: URL?
    private let fileManager: FileManager

    init(
        openCodeDirectory: URL = OpenCodePaths.defaultDirectory(),
        sourceDirectory: URL?,
        fileManager: FileManager = .default
    ) {
        self.openCodeDirectory = openCodeDirectory
        self.sourceDirectory = sourceDirectory
        self.fileManager = fileManager
    }

    var pluginsDirectory: URL {
        openCodeDirectory.appendingPathComponent("plugins", isDirectory: true)
    }

    var destination: URL {
        pluginsDirectory.appendingPathComponent(Self.pluginName, isDirectory: true)
    }

    static func defaultSourceDirectory(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        bundle: Bundle = .main
    ) -> URL? {
        if let override = environment["SECOND_BRAIN_PLUGIN_SOURCE"], !override.isEmpty {
            return URL(fileURLWithPath: override, isDirectory: true)
        }
        return bundle.resourceURL?
            .appendingPathComponent(bundledDirectoryName, isDirectory: true)
    }

    func status() -> PluginInstallStatus {
        do {
            let files = try sourceFiles()
            guard isDirectory(destination) else {
                return PluginInstallStatus(state: .notInstalled, directory: destination)
            }

            var outdated = false
            for file in files {
                let installed = destination.appendingPathComponent(file.name, isDirectory: false)
                guard let installedData = try? Data(contentsOf: installed) else {
                    return PluginInstallStatus(state: .notInstalled, directory: destination)
                }
                let sourceData = try Data(contentsOf: file.url)
                if installedData != sourceData {
                    outdated = true
                }
            }
            return PluginInstallStatus(
                state: outdated ? .outdated : .installed,
                directory: destination
            )
        } catch let error as PluginInstallError {
            return PluginInstallStatus(
                state: .error(error.errorDescription ?? "Ошибка установки плагина"),
                directory: destination
            )
        } catch {
            return PluginInstallStatus(
                state: .error(error.localizedDescription),
                directory: destination
            )
        }
    }

    @discardableResult
    func install() throws -> PluginInstallStatus {
        let files = try sourceFiles()
        try ensureDirectory(openCodeDirectory, mode: 0o755)
        try ensureDirectory(pluginsDirectory, mode: 0o755)
        sweepStagingArtifacts()

        // Stage next to `plugins/`, not inside it: OpenCode scans immediate
        // package directories there and must never see a half-written copy.
        let temporary = openCodeDirectory.appendingPathComponent(
            ".second-brain-\(UUID().uuidString)",
            isDirectory: true
        )
        defer { try? fileManager.removeItem(at: temporary) }

        do {
            try fileManager.createDirectory(
                at: temporary,
                withIntermediateDirectories: false,
                attributes: [.posixPermissions: 0o755]
            )
            try? fileManager.setAttributes([.posixPermissions: 0o755], ofItemAtPath: temporary.path)

            for file in files {
                let target = temporary.appendingPathComponent(file.name, isDirectory: false)
                try Data(contentsOf: file.url).write(to: target, options: .atomic)
                try? fileManager.setAttributes([.posixPermissions: 0o644], ofItemAtPath: target.path)
            }
        } catch {
            throw map(error, path: temporary.path)
        }

        let backup = openCodeDirectory.appendingPathComponent(
            ".second-brain-backup-\(UUID().uuidString)",
            isDirectory: true
        )
        let hadExisting = fileManager.fileExists(atPath: destination.path)

        do {
            if hadExisting {
                try fileManager.moveItem(at: destination, to: backup)
            }
            do {
                try fileManager.moveItem(at: temporary, to: destination)
            } catch {
                if hadExisting {
                    do {
                        try fileManager.moveItem(at: backup, to: destination)
                    } catch let restoreError {
                        throw PluginInstallError.installFailed(
                            "\(destination.path): \(error.localizedDescription); "
                                + "не удалось вернуть старую версию из \(backup.path): "
                                + restoreError.localizedDescription
                        )
                    }
                }
                throw map(error, path: destination.path)
            }
            if hadExisting {
                try? fileManager.removeItem(at: backup)
            }
        } catch let error as PluginInstallError {
            throw error
        } catch {
            throw map(error, path: destination.path)
        }

        return status()
    }

    /// Removes staging/backup directories left by an interrupted install. They
    /// live beside `plugins/`, so this never touches an installed plugin.
    private func sweepStagingArtifacts() {
        for parent in [openCodeDirectory, pluginsDirectory] {
            let names = (try? fileManager.contentsOfDirectory(atPath: parent.path)) ?? []
            for name in names where name.hasPrefix(".second-brain-") {
                try? fileManager.removeItem(
                    at: parent.appendingPathComponent(name, isDirectory: true)
                )
            }
        }
    }

    // MARK: - Files

    private struct SourceFile {
        let name: String
        let url: URL
    }

    private func sourceFiles() throws -> [SourceFile] {
        guard let sourceDirectory else {
            throw PluginInstallError.sourceUnavailable
        }
        guard isDirectory(sourceDirectory) else {
            throw PluginInstallError.sourceUnavailable
        }

        let names: [String]
        do {
            names = try fileManager.contentsOfDirectory(atPath: sourceDirectory.path)
        } catch {
            throw map(error, path: sourceDirectory.path)
        }

        let candidates = names
            .filter { name in
                name.hasSuffix(".ts") || name == "package.json" || name == "README.md"
            }
            .filter { name in
                isRegularFile(sourceDirectory.appendingPathComponent(name, isDirectory: false))
            }
            .sorted()

        let missing = Self.requiredFiles.filter { !candidates.contains($0) }
        if !missing.isEmpty {
            throw PluginInstallError.missingSourceFiles(missing)
        }

        return candidates.map {
            SourceFile(
                name: $0,
                url: sourceDirectory.appendingPathComponent($0, isDirectory: false)
            )
        }
    }

    private func ensureDirectory(_ url: URL, mode: Int) throws {
        var isDirectory: ObjCBool = false
        if fileManager.fileExists(atPath: url.path, isDirectory: &isDirectory) {
            guard isDirectory.boolValue else {
                throw PluginInstallError.pathOccupied(url.path)
            }
            return
        }

        do {
            try fileManager.createDirectory(
                at: url,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: mode]
            )
            try? fileManager.setAttributes([.posixPermissions: mode], ofItemAtPath: url.path)
        } catch {
            throw map(error, path: url.path)
        }
    }

    private func isDirectory(_ url: URL) -> Bool {
        var isDirectory: ObjCBool = false
        return fileManager.fileExists(atPath: url.path, isDirectory: &isDirectory)
            && isDirectory.boolValue
    }

    private func isRegularFile(_ url: URL) -> Bool {
        let attributes = try? fileManager.attributesOfItem(atPath: url.path)
        return attributes?[.type] as? FileAttributeType == .typeRegular
    }

    private func map(_ error: Error, path: String) -> PluginInstallError {
        let nsError = error as NSError
        switch nsError.code {
        case NSFileWriteNoPermissionError, NSFileReadNoPermissionError:
            return .permissionDenied(path)
        default:
            return .installFailed("\(path): \(error.localizedDescription)")
        }
    }
}
