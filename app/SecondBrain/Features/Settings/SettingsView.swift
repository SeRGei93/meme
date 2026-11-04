import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
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

            Section("Status") {
                LabeledContent("State", value: appState.statusText)
                if let lastSync = appState.lastSync {
                    LabeledContent("Last sync", value: lastSync.formatted(date: .abbreviated, time: .standard))
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
