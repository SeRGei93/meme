import Foundation
import Observation

/// Shared UI state. Real API, spool and queue state arrive in M4/M5; keeping a
/// single observable object now avoids rewriting views later.
@Observable
final class AppState {
    var statusText = "Готов к работе"
    var lastSync: Date?
    var serverURL: URL?
}
