import SwiftUI
import XCTest
@testable import SecondBrain

final class UploadQueuePresentationTests: XCTestCase {
    func testEmptyQueueLooksNeutral() {
        let presentation = UploadQueuePresentation(
            status: UploadStatus(),
            requiresReauthentication: false
        )

        XCTAssertFalse(presentation.needsAuthentication)
        XCTAssertEqual(presentation.title, "Очередь пуста")
        XCTAssertEqual(presentation.systemImage, "brain.head.profile")
        XCTAssertEqual(presentation.tint, .secondary)
        XCTAssertEqual(presentation.accessibilityStatus, "очередь пуста")
    }

    func testPendingQueueUsesUploadSymbol() {
        var status = UploadStatus()
        status.pending = 2

        let presentation = UploadQueuePresentation(
            status: status,
            requiresReauthentication: false
        )

        XCTAssertEqual(presentation.title, "Ожидает отправки")
        XCTAssertEqual(presentation.systemImage, "arrow.up.circle")
        XCTAssertEqual(presentation.tint, .orange)
        XCTAssertEqual(presentation.accessibilityStatus, "2 в очереди")
    }

    func testUploadingWinsOverPending() {
        var status = UploadStatus()
        status.pending = 2
        status.isUploading = true

        let presentation = UploadQueuePresentation(
            status: status,
            requiresReauthentication: false
        )

        XCTAssertEqual(presentation.title, "Отправка…")
        XCTAssertEqual(presentation.systemImage, "arrow.up.circle")
        XCTAssertEqual(presentation.tint, .accentColor)
        XCTAssertEqual(presentation.accessibilityStatus, "идёт отправка")
    }

    func testScheduledRetryIsOrange() {
        var status = UploadStatus()
        status.nextRetryAt = Date().addingTimeInterval(60)

        let presentation = UploadQueuePresentation(
            status: status,
            requiresReauthentication: false
        )

        XCTAssertEqual(presentation.title, "Ждёт повтора")
        XCTAssertEqual(presentation.tint, .orange)
        XCTAssertEqual(presentation.accessibilityStatus, "ждёт повтора")
    }

    func testFailuresUseRussianPluralForms() {
        for (count, expected) in [
            (1, "1 ошибка загрузки"),
            (3, "3 ошибки загрузки"),
            (11, "11 ошибок загрузки"),
            (21, "21 ошибка загрузки"),
        ] {
            var status = UploadStatus()
            status.failed = count

            let presentation = UploadQueuePresentation(
                status: status,
                requiresReauthentication: false
            )

            XCTAssertEqual(presentation.title, "Есть ошибки")
            XCTAssertEqual(presentation.systemImage, "exclamationmark.circle")
            XCTAssertEqual(presentation.tint, .red)
            XCTAssertEqual(presentation.accessibilityStatus, expected)
        }
    }

    func testMissingOpenCodeTokenIsNotSilentlyEmpty() {
        var status = UploadStatus()
        status.missingToken = true

        let presentation = UploadQueuePresentation(
            status: status,
            requiresReauthentication: false
        )

        XCTAssertTrue(presentation.needsAuthentication)
        XCTAssertEqual(presentation.title, "Нужен токен OpenCode")
        XCTAssertEqual(presentation.systemImage, "exclamationmark.triangle")
        XCTAssertEqual(presentation.tint, .red)
        XCTAssertEqual(presentation.accessibilityStatus, "нужен новый токен")
    }

    func testReauthenticationOverridesServerState() {
        var status = UploadStatus()
        status.requiresAuthentication = true

        let presentation = UploadQueuePresentation(
            status: status,
            requiresReauthentication: true
        )

        XCTAssertTrue(presentation.needsAuthentication)
        XCTAssertEqual(presentation.title, "Нужен новый токен")
        XCTAssertEqual(presentation.systemImage, "exclamationmark.triangle")
        XCTAssertEqual(presentation.accessibilityStatus, "нужен новый токен")
    }
}
