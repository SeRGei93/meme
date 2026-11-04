import Foundation
import Observation
import ServiceManagement

/// Wraps `SMAppService.mainApp`, macOS 13+. Registration can require the user
/// to approve the app in System Settings → General → Login Items, and can fail
/// for an unsigned development build; both cases are surfaced in Settings.
@Observable
final class LaunchAtLogin {
    private(set) var isEnabled = false
    private(set) var requiresApproval = false
    private(set) var errorMessage: String?

    init() {
        refresh()
    }

    func refresh() {
        let status = SMAppService.mainApp.status
        isEnabled = status == .enabled
        requiresApproval = status == .requiresApproval
        if requiresApproval {
            errorMessage = "Разрешите SecondBrain в System Settings → General → Login Items."
        }
    }

    func setEnabled(_ enabled: Bool) {
        errorMessage = nil
        do {
            if enabled {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
        } catch {
            errorMessage = error.localizedDescription
        }
        refresh()
    }
}
