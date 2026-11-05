import SwiftUI

/// One selectable source and its record count.
struct SourceOption: Identifiable, Equatable, Hashable, Sendable {
    let source: String
    let count: Int

    var id: String { source }
    var title: String { SourceStyle.title(for: source) }

    /// Picker/menu label. Zero-count entries (a stale or manually selected
    /// filter) omit the count instead of showing "· 0".
    var label: String { count > 0 ? "\(title) · \(count)" : title }

    /// Builds a sorted list from `stats.by_source` and keeps the current
    /// selection visible even when the server no longer reports it.
    static func options(from counts: [String: Int], including selected: String?) -> [SourceOption] {
        var options = counts
            .map { SourceOption(source: $0.key, count: $0.value) }
            .sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
        if let selected, !options.contains(where: { $0.source == selected }) {
            options.append(SourceOption(source: selected, count: 0))
        }
        return options
    }
}

/// Color and label for a knowledge source. Shared by search badges, the graph
/// legend/canvas and the agents screen. Colors come from a fixed palette chosen
/// by a deterministic hash, so a new source gets a stable color without a code
/// change and the same color after a restart.
enum SourceStyle {
    /// Palette entries exclude the legacy OpenCode/Jarvis hues so the two
    /// familiar sources keep their established colors.
    private static let palette: [Color] = [
        .teal, .orange, .pink, .green, .indigo, .red, .mint, .brown,
    ]

    static func title(for source: String) -> String {
        switch source {
        case "opencode": return "OpenCode"
        case "jarvis": return "Jarvis"
        default: return source
        }
    }

    static func tint(for source: String) -> Color {
        switch source {
        case "opencode": return .blue
        case "jarvis": return .purple
        default:
            let index = Int(stableHash(source) % UInt64(palette.count))
            return palette[index]
        }
    }

    /// FNV-1a over UTF-8 bytes. `String.hashValue` is randomized per process,
    /// which would make source colors change between launches.
    private static func stableHash(_ value: String) -> UInt64 {
        var hash: UInt64 = 0xcbf29ce484222325
        for byte in value.utf8 {
            hash ^= UInt64(byte)
            hash = hash &* 0x100000001b3
        }
        return hash
    }
}
