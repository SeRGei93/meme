import AppKit
import SwiftUI

struct SettingsView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.openWindow) private var openWindow
    @State private var launchAtLogin = LaunchAtLogin()
    @State private var serverURLText = ""
    @State private var spoolPathText = ""

    var body: some View {
        Form {
            Section("Server") {
                TextField("URL сервера", text: $serverURLText, prompt: Text("https://…"))
                    .textContentType(.URL)
                    .accessibilityIdentifier("settings.serverURL")

                HStack {
                    Button("Сохранить URL") {
                        Task {
                            if await appState.updateServerURL(serverURLText) {
                                serverURLText = appState.serverURL?.absoluteString ?? serverURLText
                            }
                        }
                    }
                    .accessibilityIdentifier("settings.saveServerURL")

                    Spacer()

                    Text(appState.statusText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                HStack {
                    Text("MCP config")
                    Spacer()
                    Text(mcpStatusText)
                        .foregroundStyle(mcpStatusColor)
                        .accessibilityIdentifier("settings.mcpStatus")
                        .accessibilityLabel(mcpStatusText)
                }
                if let file = appState.mcpStatus?.fileURL {
                    Text(file.path)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }

            Section("Startup") {
                Toggle(
                    "Launch at login",
                    isOn: Binding(
                        get: { launchAtLogin.isEnabled },
                        set: { launchAtLogin.setEnabled($0) }
                    )
                )
                .accessibilityIdentifier("settings.launchAtLogin")

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

            Section("OpenCode") {
                HStack {
                    Text("Плагин")
                    Spacer()
                    Text(pluginStatusText)
                        .foregroundStyle(pluginStatusColor)
                        .accessibilityIdentifier("settings.pluginStatus")
                        .accessibilityLabel(pluginStatusText)
                }
                LabeledContent("Путь", value: appState.pluginStatus?.directory.path ?? "—")

                HStack {
                    Button(installButtonTitle) {
                        _ = appState.installOpenCodeIntegration()
                    }
                    .disabled(appState.integrationBusy)
                    .accessibilityIdentifier("settings.installPlugin")

                    Button("Обновить статус") {
                        appState.refreshIntegrationStatus()
                    }
                    .disabled(appState.integrationBusy)

                    if appState.integrationBusy {
                        ProgressView()
                            .controlSize(.small)
                    }

                    Spacer()
                }

                if let notice = appState.settingsNotice {
                    Text(notice)
                        .font(.caption)
                        .foregroundStyle(.green)
                        .accessibilityIdentifier("settings.notice")
                }
                if let error = appState.settingsError {
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("settings.error")
                }
            }

            Section("Tokens") {
                tokenRow("OpenCode", account: KeychainStore.Account.opencode)
                tokenRow("Jarvis", account: KeychainStore.Account.jarvis)
                tokenRow("Second Brain app", account: KeychainStore.Account.ui)

                Button("Повернуть токены…") {
                    appState.presentOnboardingForRotation()
                    openWindow(id: "main")
                    NSApp.activate(ignoringOtherApps: true)
                }
                .disabled(appState.onboardingBusy)

                Text("Ротация создаёт новые клиенты, проверяет их и только потом отзывает старые.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Capture") {
                TextField("Spool", text: $spoolPathText, prompt: Text("~/.config/second-brain/spool"))
                    .accessibilityIdentifier("settings.spoolPath")

                HStack {
                    Button("Выбрать…") {
                        chooseSpoolDirectory()
                    }

                    Button("Сохранить путь") {
                        if appState.updateSpoolPath(spoolPathText) {
                            spoolPathText = appState.spoolPathText
                        }
                    }
                    .accessibilityIdentifier("settings.saveSpoolPath")
                }
            }

            Section("Status") {
                Button("Проверить соединение") {
                    Task {
                        if let url = appState.serverURL {
                            await appState.checkHealth(using: APIClient(baseURL: url))
                            appState.refreshIntegrationStatus()
                        }
                    }
                }
                .accessibilityIdentifier("settings.checkConnection")

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
        .frame(width: 600, height: 700)
        .onAppear {
            launchAtLogin.refresh()
            serverURLText = appState.serverURL?.absoluteString ?? ""
            spoolPathText = appState.spoolPathText
            appState.refreshIntegrationStatus()
        }
        .onChange(of: appState.serverURL) { _, newValue in
            serverURLText = newValue?.absoluteString ?? ""
        }
    }

    private func tokenRow(_ title: String, account: String) -> some View {
        LabeledContent(title) {
            let present = appState.tokenPresence[account] == true
            Text(present ? "Есть" : "Нет")
                .foregroundStyle(present ? .green : .orange)
                .accessibilityIdentifier("settings.token.\(account)")
        }
    }

    private var installButtonTitle: String {
        switch appState.pluginStatus?.state {
        case .installed: return "Переустановить"
        case .outdated: return "Обновить"
        default: return "Установить"
        }
    }

    private var pluginStatusText: String {
        switch appState.pluginStatus?.state {
        case .installed: return "Установлен"
        case .notInstalled: return "Не установлен"
        case .outdated: return "Нужно обновить"
        case .error(let message): return "Ошибка: \(message)"
        case nil: return "Проверка…"
        }
    }

    private var pluginStatusColor: Color {
        switch appState.pluginStatus?.state {
        case .installed: return .green
        case .notInstalled, .outdated: return .orange
        case .error: return .red
        case nil: return .secondary
        }
    }

    private var mcpStatusText: String {
        switch appState.mcpStatus?.state {
        case .configured: return "Настроен"
        case .notConfigured: return "Не настроен"
        case .error(let message): return "Ошибка: \(message)"
        case nil: return "Проверка…"
        }
    }

    private var mcpStatusColor: Color {
        switch appState.mcpStatus?.state {
        case .configured: return .green
        case .notConfigured: return .orange
        case .error: return .red
        case nil: return .secondary
        }
    }

    private func chooseSpoolDirectory() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Выбрать"
        if panel.runModal() == .OK, let url = panel.url {
            spoolPathText = url.path
        }
    }
}
