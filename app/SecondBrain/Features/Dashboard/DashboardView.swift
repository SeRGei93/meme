import SwiftUI

struct DashboardView: View {
    @Environment(AppState.self) private var appState
    @State private var model = DashboardModel()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                content
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { await reload() }
        .onChange(of: appState.serverURL) { _, _ in
            Task { await reload() }
        }
        .onChange(of: appState.tokenRevision) { _, _ in
            Task { await reload() }
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Обзор")
                    .font(.largeTitle.bold())
                Text("Память, очередь и последняя активность")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if let updated = model.lastUpdated {
                Text("Обновлено \(DateDisplay.text(updated))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("dashboard.updatedAt")
            }
            Button {
                Task { await reload() }
            } label: {
                Label("Обновить", systemImage: "arrow.clockwise")
            }
            .disabled(model.isLoading)
            .accessibilityIdentifier("dashboard.refresh")
        }
    }

    @ViewBuilder
    private var content: some View {
        if let stats = model.stats {
            if let error = model.error {
                InlineErrorText(message: error, identifier: "dashboard.error")
            }
            if stats.records.total == 0 {
                emptyBanner
            }
            metricGrid(stats)
            localQueue
            lastActivity(stats)
            ingestionErrors
        } else if model.isLoading {
            ProgressView("Загружаем статистику…")
                .frame(maxWidth: .infinity, minHeight: 260)
                .accessibilityIdentifier("dashboard.loading")
        } else {
            if let error = model.error {
                ErrorPaneView(
                    title: "Не удалось загрузить обзор",
                    message: error,
                    identifierPrefix: "dashboard"
                ) {
                    Task { await model.retry() }
                }
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, minHeight: 260)
                    .accessibilityIdentifier("dashboard.loading")
            }
            // Failed jobs are fetched independently of stats; keep them visible
            // when the aggregate endpoint is down.
            if model.jobsError != nil || !model.failedJobs.isEmpty {
                ingestionErrors
            }
        }
    }

    private var emptyBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "brain.head.profile")
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text("Память пока пуста")
                    .font(.headline)
                Text("Подключите агента с токеном — записи появятся здесь после первой синхронизации.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 10))
        .accessibilityIdentifier("dashboard.empty")
    }

    private func metricGrid(_ stats: Stats) -> some View {
        LazyVGrid(
            columns: [GridItem(.adaptive(minimum: 190), spacing: 12)],
            alignment: .leading,
            spacing: 12
        ) {
            DashboardMetricCard(
                title: "Записи",
                value: "\(stats.records.total)",
                detail: "\(stats.records.final) финальных · \(stats.records.draft) черновиков",
                systemImage: "brain",
                tint: .blue,
                identifier: "dashboard.recordsTotal"
            )
            DashboardMetricCard(
                title: "Проекты",
                value: "\(stats.projects)",
                detail: "С записями и без",
                systemImage: "folder",
                tint: .orange,
                identifier: "dashboard.projectsTotal"
            )
            DashboardMetricCard(
                title: "Клиенты",
                value: "\(stats.clients)",
                detail: "Активных токенов",
                systemImage: "key",
                tint: .purple,
                identifier: "dashboard.clientsTotal"
            )
            DashboardMetricCard(
                title: "Очередь сервера",
                value: "\(stats.jobs.queued)",
                detail: "\(stats.jobs.running) в работе · \(stats.jobs.failed) ошибок",
                systemImage: "tray.full",
                tint: stats.jobs.failed > 0 ? .red : .green,
                identifier: "dashboard.queueQueued"
            )
        }
    }

    private var localQueue: some View {
        let status = appState.uploadStatus
        return DashboardPanel(title: "Локальная очередь", systemImage: "arrow.up.circle") {
            HStack(spacing: 18) {
                QueueCounterView(
                    title: "Ожидают",
                    value: status.pending,
                    identifier: "dashboard.uploadPending"
                )
                QueueCounterView(
                    title: "Отправлено",
                    value: status.sent,
                    identifier: "dashboard.uploadSent"
                )
                QueueCounterView(
                    title: "Ошибки",
                    value: status.failed,
                    identifier: "dashboard.uploadFailed"
                )
                Spacer(minLength: 0)
                Button("Отправить сейчас") {
                    appState.retryUploadsNow()
                }
                .controlSize(.small)
                .disabled(status.isUploading)
                .accessibilityIdentifier("dashboard.retryUploads")
            }

            if let error = status.lastError {
                InlineErrorText(message: error, identifier: "dashboard.uploadError")
            }
            if status.isEmpty {
                Text("Локальная очередь пуста")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("dashboard.uploadEmpty")
            }
        }
    }

    private func lastActivity(_ stats: Stats) -> some View {
        DashboardPanel(title: "Последняя активность", systemImage: "clock") {
            StatusRowView(
                title: "Последняя запись на сервере",
                value: DateDisplay.text(stats.lastIngestAt),
                identifier: "dashboard.lastIngest"
            )
            StatusRowView(
                title: "Последняя успешная загрузка",
                value: DateDisplay.text(appState.uploadStatus.lastSuccessAt),
                identifier: "dashboard.lastUpload"
            )
            StatusRowView(
                title: "База данных",
                value: "\(stats.database.status) · схема \(stats.database.schemaVersion) · \(byteText(stats.database.sizeBytes))",
                identifier: "dashboard.database"
            )
        }
    }

    private var ingestionErrors: some View {
        DashboardPanel(
            title: "Ошибки ingestion",
            systemImage: "exclamationmark.triangle",
            badge: model.failedJobs.isEmpty ? nil : "\(model.failedJobs.count)"
        ) {
            if let jobsError = model.jobsError {
                InlineErrorText(message: jobsError, identifier: "dashboard.jobsError")
                Button("Повторить") {
                    Task { await reload() }
                }
                .controlSize(.small)
                .accessibilityIdentifier("dashboard.jobsRetry")
            } else if model.failedJobs.isEmpty {
                Text("Ошибок ingestion нет")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("dashboard.errorsEmpty")
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.failedJobs) { job in
                        FailedJobRow(job: job)
                    }
                }
            }
        }
    }

    private func byteText(_ bytes: Int) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file)
    }

    private func reload() async {
        model.setClient(appState.makeReadClient())
        await model.load()
    }
}

private struct DashboardMetricCard: View {
    let title: String
    let value: String
    let detail: String
    let systemImage: String
    let tint: Color
    let identifier: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Image(systemName: systemImage)
                    .foregroundStyle(tint)
                    .accessibilityHidden(true)
                Text(title)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            Text(value)
                .font(.system(size: 30, weight: .semibold, design: .rounded).monospacedDigit())
                .accessibilityIdentifier(identifier)
            Text(detail)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
        .accessibilityElement(children: .contain)
    }
}

private struct FailedJobRow: View {
    let job: IngestJob

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Image(systemName: "xmark.octagon.fill")
                    .foregroundStyle(.red)
                    .accessibilityHidden(true)
                Text(job.externalID)
                    .font(.callout.monospaced())
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 0)
                Text(DateDisplay.text(job.updatedAt))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            if let error = job.error, !error.isEmpty {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text("Сервер не вернул текст ошибки")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: 10) {
                Text("job \(job.id)")
                Text("попыток: \(job.attempts)")
                if let recordID = job.recordID {
                    Text("record \(recordID)")
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.red.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("dashboard.failedJob.\(job.id)")
    }
}
