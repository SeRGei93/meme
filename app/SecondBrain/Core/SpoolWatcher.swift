import Foundation

enum SpoolWatcherError: Error, Equatable {
    case notDirectory(String)
    case cannotOpen(String, Int32)
}

extension SpoolWatcherError: LocalizedError {
    var errorDescription: String? {
        switch self {
        case .notDirectory(let path):
            return "Spool не является каталогом: \(path)"
        case .cannotOpen(let path, let code):
            return "Не удалось следить за \(path): \(String(cString: strerror(code)))"
        }
    }
}

/// Watches the spool directory with a dispatch file-system source. Events are
/// coalesced by the queue; the watcher only reports that something changed.
/// Bundles are published by an atomic `*.tmp -> *.json` rename, so a `.tmp`
/// file is never visible to the uploader.
final class SpoolWatcher: @unchecked Sendable {
    let directory: URL
    private let onChange: @Sendable () -> Void
    private let queue = DispatchQueue(label: "com.serg.secondbrain.spool-watcher")
    private let fileManager: FileManager
    private var source: DispatchSourceFileSystemObject?
    private var stopped = true

    init(directory: URL, fileManager: FileManager = .default, onChange: @escaping @Sendable () -> Void) {
        self.directory = directory
        self.fileManager = fileManager
        self.onChange = onChange
    }

    deinit {
        source?.cancel()
    }

    func start() throws {
        try queue.sync {
            stopped = false
            try arm()
        }

        // Pick up bundles that were written while the app was not running.
        notifyIfBundlesExist(force: true)
    }

    func stop() {
        queue.sync {
            stopped = true
            source?.cancel()
            source = nil
        }
    }

    /// Opens the directory again after it was removed or renamed. Without this
    /// the file descriptor would point at an unlinked inode and events would
    /// silently stop for the rest of the process lifetime.
    private func arm() throws {
        guard !stopped, source == nil else { return }

        var isDirectory: ObjCBool = false
        if fileManager.fileExists(atPath: directory.path, isDirectory: &isDirectory) {
            guard isDirectory.boolValue else {
                throw SpoolWatcherError.notDirectory(directory.path)
            }
        } else {
            try fileManager.createDirectory(
                at: directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
        }

        let fd = open(directory.path, O_EVTONLY)
        guard fd >= 0 else {
            throw SpoolWatcherError.cannotOpen(directory.path, errno)
        }

        let source = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd,
            eventMask: [.write, .rename, .delete, .extend, .attrib],
            queue: queue
        )
        source.setEventHandler { [weak self] in
            self?.handleEvent()
        }
        source.setCancelHandler {
            close(fd)
        }
        self.source = source
        source.resume()
    }

    private func handleEvent() {
        var isDirectory: ObjCBool = false
        if !fileManager.fileExists(atPath: directory.path, isDirectory: &isDirectory)
            || !isDirectory.boolValue {
            source?.cancel()
            source = nil
            queue.asyncAfter(deadline: .now() + 0.2) { [weak self] in
                guard let self, !self.stopped else { return }
                do {
                    try self.arm()
                    self.notifyIfBundlesExist(force: true)
                } catch {
                    // Keep trying on the next event; the app shows the error
                    // through the queue only when the directory is usable.
                }
            }
            return
        }
        notifyIfBundlesExist()
    }

    /// Scans the directory immediately. Used by `start()` and tests.
    func scanNow() {
        notifyIfBundlesExist(force: true)
    }

    static func bundleFiles(in directory: URL) throws -> [URL] {
        let keys: [URLResourceKey] = [.isRegularFileKey]
        let urls = try FileManager.default.contentsOfDirectory(
            at: directory,
            includingPropertiesForKeys: keys,
            options: [.skipsHiddenFiles]
        )
        return urls.filter { url in
            guard url.pathExtension.lowercased() == "json" else { return false }
            let values = try? url.resourceValues(forKeys: Set(keys))
            return values?.isRegularFile == true
        }
        .sorted { $0.lastPathComponent < $1.lastPathComponent }
    }

    private func notifyIfBundlesExist(force: Bool = false) {
        guard let files = try? Self.bundleFiles(in: directory) else {
            if force { onChange() }
            return
        }
        if force || !files.isEmpty {
            onChange()
        }
    }
}
