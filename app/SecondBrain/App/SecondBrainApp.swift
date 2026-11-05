import AppKit
import SwiftUI

@main
struct SecondBrainApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var appState = AppState()

    var body: some Scene {
        WindowGroup("Second Brain", id: "main") {
            ContentView()
                .environment(appState)
        }
        .defaultSize(width: 1180, height: 760)
        .commands {
            CommandGroup(replacing: .newItem) {}
            CommandMenu("Поиск") {
                Button("Найти…") {
                    NotificationCenter.default.post(name: .focusMemorySearch, object: nil)
                }
                .keyboardShortcut("k", modifiers: .command)
            }
        }

        MenuBarExtra {
            MenuBarContent()
                .environment(appState)
        } label: {
            Label("Second Brain", systemImage: "brain.head.profile")
        }

        Settings {
            SettingsView()
                .environment(appState)
        }
    }
}

private struct MenuBarContent: View {
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Button("Open Second Brain") {
            openWindow(id: "main")
            NSApp.activate(ignoringOtherApps: true)
        }

        SettingsLink {
            Text("Settings…")
        }

        Divider()

        Button("Quit Second Brain") {
            NSApp.terminate(nil)
        }
        .keyboardShortcut("q")
    }
}
