import SwiftUI

/// Upload queue panel: what is waiting, what was delivered, what failed, and
/// the actions a user needs without opening logs or the spool directory.
struct UploadQueueView: View {
    @Environment(AppState.self) private var appState

    private var status: UploadStatus { appState.uploadStatus }

    private var appNeedsAuthentication: Bool {
        appState.requiresReauthentication
    }

    private var uploadNeedsAuthentication: Bool {
        status.requiresAuthentication || status.missingToken
    }

    private var needsAuthentication: Bool {
        appNeedsAuthentication || uploadNeedsAuthentication
    }

    private var stateTitle: String {
        if uploadNeedsAuthentication {
            return status.stateTitle
        }
        if appNeedsAuthentication {
            return "Нужен новый токен"
        }
        return status.stateTitle
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            metrics

            if status.isEmpty {
                emptyState
            }

            if let error = status.lastError {
                errorBanner(error)
            }

            footer
            actions
        }
        .padding(14)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("upload.panel")
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 8) {
            Label("Загрузка", systemImage: "arrow.up.circle.fill")
                .font(.headline)
            Spacer(minLength: 8)
            HStack(spacing: 5) {
                if status.isUploading {
                    ProgressView()
                        .controlSize(.mini)
                }
                Text(stateTitle)
                    .font(.caption.weight(.medium))
                    .accessibilityIdentifier("upload.state")
                    .accessibilityLabel(stateTitle)
            }
            .foregroundStyle(stateColor)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(stateColor.opacity(0.12), in: Capsule())
        }
    }

    private var stateColor: Color {
        if needsAuthentication || status.failed > 0 || status.lastError != nil {
            return .red
        }
        if status.isUploading {
            return .accentColor
        }
        if status.pending > 0 || status.nextRetryAt != nil {
            return .orange
        }
        return .secondary
    }

    // MARK: - Metrics

    private var metrics: some View {
        HStack(spacing: 8) {
            metric(
                title: "В очереди",
                value: status.pending,
                systemImage: "tray.full",
                tint: status.pending > 0 ? .orange : .secondary,
                identifier: "upload.pendingValue"
            )
            metric(
                title: "Отправлено",
                value: status.sent,
                systemImage: "checkmark.circle",
                tint: status.sent > 0 ? .green : .secondary,
                identifier: "upload.sentValue",
                help: "За текущую сессию загрузки"
            )
            metric(
                title: "Ошибки",
                value: status.failed,
                systemImage: "exclamationmark.triangle",
                tint: status.failed > 0 ? .red : .secondary,
                identifier: "upload.failedValue"
            )
        }
    }

    private func metric(
        title: String,
        value: Int,
        systemImage: String,
        tint: Color,
        identifier: String,
        help: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Image(systemName: systemImage)
                    .font(.caption2)
                    .accessibilityHidden(true)
                Text(title)
                    .font(.caption)
            }
            .foregroundStyle(.secondary)

            Text("\(value)")
                .font(.title2.monospacedDigit().weight(.semibold))
                .foregroundStyle(tint)
                .accessibilityIdentifier(identifier)
                .accessibilityLabel("\(title): \(value)")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(.background.opacity(0.55), in: RoundedRectangle(cornerRadius: 8))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
        .help(help ?? title)
    }

    // MARK: - Empty and error

    private var emptyState: some View {
        HStack(spacing: 10) {
            Image(systemName: "tray")
                .font(.title3)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text("Пока нечего отправлять")
                    .font(.callout.weight(.medium))
                    .accessibilityIdentifier("upload.empty")
                Text("Новые сессии OpenCode появятся здесь автоматически.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(10)
        .background(.background.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .contain)
    }

    private func errorBanner(_ error: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.octagon.fill")
                .foregroundStyle(.red)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text("Последняя ошибка")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.red)
                Text(error)
                    .font(.caption)
                    .lineLimit(4)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("upload.error")
                    .help(error)
            }
            Spacer(minLength: 0)
        }
        .padding(10)
        .background(Color.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .strokeBorder(Color.red.opacity(0.25), lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
    }

    // MARK: - Footer and actions

    @ViewBuilder
    private var footer: some View {
        if status.lastSuccessAt != nil || status.nextRetryAt != nil {
            HStack(spacing: 12) {
                if let lastSuccessAt = status.lastSuccessAt {
                    Label(
                        "Последняя отправка: \(lastSuccessAt.formatted(date: .omitted, time: .standard))",
                        systemImage: "clock"
                    )
                    .accessibilityIdentifier("upload.lastSuccess")
                }
                Spacer(minLength: 0)
                if status.nextRetryAt != nil {
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        if let countdown = status.retryCountdownText(now: context.date) {
                            Label(countdown, systemImage: "timer")
                                .accessibilityIdentifier("upload.retryCountdown")
                        }
                    }
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
    }

    private var actions: some View {
        HStack(spacing: 8) {
            if uploadNeedsAuthentication {
                Button {
                    appState.presentOnboardingForRotation()
                } label: {
                    Label("Повернуть токены…", systemImage: "key")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .accessibilityIdentifier("main.rotateTokens")
            } else {
                Button {
                    appState.retryUploadsNow()
                } label: {
                    Label("Отправить сейчас", systemImage: "arrow.clockwise")
                }
                .controlSize(.small)
                .disabled(status.isUploading)
                .accessibilityIdentifier("main.retryUploads")
            }

            if appNeedsAuthentication && !uploadNeedsAuthentication {
                Button {
                    appState.presentOnboardingForRotation()
                } label: {
                    Label("Повернуть токены…", systemImage: "key")
                }
                .controlSize(.small)
                .accessibilityIdentifier("main.rotateTokens")
            }

            if status.failed > 0 {
                Button(role: .destructive) {
                    appState.clearFailedUploads()
                } label: {
                    Label("Очистить failed", systemImage: "trash")
                }
                .controlSize(.small)
                .accessibilityIdentifier("main.clearFailed")
                .help("Удалить файлы из spool/failed")
            }

            Spacer(minLength: 0)
        }
    }
}
