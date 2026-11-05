import Foundation
import Observation

/// One source's aggregate status. Token/plugin state that belongs to this
/// installation is added by the view, keeping the model testable with stubs.
struct AgentSummary: Identifiable, Equatable, Sendable {
    let source: String
    let recordCount: Int
    let lastActivityAt: Date?
    let lastExternalID: String?
    let lastJobStatus: JobStatus?
    let activeClientNames: [String]
    let revokedClientCount: Int
    /// True when one of the clients under this source is the OpenCode client
    /// created by this installation, so plugin/MCP rows are relevant.
    let isLocalOpenCode: Bool

    var id: String { source }
    var title: String { SourceStyle.title(for: source) }
}

/// Loads stats, clients and the newest jobs, then aggregates them per source.
/// A source can have several clients (machines, rotations); records and jobs are
/// summed, while names show which instances are active.
@MainActor
@Observable
final class AgentsModel {
    private(set) var summaries: [AgentSummary] = []
    private(set) var queue: Stats.Jobs?
    private(set) var isLoading = true
    private(set) var error: String?
    private(set) var jobsError: String?
    private(set) var clientsError: String?

    /// Set before `load()` so the OpenCode card can show plugin/MCP rows only
    /// for the client this installation created.
    var localOpenCodeClient: StoredClient?

    private var client: APIClient?
    private var generation = 0

    init(client: APIClient? = nil) {
        self.client = client
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        generation &+= 1
        summaries = []
        queue = nil
        error = nil
        jobsError = nil
        clientsError = nil
        isLoading = false
    }

    func load() async {
        guard let client else {
            summaries = []
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
            self.error = ErrorDisplay.message(for: error)
            isLoading = false
            return
        }

        var clients: [ClientInfo] = []
        do {
            clients = try await client.listClients().items
            guard generation == self.generation, !Task.isCancelled else { return }
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            clientsError = ErrorDisplay.message(for: error)
        }

        var jobs: [IngestJob] = []
        if !clients.isEmpty {
            do {
                jobs = try await loadRecentJobs(client: client, clients: clients)
                guard generation == self.generation, !Task.isCancelled else { return }
            } catch {
                guard generation == self.generation, !Task.isCancelled else { return }
                jobsError = ErrorDisplay.message(for: error)
            }
        }

        guard generation == self.generation else { return }
        queue = stats.jobs
        summaries = buildSummaries(stats: stats, clients: clients, jobs: jobs)
        isLoading = false
    }

    func retry() async {
        await load()
    }

    /// Jobs are newest-first and paginated; page through until every source
    /// client has a hit instead of assuming the latest 200 are representative.
    private func loadRecentJobs(
        client: APIClient,
        clients: [ClientInfo]
    ) async throws -> [IngestJob] {
        let wanted = Set(clients.compactMap { $0.source == nil ? nil : $0.id })
        guard !wanted.isEmpty else { return [] }

        var jobs: [IngestJob] = []
        var found = Set<Int64>()
        var cursor: String?

        for _ in 0..<5 {
            let page = try await client.jobs(limit: 200, cursor: cursor)
            jobs.append(contentsOf: page.items)

            for job in page.items where wanted.contains(job.clientID) {
                found.insert(job.clientID)
            }

            guard let next = page.nextCursor, !next.isEmpty else { break }
            if found == wanted { break }
            cursor = next
        }
        return jobs
    }

    private func buildSummaries(
        stats: Stats,
        clients: [ClientInfo],
        jobs: [IngestJob]
    ) -> [AgentSummary] {
        var grouped: [String: [ClientInfo]] = [:]
        for client in clients {
            guard let source = client.source else { continue }
            grouped[source, default: []].append(client)
        }
        let sources = Set(grouped.keys).union(stats.records.bySource.keys)
        let jobsByClient = Dictionary(grouping: jobs, by: \.clientID)

        return sources
            .map { source in
                let group = grouped[source] ?? []
                let latest = group
                    .flatMap { jobsByClient[$0.id] ?? [] }
                    .max { $0.createdAt < $1.createdAt }
                let isLocal = localOpenCodeClient.map { local in
                    group.contains { $0.id == local.id && $0.name == local.name }
                } ?? false

                return AgentSummary(
                    source: source,
                    recordCount: stats.records.bySource[source] ?? 0,
                    lastActivityAt: latest?.createdAt,
                    lastExternalID: latest?.externalID,
                    lastJobStatus: latest?.status,
                    activeClientNames: group
                        .filter { $0.revokedAt == nil }
                        .map(\.name)
                        .sorted(),
                    revokedClientCount: group.filter { $0.revokedAt != nil }.count,
                    isLocalOpenCode: isLocal
                )
            }
            .sorted {
                $0.title.localizedStandardCompare($1.title) == .orderedAscending
            }
    }
}
