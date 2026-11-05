import Foundation
import Observation

/// One source's server-side status. Token and plugin state are local and are
/// added by the view from `AppState`, keeping this model testable with stubs.
struct AgentSummary: Equatable, Sendable {
    let recordCount: Int
    let lastActivityAt: Date?
    let lastExternalID: String?
    let lastJobStatus: JobStatus?
    let activeClientNames: [String]
    let revokedClientCount: Int
}

/// Loads stats, clients and the newest jobs, then resolves the latest job for
/// each agent kind. `last_seen_at` is not maintained by the server, so the queue
/// is the most reliable record of the last capture/memory.
@MainActor
@Observable
final class AgentsModel {
    private(set) var opencode: AgentSummary?
    private(set) var jarvis: AgentSummary?
    private(set) var queue: Stats.Jobs?
    private(set) var isLoading = false
    private(set) var error: String?
    private(set) var jobsError: String?
    private(set) var clientsError: String?

    private var client: APIClient?
    private var generation = 0

    init(client: APIClient? = nil) {
        self.client = client
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        generation &+= 1
        opencode = nil
        jarvis = nil
        queue = nil
        error = nil
        jobsError = nil
        clientsError = nil
        isLoading = false
    }

    func load() async {
        guard let client else {
            opencode = nil
            jarvis = nil
            queue = nil
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            isLoading = false
            return
        }

        let generation = self.generation
        isLoading = true
        error = nil
        jobsError = nil
        clientsError = nil

        let stats: Stats
        do {
            stats = try await client.stats()
            guard generation == self.generation, !Task.isCancelled else { return }
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            self.error = message(for: error)
            isLoading = false
            return
        }

        var clients: [ClientInfo] = []
        do {
            clients = try await client.listClients().items
            guard generation == self.generation, !Task.isCancelled else { return }
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            clientsError = message(for: error)
        }

        var jobs: [IngestJob] = []
        if !clients.isEmpty {
            do {
                jobs = try await loadRecentJobs(client: client, clients: clients)
                guard generation == self.generation, !Task.isCancelled else { return }
            } catch {
                guard generation == self.generation, !Task.isCancelled else { return }
                jobsError = message(for: error)
            }
        }

        guard generation == self.generation else { return }
        queue = stats.jobs
        opencode = summary(for: .opencode, kind: "opencode", stats: stats, jobs: jobs, clients: clients)
        jarvis = summary(for: .jarvis, kind: "jarvis", stats: stats, jobs: jobs, clients: clients)
        isLoading = false
    }

    func retry() async {
        await load()
    }

    /// Jobs are newest-first and paginated; page through until both agent kinds
    /// have a hit instead of assuming the latest 200 are representative.
    private func loadRecentJobs(
        client: APIClient,
        clients: [ClientInfo]
    ) async throws -> [IngestJob] {
        let wantedKinds = Set(clients.map(\.kind).filter { $0 == "opencode" || $0 == "jarvis" })
        let jobsByID = Dictionary(uniqueKeysWithValues: clients.map { ($0.id, $0.kind) })
        var jobs: [IngestJob] = []
        var foundKinds = Set<String>()
        var cursor: String?

        for _ in 0..<5 {
            let page = try await client.jobs(limit: 200, cursor: cursor)
            jobs.append(contentsOf: page.items)

            for job in page.items {
                if let kind = jobsByID[job.clientID], wantedKinds.contains(kind) {
                    foundKinds.insert(kind)
                }
            }

            guard let next = page.nextCursor, !next.isEmpty else { break }
            if !wantedKinds.isEmpty, foundKinds == wantedKinds { break }
            cursor = next
        }
        return jobs
    }

    private func summary(
        for source: MemorySource,
        kind: String,
        stats: Stats,
        jobs: [IngestJob],
        clients: [ClientInfo]
    ) -> AgentSummary {
        let matching = clients.filter { $0.kind == kind }
        let ids = Set(matching.map(\.id))
        let latest = jobs
            .filter { ids.contains($0.clientID) }
            .max { $0.createdAt < $1.createdAt }

        return AgentSummary(
            recordCount: source == .opencode
                ? stats.records.bySource.opencode
                : stats.records.bySource.jarvis,
            lastActivityAt: latest?.createdAt,
            lastExternalID: latest?.externalID,
            lastJobStatus: latest?.status,
            activeClientNames: matching.filter { $0.revokedAt == nil }.map(\.name).sorted(),
            revokedClientCount: matching.filter { $0.revokedAt != nil }.count
        )
    }

    private func message(for error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
