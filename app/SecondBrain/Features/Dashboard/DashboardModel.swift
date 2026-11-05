import Foundation
import Observation

/// State for the dashboard: aggregate counters, the server queue, local upload
/// state and the failed ingestion jobs that would otherwise only be visible in
/// server logs.
@MainActor
@Observable
final class DashboardModel {
    private(set) var stats: Stats?
    private(set) var failedJobs: [IngestJob] = []
    private(set) var isLoading = false
    private(set) var error: String?
    private(set) var jobsError: String?
    private(set) var lastUpdated: Date?

    private var client: APIClient?
    private var generation = 0

    init(client: APIClient? = nil) {
        self.client = client
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        generation &+= 1
        stats = nil
        failedJobs = []
        error = nil
        jobsError = nil
        isLoading = false
        lastUpdated = nil
    }

    func load() async {
        guard let client else {
            stats = nil
            failedJobs = []
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            jobsError = nil
            isLoading = false
            return
        }

        let generation = self.generation
        isLoading = true
        error = nil
        jobsError = nil

        do {
            let stats = try await client.stats()
            guard generation == self.generation, !Task.isCancelled else { return }
            self.stats = stats
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            self.error = message(for: error)
        }

        do {
            let jobs = try await client.jobs(status: .failed, limit: 20)
            guard generation == self.generation, !Task.isCancelled else { return }
            self.failedJobs = jobs.items
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            self.jobsError = message(for: error)
        }

        guard generation == self.generation else { return }
        isLoading = false
        lastUpdated = Date()
    }

    func retry() async {
        await load()
    }

    private func message(for error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
