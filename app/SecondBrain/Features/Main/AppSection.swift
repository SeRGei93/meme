import Foundation

/// Top-level destinations in the main window. Search stays the default so the
/// existing memory workflow is one click away.
enum AppSection: String, CaseIterable, Identifiable, Sendable {
    case search
    case dashboard
    case projects
    case agents
    case clients
    case graph

    var id: String { rawValue }

    var title: String {
        switch self {
        case .search: return "Поиск"
        case .dashboard: return "Обзор"
        case .projects: return "Проекты"
        case .agents: return "Агенты"
        case .clients: return "Клиенты"
        case .graph: return "Граф"
        }
    }

    var systemImage: String {
        switch self {
        case .search: return "magnifyingglass"
        case .dashboard: return "rectangle.grid.2x2"
        case .projects: return "folder"
        case .agents: return "person.2"
        case .clients: return "key"
        case .graph: return "point.3.connected.trianglepath.dotted"
        }
    }

    /// Section shown on launch. UI tests can override it in DEBUG.
    static func initial(environment: [String: String] = ProcessInfo.processInfo.environment) -> AppSection {
        #if DEBUG
        if let raw = environment["SECOND_BRAIN_UI_SECTION"],
           let section = AppSection(rawValue: raw) {
            return section
        }
        #endif
        return .search
    }
}
