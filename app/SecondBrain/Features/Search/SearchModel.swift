import Foundation
import Observation

enum MemorySource: String, CaseIterable, Identifiable, Sendable {
    case opencode
    case jarvis

    var id: String { rawValue }

    var title: String {
        switch self {
        case .opencode: return "OpenCode"
        case .jarvis: return "Jarvis"
        }
    }
}

enum SearchDateRange: String, CaseIterable, Identifiable, Sendable {
    case any
    case today
    case week
    case month
    case quarter

    var id: String { rawValue }

    var title: String {
        switch self {
        case .any: return "Любая дата"
        case .today: return "Сегодня"
        case .week: return "7 дней"
        case .month: return "30 дней"
        case .quarter: return "90 дней"
        }
    }

    func since(now: Date, calendar: Calendar = .current) -> Date? {
        switch self {
        case .any:
            return nil
        case .today:
            return calendar.startOfDay(for: now)
        case .week:
            return calendar.date(byAdding: .day, value: -7, to: now)
        case .month:
            return calendar.date(byAdding: .day, value: -30, to: now)
        case .quarter:
            return calendar.date(byAdding: .day, value: -90, to: now)
        }
    }
}

/// State for the memory search screen. The model owns debounce, filters,
/// selection and the full-record fetch, so the views stay declarative and the
/// behaviour can be tested without SwiftUI.
@MainActor
@Observable
final class SearchModel {
    struct Criteria: Equatable, Sendable {
        let query: String
        let projectKey: String?
        let source: MemorySource?
        let dateRange: SearchDateRange
    }

    var query = "" {
        didSet {
            guard query != oldValue else { return }
            scheduleSearch()
        }
    }

    var projectKey: String? {
        didSet {
            guard projectKey != oldValue else { return }
            scheduleSearch()
        }
    }

    var source: MemorySource? {
        didSet {
            guard source != oldValue else { return }
            scheduleSearch()
        }
    }

    var dateRange: SearchDateRange = .any {
        didSet {
            guard dateRange != oldValue else { return }
            scheduleSearch()
        }
    }

    var selectedID: Int64? {
        didSet {
            guard selectedID != oldValue else { return }
            loadSelectedRecord()
        }
    }

    private(set) var results: [Card] = []
    private(set) var total = 0
    private(set) var isLoading = false
    private(set) var error: String?
    private(set) var projects: [Project] = []
    private(set) var detail: Record?
    private(set) var detailLoading = false
    private(set) var detailError: String?

    private var client: APIClient?
    private var searchTask: Task<Void, Never>?
    private var detailTask: Task<Void, Never>?
    private var projectsTask: Task<Void, Never>?
    private var projectsLoading = false
    private var projectsLoaded = false
    private var projectsGeneration = 0
    private let debounce: Duration
    private let now: () -> Date
    private let calendar: Calendar
    private let limit: Int

    init(
        client: APIClient? = nil,
        debounce: Duration = .milliseconds(300),
        limit: Int = 20,
        now: @escaping () -> Date = { Date() },
        calendar: Calendar = .current
    ) {
        self.client = client
        self.debounce = debounce
        self.limit = limit
        self.now = now
        self.calendar = calendar
        #if DEBUG
        query = ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_QUERY"] ?? ""
        #endif
    }

