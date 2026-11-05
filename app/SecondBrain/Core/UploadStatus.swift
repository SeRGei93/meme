import Foundation

struct UploadStatus: Equatable, Sendable {
    var pending = 0
    var failed = 0
    var sent = 0
    var isUploading = false
    var lastError: String?
    var lastSuccessAt: Date?
    var nextRetryAt: Date?
    /// The server rejected the stored token: a rotation is required.
    var requiresAuthentication = false
    /// The local OpenCode token is absent; the user needs to run onboarding.
    var missingToken = false

    var needsAuthentication: Bool {
        requiresAuthentication || missingToken
    }

    /// Nothing is waiting, nothing failed, and nothing was sent in this run.
    /// The UI shows the empty state only when there is also nothing to explain.
    var isEmpty: Bool {
        pending == 0
            && failed == 0
            && sent == 0
            && !isUploading
            && !needsAuthentication
            && nextRetryAt == nil
            && lastError == nil
    }

    /// Short state for the queue badge. Counters stay visible separately, so
    /// this only names the most important thing happening right now.
    var stateTitle: String {
        if requiresAuthentication {
            return "Нужен новый токен"
        }
        if missingToken {
            return "Нужен токен OpenCode"
        }
        if isUploading {
            return "Отправка…"
        }
        if failed > 0 {
            return "Есть ошибки"
        }
        if nextRetryAt != nil {
            return "Ждёт повтора"
        }
        if pending > 0 {
            return "Ожидает отправки"
        }
        if lastError != nil {
            return "Ошибка загрузки"
        }
        if sent > 0 {
            return "Всё отправлено"
        }
        return "Очередь пуста"
    }

    /// Countdown shown while a transient failure is waiting for its next try.
    func retryCountdownText(now: Date) -> String? {
        guard let nextRetryAt else { return nil }
        let remaining = nextRetryAt.timeIntervalSince(now)
        let seconds = max(0, Int(remaining.rounded(.up)))
        if seconds == 0 {
            return "Повтор сейчас"
        }
        if seconds < 60 {
            return "Повтор через \(seconds) с"
        }
        return "Повтор в \(nextRetryAt.formatted(date: .omitted, time: .shortened))"
    }
}
