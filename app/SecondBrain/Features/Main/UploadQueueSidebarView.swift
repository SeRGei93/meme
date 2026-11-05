import SwiftUI

/// Narrow upload card for the app sidebar. The full queue panel lives with the
/// future dashboard, but the sidebar must still show progress, errors and the
/// two recovery actions without overflowing its column.
struct UploadQueueSidebarView: View {
    @Environment(AppState.self) private var appState

    private var status: UploadStatus { appState.uploadStatus }

    private var queue: UploadQueuePresentation {
        UploadQueuePresentation(
            status: appState.uploadStatus,
            requiresReauthentication: appState.requiresReauthentication
        )
    }

    private var needsAuthentication: Bool { queue.needsAuthentication }

    private var stateTitle: String { queue.title }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Label("Загрузка", systemImage: "arrow.up.circle.fill")
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Spacer(minLength: 4)
                Text(stateTitle)
                    .font(.caption2.weight(.medium))
                    .foregroundStyle(stateColor)
                    .lineLimit(1)
                    .accessibilityIdentifier("upload.state")
                    .accessibilityLabel(stateTitle)
            }

            HStack(spacing: 6) {
                counter(
                    "В очереди", status.pending,
                    tint: status.pending > 0 ? .orange : .secondary,
                    identifier: "upload.pendingValue"
                )
                counter(
                    "Отправлено", status.sent,
                    tint: status.sent > 0 ? .green : .secondary,
                    identifier: "upload.sentValue",
                    help: "За текущую сессию загрузки"
                )
                counter(
                    "Ошибки", status.failed,
                    tint: status.failed > 0 ? .red : .secondary,
                    identifier: "upload.failedValue"
                )
            }

            if status.isEmpty {
                Text("Очередь пуста")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("upload.empty")
            }

            if let error = status.lastError {
                Text(error)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("upload.error")
                    .help(error)
            }

            HStack(spacing: 8) {
                if needsAuthentication {
                    Button {
                        appState.presentOnboardingForRotation()
                    } label: {
                        Label("Токены…", systemImage: "key")
                    }
                    .controlSize(.mini)
                    .accessibilityIdentifier("main.rotateTokens")
                } else {
                    Button {
                        appState.retryUploadsNow()
                    } label: {
                        Label("Отправить", systemImage: "arrow.clockwise")
                    }
                    .controlSize(.mini)
                    .disabled(status.isUploading)
                    .accessibilityIdentifier("main.retryUploads")
                }

                if status.failed > 0 {
                    Button(role: .destructive) {
                        appState.clearFailedUploads()
                    } label: {
                        Image(systemName: "trash")
                    }
                    .controlSize(.mini)
                    .accessibilityIdentifier("main.clearFailed")
                    .accessibilityLabel("Очистить failed")
                    .help("Удалить файлы из spool/failed")
                }

                Spacer(minLength: 0)
            }

            if status.nextRetryAt != nil {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    if let countdown = status.retryCountdownText(now: context.date) {
                        Text(countdown)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .accessibilityIdentifier("upload.retryCountdown")
                    }
                }
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("upload.panel")
    }

    private var stateColor: Color { queue.tint }

    private func counter(
        _ title: String,
        _ value: Int,
        tint: Color,
        identifier: String,
        help: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title)
                .font(.system(size: 9))
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text("\(value)")
                .font(.caption.monospacedDigit().weight(.semibold))
                .foregroundStyle(tint)
                .accessibilityIdentifier(identifier)
                .accessibilityLabel("\(title): \(value)")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .help(help ?? title)
    }
}
