import Foundation
import Observation

/// Lightweight search used by the menu bar panel. It deliberately does not
/// depend on `SearchModel`: the panel only needs a debounced query and a short
/// result list, and its state must reset when the panel closes or the server
/// changes.
@MainActor
@Observable
final class MenuBarSearchModel {
    var query = "" {
        didSet {
            guard query != oldValue else { return }
            scheduleSearch()
        }
    }

    private(set) var results: [Card] = []
    private(set) var isSearching = false
    private(set) var error: String?

    var trimmedQuery: String {
        query.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var hasQuery: Bool {
        !trimmedQuery.isEmpty
    }

    /// Mirrors `SearchModel.isQueryValid`: the server needs at least one
    /// alphanumeric character and rejects queries longer than 500 characters.
    var isQueryValid: Bool {
        guard hasQuery, trimmedQuery.count <= 500 else { return false }
        return trimmedQuery.rangeOfCharacter(from: .alphanumerics) != nil
    }

    private var client: APIClient?
    private var task: Task<Void, Never>?
    private var generation = 0
    private let debounce: Duration
    private let limit: Int

    init(
        client: APIClient? = nil,
        debounce: Duration = .milliseconds(250),
        limit: Int = 8
    ) {
        self.client = client
        self.debounce = debounce
        self.limit = limit
        #if DEBUG
        // UI tests cannot reliably synthesize keyboard input into a menu bar
        // panel, so they seed the query and exercise the result flow.
        query = ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_MENU_QUERY"] ?? ""
        #endif
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        task?.cancel()
        task = nil
        results = []
        isSearching = false
        error = nil
        if hasQuery {
            scheduleSearch()
        }
    }

    /// The popover can be dismissed mid-debounce or mid-request. Drop that work
    /// and clear the query so the next opening starts from a clean state.
    func resetForDismissal() {
        task?.cancel()
        task = nil
        query = ""
        results = []
        isSearching = false
        error = nil
    }

    func retry() {
        scheduleSearch()
    }

    func clear() {
        query = ""
    }

    private func scheduleSearch() {
        task?.cancel()
        generation &+= 1
        let generation = self.generation
        let trimmed = trimmedQuery

        guard !trimmed.isEmpty, isQueryValid else {
            task = nil
            results = []
            isSearching = false
            error = nil
            return
        }

        isSearching = true
        error = nil
        task = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(for: self.debounce)
            guard !Task.isCancelled else { return }
            await self.performSearch(trimmed, generation: generation)
        }
    }

    private func performSearch(_ query: String, generation: Int) async {
        guard let client else {
            results = []
            isSearching = false
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            return
        }

        do {
            let response = try await client.search(query: query, limit: limit)
            guard generation == self.generation, !Task.isCancelled else { return }
            results = response.items
            isSearching = false
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            results = []
            isSearching = false
            self.error = ErrorDisplay.message(for: error)
        }
    }
}
