import XCTest
@testable import SecondBrain

final class UploadStatusTests: XCTestCase {
    func testEmptyOnlyWhenThereIsNoActivityOrError() {
        XCTAssertTrue(UploadStatus().isEmpty)

        var pending = UploadStatus()
        pending.pending = 1
        XCTAssertFalse(pending.isEmpty)

        var sent = UploadStatus()
        sent.sent = 1
        XCTAssertFalse(sent.isEmpty)

        var failed = UploadStatus()
        failed.failed = 1
        XCTAssertFalse(failed.isEmpty)

        var uploading = UploadStatus()
        uploading.isUploading = true
        XCTAssertFalse(uploading.isEmpty)

        var error = UploadStatus()
        error.lastError = "server down"
        XCTAssertFalse(error.isEmpty)

        var retrying = UploadStatus()
        retrying.nextRetryAt = Date().addingTimeInterval(5)
        XCTAssertFalse(retrying.isEmpty)

        var unauthorized = UploadStatus()
        unauthorized.requiresAuthentication = true
        XCTAssertFalse(unauthorized.isEmpty)

        var missingToken = UploadStatus()
        missingToken.missingToken = true
        XCTAssertFalse(missingToken.isEmpty)
    }

    func testStateTitlePrioritisesTheActionTheUserNeeds() {
        XCTAssertEqual(UploadStatus().stateTitle, "Очередь пуста")

        var queued = UploadStatus()
        queued.pending = 2
        XCTAssertEqual(queued.stateTitle, "Ожидает отправки")

        var uploading = queued
        uploading.isUploading = true
        XCTAssertEqual(uploading.stateTitle, "Отправка…")

        var retrying = queued
        retrying.nextRetryAt = Date().addingTimeInterval(5)
        XCTAssertEqual(retrying.stateTitle, "Ждёт повтора")

        var failed = queued
        failed.failed = 1
        XCTAssertEqual(failed.stateTitle, "Есть ошибки")

        var unauthorized = failed
        unauthorized.requiresAuthentication = true
        XCTAssertEqual(unauthorized.stateTitle, "Нужен новый токен")

        var missingToken = failed
        missingToken.missingToken = true
        XCTAssertEqual(missingToken.stateTitle, "Нужен токен OpenCode")

        var errorOnly = UploadStatus()
        errorOnly.lastError = "spool unavailable"
        XCTAssertEqual(errorOnly.stateTitle, "Ошибка загрузки")

        var sent = UploadStatus()
        sent.sent = 1
        XCTAssertEqual(sent.stateTitle, "Всё отправлено")
    }

    func testRetryCountdownHandlesSecondsMinutesAndOverdueTimers() {
        let now = Date(timeIntervalSince1970: 1_000)

        var status = UploadStatus()
        XCTAssertNil(status.retryCountdownText(now: now))

        status.nextRetryAt = now.addingTimeInterval(4.2)
        XCTAssertEqual(status.retryCountdownText(now: now), "Повтор через 5 с")

        status.nextRetryAt = now.addingTimeInterval(120)
        XCTAssertEqual(status.retryCountdownText(now: now)?.hasPrefix("Повтор в "), true)

        status.nextRetryAt = now.addingTimeInterval(-1)
        XCTAssertEqual(status.retryCountdownText(now: now), "Повтор сейчас")
    }
}
