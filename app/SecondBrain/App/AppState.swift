import Foundation
import Observation

/// Shared UI state. One observable object feeds the window, menu bar and
/// Settings, so a failed health check or a revoked token is visible everywhere.
@Observable
final class AppState {
    enum Connection: Equatable {
        case unknown
        case checking
        case healthy(Health)
        case unauthenticated
        case failed(String)
    }

    var statusText = "Готов к работе"
    var lastSync: Date?
    var serverURL: URL?
    var connection: Connection = .unknown
    var health: Health?
    var stats: Stats?
    var errorMessage: String?
    var requiresReauthentication = false

    /// Public endpoint: works before the user has entered any token.
    func checkHealth(using client: APIClient) async {
        statusText = "Проверка подключения…"
        connection = .checking

        do {
            let health = try await client.health()
            self.health = health
            serverURL = client.configuration.baseURL
            lastSync = Date()
            requiresReauthentication = false
            errorMessage = nil
            connection = .healthy(health)
            statusText = health.status == "ok"
                ? "Подключено"
                : "Сервер работает с ограничениями"
        } catch {
            apply(error)
        }
    }

    /// Authenticated call used to verify that the stored token still works.
    func loadStats(using client: APIClient) async {
        do {
            let stats = try await client.stats()
            self.stats = stats
            serverURL = client.configuration.baseURL
            lastSync = Date()
            requiresReauthentication = false
            errorMessage = nil
            connection = health.map(Connection.healthy) ?? .unknown
            statusText = "Подключено"
        } catch {
            apply(error)
        }
    }

    /// Maps any error to the state the UI shows. A `401`/`403` stops the queue
    /// and asks for a new token instead of leaving a generic failure behind.
    func apply(_ error: Error) {
        let apiError = error as? APIError
            ?? .transport(localized: error.localizedDescription, retryable: true)
        errorMessage = apiError.displayMessage

        if apiError.requiresReauthentication {
            requiresReauthentication = true
            connection = .unauthenticated
            statusText = apiError.displayMessage
        } else {
            connection = .failed(apiError.displayMessage)
            statusText = apiError.displayMessage
        }
    }
}
