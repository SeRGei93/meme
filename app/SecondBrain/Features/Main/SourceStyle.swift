import SwiftUI

/// Color and label for a knowledge source. Shared by the search badge and the
/// graph legend/canvas so both screens describe the same two sources.
enum SourceStyle {
    static func title(for source: String) -> String {
        switch source {
        case MemorySource.opencode.rawValue: return "OpenCode"
        case MemorySource.jarvis.rawValue: return "Jarvis"
        default: return source.capitalized
        }
    }

    static func tint(for source: String) -> Color {
        switch source {
        case MemorySource.opencode.rawValue: return .blue
        case MemorySource.jarvis.rawValue: return .purple
        default: return .secondary
        }
    }
}
