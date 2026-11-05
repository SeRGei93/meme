import Foundation
import Observation

/// State for the project list. Filtering is local because `/api/v1/projects`
/// returns a single bounded page (up to 500 rows) and the user expects instant
/// feedback while typing.
@MainActor
@Observable
final class ProjectsModel {
    private(set) var projects: [Project] = []
    private(set) var isLoading = false
    private(set) var error: String?
    var filter = ""

    private var client: APIClient?
    private var generation = 0

    init(client: APIClient? = nil) {
        self.client = client
    }

    var filteredProjects: [Project] {
        let needle = filter.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !needle.isEmpty else { return projects }
        return projects.filter { project in
            project.name.localizedCaseInsensitiveContains(needle)
                || project.key.localizedCaseInsensitiveContains(needle)
                || project.path.localizedCaseInsensitiveContains(needle)
        }
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        generation &+= 1
        projects = []
        error = nil
        isLoading = false
    }

    func load() async {
        guard let client else {
            projects = []
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            isLoading = false
            return
        }

        let generation = self.generation
        isLoading = true
        error = nil

        do {
            let response = try await client.projects()
            guard generation == self.generation, !Task.isCancelled else { return }
            projects = response.items
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            projects = []
            self.error = ErrorDisplay.message(for: error)
        }

        guard generation == self.generation else { return }
        isLoading = false
    }

    func retry() async {
        await load()
    }
}