    var trimmedQuery: String {
        query.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var hasActiveFilters: Bool {
        projectKey != nil || source != nil || dateRange != .any
    }

    var isIdle: Bool {
        trimmedQuery.isEmpty
    }

    var isQueryValid: Bool {
        guard !trimmedQuery.isEmpty, trimmedQuery.count <= 500 else { return false }
        return trimmedQuery.rangeOfCharacter(from: .alphanumerics) != nil
    }

    var currentCriteria: Criteria {
        Criteria(query: trimmedQuery, projectKey: projectKey, source: source, dateRange: dateRange)
    }

    var selectedProject: Project? {
        guard let projectKey else { return nil }
        return projects.first { $0.key == projectKey }
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        guard client != nil else {
            projectsTask?.cancel()
            projectsTask = nil
            projectsLoading = false
            projectsLoaded = false
            projectsGeneration &+= 1
            projects = []
            results = []
            total = 0
            selectedID = nil
            detail = nil
            detailError = nil
            error = nil
            isLoading = false
            return
        }
        results = []
        total = 0
        selectedID = nil
        detail = nil
        detailError = nil
        error = nil
        isLoading = false
        projectsTask?.cancel()
        projectsTask = nil
        projectsLoading = false
        projectsLoaded = false
        projectsGeneration &+= 1
        loadProjects()
        if !isIdle {
            scheduleSearch(immediate: false)
        }
    }

    /// Ensures the project picker can resolve a display name after the search
    /// view is recreated, without re-fetching on every appearance.
    func ensureProjectsLoaded() {
        guard !projectsLoaded, !projectsLoading else { return }
        loadProjects()
    }

    /// Runs the current query immediately, bypassing the debounce. Used by
    /// tests and explicit refreshes.
    func searchNow() async {
        searchTask?.cancel()
        searchTask = nil
        await performSearch()
    }

    func retrySearch() {
        scheduleSearch(immediate: true)
    }

    /// Opens a record requested from outside the search flow (menu bar quick
    /// search). An already selected record is reloaded instead of ignored.
    func openRecord(id: Int64) {
        if selectedID == id {
            loadSelectedRecord()
        } else {
            selectedID = id
        }
    }

    func moveSelection(by delta: Int) {
        guard !results.isEmpty else { return }
        guard let selectedID,
              let index = results.firstIndex(where: { $0.id == selectedID }) else {
            self.selectedID = delta < 0 ? results.last?.id : results.first?.id
            return
        }
        let next = (index + delta % results.count + results.count) % results.count
        self.selectedID = results[next].id
    }

    func resetFilters() {
        projectKey = nil
        source = nil
        dateRange = .any
    }

    func retryDetail() {
        loadSelectedRecord()
    }

    /// Cancels in-flight work, e.g. when the search screen disappears or a test
    /// finishes before a detail request completes.
    func cancelPendingRequests() {
        searchTask?.cancel()
        detailTask?.cancel()
        projectsTask?.cancel()
        searchTask = nil
        detailTask = nil
        projectsTask = nil
        projectsLoading = false
        isLoading = false
        detailLoading = false
    }

    // MARK: - Search

    private func scheduleSearch(immediate: Bool = false) {
        searchTask?.cancel()

        guard !trimmedQuery.isEmpty else {
            searchTask = nil
            results = []
            total = 0
            selectedID = nil
            detail = nil
            error = nil
            isLoading = false
            return
        }

        guard isQueryValid else {
            searchTask = nil
            results = []
            total = 0
            selectedID = nil
            detail = nil
            error = nil
            isLoading = false
            return
        }

        isLoading = true
        let debounce = self.debounce
        searchTask = Task { [weak self] in
            if !immediate, debounce > .zero {
                try? await Task.sleep(for: debounce)
            }
            guard !Task.isCancelled else { return }
            await self?.performSearch()
        }
    }

    private func performSearch() async {
        let criteria = currentCriteria
        guard !criteria.query.isEmpty, isQueryValid else {
            results = []
            total = 0
            selectedID = nil
            error = nil
            isLoading = false
            return
        }
        guard let client else {
            results = []
            total = 0
            selectedID = nil
            isLoading = false
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            return
        }

        isLoading = true
        error = nil
        let since = criteria.dateRange.since(now: now(), calendar: calendar)

        do {
            let response = try await client.search(
                query: criteria.query,
                limit: limit,
                projectKey: criteria.projectKey,
                source: criteria.source?.rawValue,
                since: since
            )
            guard !Task.isCancelled, criteria == currentCriteria else { return }
            results = response.items
            total = response.total
            isLoading = false
            if let selectedID, results.contains(where: { $0.id == selectedID }) {
                return
            }
            selectedID = results.first?.id
        } catch {
            guard !Task.isCancelled, criteria == currentCriteria else { return }
            isLoading = false
            results = []
            total = 0
            selectedID = nil
            self.error = (error as? LocalizedError)?.errorDescription
                ?? error.localizedDescription
        }
    }

    // MARK: - Projects and detail

    private func loadProjects() {
        guard let client, !projectsLoading else { return }
        projectsLoading = true
        projectsGeneration &+= 1
        let generation = projectsGeneration
        projectsTask?.cancel()
        projectsTask = Task { [weak self] in
            defer {
                if self?.projectsGeneration == generation {
                    self?.projectsLoading = false
                }
            }
            do {
                let response = try await client.projects()
                guard !Task.isCancelled, self?.projectsGeneration == generation else { return }
                self?.projects = response.items
                self?.projectsLoaded = true
            } catch {
                guard !Task.isCancelled, self?.projectsGeneration == generation else { return }
                self?.projects = []
            }
        }
    }

    private func loadSelectedRecord() {
        detailTask?.cancel()
        guard let id = selectedID, let client else {
            detail = nil
            detailLoading = false
            detailError = nil
            return
        }

        detail = nil
        detailLoading = true
        detailError = nil
        detailTask = Task { [weak self] in
            do {
                let record = try await client.record(id: id)
                guard !Task.isCancelled, self?.selectedID == id else { return }
                self?.detail = record
                self?.detailLoading = false
            } catch {
                guard !Task.isCancelled, self?.selectedID == id else { return }
                self?.detailLoading = false
                self?.detailError = (error as? LocalizedError)?.errorDescription
                    ?? error.localizedDescription
            }
        }
    }
}
