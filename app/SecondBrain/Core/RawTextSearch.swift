import Foundation

enum RawTextSearch {
    /// Lines containing the query, case-insensitively. Kept separate from the
    /// view so large raw texts can be scanned off the main thread and tested.
    static func matchingLines(in text: String, query: String) -> [String] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return [] }
        return text
            .components(separatedBy: .newlines)
            .filter { $0.localizedCaseInsensitiveContains(trimmed) }
    }
}
