import SwiftUI

/// Queue state as presented in the sidebar, the menu bar panel and the status
/// item. Keeping it in one place means a new queue state (for example a missing
/// token or a scheduled retry) is handled by every surface at once.
struct UploadQueuePresentation {
    let status: UploadStatus
    let requiresReauthentication: Bool

    var needsAuthentication: Bool {
        requiresReauthentication || status.needsAuthentication
    }

    var title: String {
        if requiresReauthentication || status.requiresAuthentication {
            return "Нужен новый токен"
        }
        return status.stateTitle
    }

    var tint: Color {
        if needsAuthentication || status.failed > 0 || status.lastError != nil {
            return .red
        }
        if status.isUploading {
            return .accentColor
        }
        if status.pending > 0 || status.nextRetryAt != nil {
            return .orange
        }
        return .secondary
    }

    var systemImage: String {
        if needsAuthentication {
            return "exclamationmark.triangle"
        }
        if status.failed > 0 {
            return "exclamationmark.circle"
        }
        if status.isUploading || status.pending > 0 || status.nextRetryAt != nil {
            return "arrow.up.circle"
        }
        return "brain.head.profile"
    }

    var accessibilityStatus: String {
        if needsAuthentication {
            return "нужен новый токен"
        }
        if status.failed > 0 {
            return Self.errorText(status.failed)
        }
        if status.isUploading {
            return "идёт отправка"
        }
        if status.pending > 0 {
            return "\(status.pending) в очереди"
        }
        if status.nextRetryAt != nil {
            return "ждёт повтора"
        }
        return "очередь пуста"
    }

    private static func errorText(_ count: Int) -> String {
        let lastTwo = count % 100
        let last = count % 10
        if last == 1 && lastTwo != 11 {
            return "\(count) ошибка загрузки"
        }
        if (2...4).contains(last) && !(12...14).contains(lastTwo) {
            return "\(count) ошибки загрузки"
        }
        return "\(count) ошибок загрузки"
    }
}
