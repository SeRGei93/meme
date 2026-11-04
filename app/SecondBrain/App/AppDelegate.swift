import AppKit

/// The app lives in the menu bar. Closing the main window must not terminate
/// it, otherwise the menu bar item and the background upload queue disappear.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
    }
}
