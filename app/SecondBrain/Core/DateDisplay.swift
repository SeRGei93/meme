import Foundation

/// Shared, locale-aware date text for status screens. A missing server value
/// is rendered explicitly instead of as an empty label.
enum DateDisplay {
    static func text(_ date: Date?) -> String {
        guard let date else { return "Нет данных" }
        return date.formatted(date: .abbreviated, time: .shortened)
    }
}
