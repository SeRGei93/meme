import AppKit
import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.openWindow) private var openWindow
    @State private var launchAtLogin = LaunchAtLogin()

    var body: some View {
        Form {
            Section("General") {
                Toggle(
                    "Launch at login",
                    isOn: Binding(
                        get: { launchAtLogin.isEnabled },
                        set: { launchAtLogin.setEnabled($0) }
                    )
                )

                if launchAtLogin.requiresApproval {
                    Text("Требуется подтверждение в System Settings → General → Login Items.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let errorMessage = launchAtLogin.errorMessage {
                    Text(errorMessage)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }

            Section("Connection") {
                LabeledContent("Server", value: appState.serverURL?.absoluteString ?? "Не настроен")

                Button("Повернуть токены…") {
                    appState.presentOnboardingForRotation()
                    openWindow(id: "main")
                    NSApp.activate()
                }
            }

            Section("Status") {
                LabeledContent("State", value: appState.statusText)
                if let health = appState.health {
                    LabeledContent("Version", value: health.version)
                    LabeledContent("Schema", value: String(health.schemaVersion))
                    LabeledContent("FTS5", value: health.database.fts5 ? "Yes" : "No")
                }
                if let lastSync = appState.lastSync {
                    LabeledContent("Last sync", value: lastSync.formatted(date: .abbreviated, time: .standard))
                }
                if appState.requiresReauthentication {
                    Text("Токен недействителен. Нужен новый токен в настройках.")
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 480, height: 280)
        .onAppear {
            launchAtLogin.refresh()
        }
    }
}
