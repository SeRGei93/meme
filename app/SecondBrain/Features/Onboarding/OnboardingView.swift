import SwiftUI

/// First-launch setup and the "rotate tokens" flow. The admin token is used
/// once and never persisted; only the issued client tokens go to the Keychain.
struct OnboardingView: View {
    @Environment(AppState.self) private var appState
    @State private var serverURLText = ""
    @State private var adminToken = ""
    @State private var didPrefill = false

    private var canSubmit: Bool {
        !appState.onboardingBusy
            && !serverURLText.trimmingCharacters(in: .whitespaces).isEmpty
            && !adminToken.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VStack(alignment: .leading, spacing: 6) {
                Text(appState.isConfigured ? "Обновить подключение" : "Подключение к Second Brain")
                    .font(.title2.bold())
                Text("Укажите адрес сервера и bootstrap admin token. Приложение создаст три клиента: opencode, jarvis и ui.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            VStack(alignment: .leading, spacing: 12) {
                TextField("Server URL", text: $serverURLText, prompt: Text("https://brain.example.com"))
                    .textFieldStyle(.roundedBorder)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("onboarding.serverURL")

                SecureField("Admin token", text: $adminToken, prompt: Text("BRAIN_ADMIN_TOKEN"))
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("onboarding.adminToken")
            }

            if let error = appState.onboardingError {
                Text(error)
                    .font(.callout)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack(spacing: 12) {
                Button(appState.onboardingBusy ? "Подключение…" : "Connect") {
                    Task {
                        await appState.connect(serverURLText: serverURLText, adminToken: adminToken)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSubmit)
                .accessibilityIdentifier("onboarding.connect")

                if appState.onboardingBusy {
                    ProgressView()
                        .controlSize(.small)
                }

                Spacer()

                if appState.isConfigured {
                    Button("Cancel") {
                        appState.isOnboardingPresented = false
                    }
                }
            }

            Text("Токены сохраняются в Keychain. mcp_token пишется в ~/.config/second-brain/mcp_token с правами 0600.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(28)
        .frame(width: 520)
        .onAppear(perform: prefill)
    }

    private func prefill() {
        guard !didPrefill else { return }
        didPrefill = true
        serverURLText = appState.serverURL?.absoluteString ?? "https://"
    }
}
