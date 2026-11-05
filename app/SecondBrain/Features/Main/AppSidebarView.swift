import AppKit
import SwiftUI

/// Left column: connection state, upload queue and app actions. Search and
/// reading live in the main detail column.
struct AppSidebarView: View {
    @Environment(AppState.self) private var appState

    var body: some View {
        GeometryReader { proxy in
            ScrollView(.vertical) {
                VStack(alignment: .leading, spacing: 16) {
                    brand
                    status
                    if appState.isConfigured {
                        UploadQueueSidebarView()
                    }
                    if let errorMessage = appState.errorMessage {
                        Text(errorMessage)
                            .font(.caption)
                            .foregroundStyle(.red)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    Spacer(minLength: 8)
                    actions
                }
                .frame(
                    width: max(0, proxy.size.width - 32),
                    alignment: .topLeading
                )
                .frame(
                    minHeight: max(0, proxy.size.height - 32),
                    alignment: .topLeading
                )
                .padding(16)
            }
        }
    }

    private var brand: some View {
        HStack(spacing: 10) {
            Image(systemName: "brain.head.profile")
                .font(.system(size: 28, weight: .light))
                .foregroundStyle(.tint)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                Text("Second Brain")
                    .font(.title3.bold())
                    .lineLimit(1)
                Text("Память для OpenCode и Jarvis")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
    }

    private var status: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(appState.statusText)
                .font(.callout)
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
                .background(.quaternary, in: Capsule())
                .lineLimit(1)
                .accessibilityIdentifier("sidebar.status")

            if let health = appState.health {
                Text("Версия \(health.version) · схема \(health.schemaVersion)")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            Text(appState.integrationSummary)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(3)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("main.integrationSummary")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var actions: some View {
        HStack(spacing: 8) {
            if !appState.isConfigured {
                Button("Настроить…") {
                    appState.presentOnboardingForRotation()
                }
            }
            SettingsLink {
                Text("Settings…")
            }
            Button("Quit") {
                NSApp.terminate(nil)
            }
        }
    }
}
