import Foundation

/// User-facing error text shared by the feature models. `LocalizedError`
/// descriptions come from `APIError`; anything else falls back to the system
/// message so a network failure is never rendered as an empty string.
enum ErrorDisplay {
    static func message(for error: Error) -> String {
        (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
    }
}
