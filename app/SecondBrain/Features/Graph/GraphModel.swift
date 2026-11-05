import Foundation
import Observation

/// Project chooser entry. `/api/v1/projects` provides names; when it is
/// unavailable the graph's own node project keys keep the filter usable.
struct ProjectOption: Identifiable, Equatable, Sendable {
    let key: String
    let name: String

    var id: String { key }
}

/// One node ready to draw: the contract node plus its normalized position.
struct GraphVisualNode: Identifiable, Equatable, Sendable {
    let id: Int64
    let title: String
    let source: String
    let projectKey: String
    let point: GraphPoint
}

/// One drawn link. `weight` is normalized to `0...1` by the contract.
struct GraphVisualEdge: Identifiable, Equatable, Sendable {
    var id: String { "\(fromID)->\(toID)" }

    let fromID: Int64
    let toID: Int64
    let weight: Double
}

/// State for the graph screen.
///
/// The graph is fetched once per project filter (source filtering is local,
/// because every node carries its source). Layout runs on a detached task and
/// the canvas only redraws when positions arrive, so a 400-node graph never
/// blocks the main actor.
@MainActor
@Observable
final class GraphModel {
    /// The server supports up to 500; 400 keeps the O(n²) layout fast while
    /// showing far more than the default page of 100.
    static let nodeLimit = 400

    private(set) var graph: MemoryGraph?
    private(set) var projects: [Project] = []
    private(set) var positions: [Int64: GraphPoint] = [:]
    private(set) var isLoading = false
    private(set) var isLayouting = false
    private(set) var error: String?
    private(set) var projectsError: String?
    private(set) var detail: Record?
    private(set) var detailLoading = false
    private(set) var detailError: String?
    private(set) var selectedNodeID: Int64?

    /// Server-side project filter. Changing it requires a reload.
    var projectKey: String?
    /// Local source filter.
    var source: MemorySource?
    var isDetailPresented = false

    private var client: APIClient?
    private var generation = 0
    private var detailTask: Task<Void, Never>?

    init(client: APIClient? = nil) {
        self.client = client
    }

    // MARK: - Derived state

    var nodes: [GraphNode] {
        let all = graph?.nodes ?? []
        guard let source else { return all }
        return all.filter { $0.source == source.rawValue }
    }

    var edges: [GraphEdge] {
        let ids = Set(nodes.map(\.id))
        return (graph?.edges ?? []).filter {
            ids.contains($0.fromID) && ids.contains($0.toID)
        }
    }

    var visualNodes: [GraphVisualNode] {
        nodes.compactMap { node in
            guard let point = positions[node.id] else { return nil }
            return GraphVisualNode(
                id: node.id,
                title: node.title,
                source: node.source,
                projectKey: node.projectKey,
                point: point
            )
        }
    }

    var visualEdges: [GraphVisualEdge] {
        let ids = Set(visualNodes.map(\.id))
        return edges.compactMap { edge in
            guard ids.contains(edge.fromID), ids.contains(edge.toID) else { return nil }
            return GraphVisualEdge(
                fromID: edge.fromID,
                toID: edge.toID,
                weight: edge.weight
            )
        }
    }

    var isTruncated: Bool { graph?.truncated ?? false }

    /// True when the (project-filtered) response contains nodes. A `false`
    /// value with a project filter means the project itself has no records.
    var hasNodesInResponse: Bool { !(graph?.nodes.isEmpty ?? true) }

    var projectOptions: [ProjectOption] {
        var names: [String: String] = [:]
        for project in projects {
            names[project.key] = project.name.isEmpty ? project.key : project.name
        }
        for node in graph?.nodes ?? [] where !node.projectKey.isEmpty {
            if names[node.projectKey] == nil {
                names[node.projectKey] = node.projectKey
            }
        }
        return names
            .map { ProjectOption(key: $0.key, name: $0.value) }
            .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    func displayName(for projectKey: String) -> String {
        projectOptions.first { $0.key == projectKey }?.name ?? projectKey
    }

    func project(for projectKey: String) -> Project? {
        projects.first { $0.key == projectKey }
    }

    func count(for source: MemorySource?) -> Int {
        guard let source else { return graph?.nodes.count ?? 0 }
        return (graph?.nodes ?? []).filter { $0.source == source.rawValue }.count
    }

    // MARK: - Lifecycle

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        reset()
    }

    private func reset() {
        generation &+= 1
        graph = nil
        projects = []
        positions = [:]
        isLoading = false
        isLayouting = false
        error = nil
        projectsError = nil
        clearSelection()
    }

    func load() async {
        clearSelection()
        guard let client else {
            graph = nil
            positions = [:]
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            isLoading = false
            isLayouting = false
            return
        }

        generation &+= 1
        let generation = self.generation
        isLoading = true
        error = nil
        projectsError = nil

        let projectsTask = Task { try await client.projects() }
        defer { projectsTask.cancel() }

        let result: MemoryGraph
        do {
            result = try await client.graph(projectKey: projectKey, limit: Self.nodeLimit)
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            graph = nil
            positions = [:]
            self.error = ErrorDisplay.message(for: error)
            isLoading = false
            isLayouting = false
            return
        }
        guard generation == self.generation, !Task.isCancelled else { return }

        graph = result
        isLoading = false
        isLayouting = true

        await applyLayout(result, generation: generation)
        guard generation == self.generation, !Task.isCancelled else { return }

        do {
            let list = try await projectsTask.value
            guard generation == self.generation, !Task.isCancelled else { return }
            projects = list.items
            projectsError = nil
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            projectsError = ErrorDisplay.message(for: error)
        }
    }

    /// Resets both filters. Changing `projectKey` triggers a reload in the view.
    func resetFilters() {
        projectKey = nil
        source = nil
    }

    private func applyLayout(_ graph: MemoryGraph, generation: Int) async {
        let ids = graph.nodes.map(\.id)
        let links = graph.edges.map {
            GraphLink(fromID: $0.fromID, toID: $0.toID, weight: $0.weight)
        }
        isLayouting = true

        let points = await Task.detached(priority: .userInitiated) {
            GraphLayout.compute(nodeIDs: ids, edges: links)
        }.value

        guard generation == self.generation, !Task.isCancelled else { return }
        positions = points
        isLayouting = false
    }

    // MARK: - Selection

    func selectNode(_ id: Int64) {
        selectedNodeID = id
        isDetailPresented = true
        loadDetail(id)
    }

    func dismissDetail() {
        clearSelection()
    }

    private func clearSelection() {
        isDetailPresented = false
        selectedNodeID = nil
        detailTask?.cancel()
        detailTask = nil
        detail = nil
        detailLoading = false
        detailError = nil
    }

    func retryDetail() {
        guard let selectedNodeID else { return }
        loadDetail(selectedNodeID)
    }

    private func loadDetail(_ id: Int64) {
        guard let client else {
            detailError = "Нет доступа к серверу. Проверьте подключение в Settings."
            detailLoading = false
            return
        }

        detailTask?.cancel()
        detail = nil
        detailError = nil
        detailLoading = true
        detailTask = Task { [weak self] in
            do {
                let record = try await client.record(id: id)
                guard !Task.isCancelled, self?.selectedNodeID == id else { return }
                self?.detail = record
                self?.detailLoading = false
            } catch {
                guard !Task.isCancelled, self?.selectedNodeID == id else { return }
                self?.detailLoading = false
                self?.detailError = ErrorDisplay.message(for: error)
            }
        }
    }
}
