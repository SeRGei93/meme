import Foundation

struct UploadRetryPolicy: Equatable, Sendable {
    let base: TimeInterval
    let cap: TimeInterval
    let maxAttempts: Int

    static let `default` = UploadRetryPolicy(base: 1, cap: 60, maxAttempts: 5)

    /// `docs/CONTRACTS.md` §5.2: full jitter, capped, and never earlier than
    /// `Retry-After` when the server sent one.
    func delay(attempt: Int, retryAfter: Int?, jitter: Double) -> TimeInterval {
        let exponential = min(cap, base * pow(2, Double(max(0, attempt))))
        let jittered = min(1, max(0, jitter)) * exponential
        let serverDelay = TimeInterval(max(0, retryAfter ?? 0))
        return max(jittered, serverDelay)
    }
}

/// Serial uploader for spool bundles.
///
/// The queue groups by `external_id`, uploads the newest bundle of a session,
/// deletes the whole group after success, retries transient failures with
/// exponential backoff and an idempotency key, and moves permanently invalid
/// bundles to `spool/failed/`. One bad session never blocks the others.
actor UploadQueue {
    typealias StatusHandler = @Sendable (UploadStatus) -> Void

    private let spoolDirectory: URL
    private let failedDirectory: URL
    private let retryPolicy: UploadRetryPolicy
    private let now: @Sendable () -> Date
    private let jitter: @Sendable () -> Double

    private var client: APIClient?
    private(set) var status = UploadStatus()
    private var statusHandler: StatusHandler?
    private var isDraining = false
    private var needsAnotherPass = false
    private var retryTask: Task<Void, Never>?
    private var attempts: [String: Int] = [:]

    init(
        spoolDirectory: URL,
        client: APIClient?,
        retryPolicy: UploadRetryPolicy = .default,
        now: @escaping @Sendable () -> Date = { Date() },
        jitter: @escaping @Sendable () -> Double = { Double.random(in: 0..<1) }
    ) {
        self.spoolDirectory = spoolDirectory
        failedDirectory = spoolDirectory.appendingPathComponent("failed", isDirectory: true)
        self.client = client
        self.retryPolicy = retryPolicy
        self.now = now
        self.jitter = jitter
    }

    func setStatusHandler(_ handler: @escaping StatusHandler) {
        statusHandler = handler
        handler(status)
    }

    /// A new bundle (or an explicit UI action) should not wait for the backoff
    /// timer: cancel it and try immediately.
    func trigger() async {
        guard !status.requiresAuthentication else { return }
        retryTask?.cancel()
        retryTask = nil
        status.nextRetryAt = nil
        await drain()
    }

    func retryNow() async {
        await trigger()
    }

    func updateClient(_ client: APIClient?) async {
        self.client = client
        attempts = [:]
        if client != nil {
            status.requiresAuthentication = false
            status.missingToken = false
            status.lastError = nil
            await trigger()
        } else {
            publish()
        }
    }

    func shutdown() async {
        retryTask?.cancel()
        retryTask = nil
        statusHandler = nil
    }

    func clearFailed() async {
        do {
            if FileManager.default.fileExists(atPath: failedDirectory.path) {
                try FileManager.default.removeItem(at: failedDirectory)
            }
            status.lastError = nil
        } catch {
            status.lastError = "Не удалось очистить failed: \(error.localizedDescription)"
        }
        refreshCounts()
        publish()
    }

    // MARK: - Draining

    private func drain() async {
        if isDraining {
            needsAnotherPass = true
            return
        }
        isDraining = true
        defer {
            isDraining = false
            status.isUploading = false
            refreshCounts()
            publish()
        }

        repeat {
            needsAnotherPass = false
            await performPass()
        } while needsAnotherPass && !status.requiresAuthentication
    }

    private func performPass() async {
        status.isUploading = true
        refreshCounts()
        publish()

        let files: [URL]
        do {
            files = try SpoolWatcher.bundleFiles(in: spoolDirectory)
        } catch {
            status.lastError = "Не удалось прочитать spool: \(error.localizedDescription)"
            return
        }
        guard !files.isEmpty else {
            // A clean pass clears a transient error, but never hides a
            // permanent failure that still has files in `failed/`.
            if status.failed == 0 {
                status.lastError = nil
            }
            return
        }

        var valid: [(url: URL, bundle: SpoolBundle)] = []
        for url in files {
            do {
                valid.append((url, try SpoolBundleLoader.load(from: url)))
            } catch {
                moveToFailed(url, error: error)
            }
        }
        refreshCounts()
        if valid.isEmpty {
            // Nothing needs the network, so a stale "no token" flag must not
            // hide the real per-file errors in the badge.
            status.missingToken = false
            return
        }

        // Validation is local, so an invalid bundle is quarantined even when
        // there is no token. Only a valid bundle needs the network.
        guard let client else {
            status.missingToken = true
            status.lastError = "Нет токена OpenCode. Запустите onboarding"
            return
        }
        status.missingToken = false

        let groups = Dictionary(grouping: valid) { $0.bundle.externalID }
        for group in groups.values.sorted(by: { lhs, rhs in
            (lhs.first?.bundle.externalID ?? "") < (rhs.first?.bundle.externalID ?? "")
        }) {
            let candidates = group.sorted { lhs, rhs in
                let left = lhs.bundle.updatedDate ?? .distantPast
                let right = rhs.bundle.updatedDate ?? .distantPast
                if left != right { return left > right }
                return lhs.url.lastPathComponent > rhs.url.lastPathComponent
            }

            for item in candidates {
                do {
                    _ = try await client.ingest(item.bundle)
                    let removed = delete(candidates.map(\.url))
                    attempts[item.bundle.bundleID] = nil
                    status.sent += 1
                    status.lastSuccessAt = now()
                    status.pending = max(0, status.pending - removed)
                    if status.failed == 0 {
                        status.lastError = nil
                    }
                    // A large spool may take a while; show progress per session
                    // without rescanning the whole directory for every bundle.
                    publish()
                    break
                } catch let error as APIError {
                    if error.requiresReauthentication {
                        status.requiresAuthentication = true
                        status.lastError = error.displayMessage
                        refreshCounts()
                        return
                    }
                    if Self.isRetryable(error) {
                        registerRetry(for: item.bundle, error: error)
                        refreshCounts()
                        return
                    }
                    // The newest bundle is authoritative. If the server
                    // rejects it permanently, do not let an older snapshot
                    // overwrite the record with stale content.
                    moveToFailed(item.url, error: error)
                    for older in candidates where older.url != item.url {
                        moveToFailed(older.url, error: error)
                    }
                    refreshCounts()
                    publish()
                    break
                } catch {
                    registerRetry(for: item.bundle, error: nil)
                    refreshCounts()
                    return
                }
            }
        }
    }

    // MARK: - Failures and retries

    private static func isRetryable(_ error: APIError) -> Bool {
        switch error {
        case .transport, .invalidResponse, .rateLimited:
            return true
        case .server(let status, _, _, _, _):
            return [408, 425, 500, 502, 503, 504].contains(status)
        case .unauthorized, .forbidden, .notFound, .invalidURL, .decoding:
            return false
        }
    }

    private func registerRetry(for bundle: SpoolBundle, error: APIError?) {
        let attempt = attempts[bundle.bundleID, default: 0]
        attempts[bundle.bundleID] = attempt + 1

        let retryAfter: Int?
        if case .rateLimited(let seconds, _) = error {
            retryAfter = seconds
        } else {
            retryAfter = nil
        }

        let delay: TimeInterval
        if attempt + 1 >= retryPolicy.maxAttempts {
            // Keep retrying, but in a fresh batch after the cap instead of
            // hammering a server that has been down for a while.
            attempts[bundle.bundleID] = 0
            delay = retryPolicy.cap
        } else {
            delay = retryPolicy.delay(attempt: attempt, retryAfter: retryAfter, jitter: jitter())
        }

        status.lastError = error?.displayMessage ?? "Не удалось отправить бандл"
        scheduleRetry(after: delay)
    }

    private func scheduleRetry(after delay: TimeInterval) {
        retryTask?.cancel()
        let delay = max(0, delay)
        status.nextRetryAt = now().addingTimeInterval(delay)
        let nanoseconds = UInt64(delay * 1_000_000_000)
        retryTask = Task { [weak self] in
            if nanoseconds > 0 {
                try? await Task.sleep(nanoseconds: nanoseconds)
            }
            guard !Task.isCancelled else { return }
            await self?.fireScheduledRetry()
        }
    }

    private func fireScheduledRetry() async {
        retryTask = nil
        status.nextRetryAt = nil
        await drain()
    }

    private func moveToFailed(_ url: URL, error: Error) {
        do {
            if !FileManager.default.fileExists(atPath: failedDirectory.path) {
                try FileManager.default.createDirectory(
                    at: failedDirectory,
                    withIntermediateDirectories: true,
                    attributes: [.posixPermissions: 0o700]
                )
            }
            var target = failedDirectory.appendingPathComponent(url.lastPathComponent)
            if FileManager.default.fileExists(atPath: target.path) {
                target = failedDirectory.appendingPathComponent(
                    "\(UUID().uuidString)-\(url.lastPathComponent)"
                )
            }
            try FileManager.default.moveItem(at: url, to: target)
        } catch {
            status.lastError = "Не удалось переместить бандл в failed: \(error.localizedDescription)"
            return
        }
        status.lastError = (error as? LocalizedError)?.errorDescription
            ?? error.localizedDescription
    }

    @discardableResult
    private func delete(_ urls: [URL]) -> Int {
        var removed = 0
        var firstError: String?
        for url in urls where FileManager.default.fileExists(atPath: url.path) {
            do {
                try FileManager.default.removeItem(at: url)
                removed += 1
            } catch {
                firstError = firstError ?? error.localizedDescription
            }
        }
        if let firstError {
            status.lastError = "Не удалось удалить отправленный бандл: \(firstError)"
        }
        return removed
    }

    private func refreshCounts() {
        do {
            status.pending = try SpoolWatcher.bundleFiles(in: spoolDirectory).count
        } catch {
            status.pending = 0
            status.lastError = "Не удалось прочитать spool: \(error.localizedDescription)"
        }
        // The failed directory is created lazily, so a missing directory is a
        // normal empty state rather than an error.
        status.failed = (try? SpoolWatcher.bundleFiles(in: failedDirectory).count) ?? 0
    }

    private func publish() {
        statusHandler?(status)
    }
}
