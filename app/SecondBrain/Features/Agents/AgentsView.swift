import SwiftUI

struct AgentsView: View {
    @Environment(AppState.self) private var appState
    @State private var model = AgentsModel()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                content
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task {
            await reload()
        }
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
                Text("Агенты")
                    .font(.largeTitle.bold())
                Text("Статус OpenCode и Jarvis: последняя активность, токены и плагин.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                appState.refreshIntegrationStatus()
            } label: {
                Label("Обновить", systemImage: "arrow.clockwise")
            }
            .disabled(model.isLoading)
            .accessibilityIdentifier("agents.refresh")
        }
    }

    @ViewBuilder
    private var content: some View {
        if model.opencode == nil && model.jarvis == nil {
            if model.isLoading {
                ProgressView("Проверяем агентов…")
                    .frame(maxWidth: .infinity, minHeight: 280)
                    .accessibilityIdentifier("agents.loading")
            } else if let error = model.error {
                ErrorPaneView(
                    title: "Не удалось загрузить статус агентов",
                    message: error,
                    identifierPrefix: "agents"
                ) {
                    Task { await model.retry() }
                }
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, minHeight: 280)
                    .accessibilityIdentifier("agents.loading")
            }
        } else {
            if let error = model.error {
                InlineErrorText(message: error, identifier: "agents.error")
            }
            if let jobsError = model.jobsError {
                InlineErrorText(message: "Не удалось загрузить очередь: \(jobsError)", identifier: "agents.jobsError")
            }
            if let clientsError = model.clientsError {
                InlineErrorText(message: "Не удалось загрузить клиентов: \(clientsError)", identifier: "agents.clientsError")
            }
            if isIdle {
                Text("Активности пока нет — агенты ещё не присылали записи.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("agents.empty")
            }
            HStack(alignment: .top, spacing: 12) {
                opencodeCard
                jarvisCard
            }
            queueCard
        }
    }

    private var opencodeCard: some View {
        DashboardPanel(title: "OpenCode", systemImage: "terminal") {
            if let summary = model.opencode {
                StatusRowView(
                    title: "Записей",
                    value: "\(summary.recordCount)",
                    identifier: "agents.opencode.records"
                )
                StatusRowView(
                    title: "Последняя запись",
                    value: lastActivityText(summary),
                    identifier: "agents.opencode.lastCapture"
                )
                if let externalID = summary.lastExternalID {
                    Text(externalID)
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .accessibilityIdentifier("agents.opencode.lastID")
                }
                if let status = summary.lastJobStatus {
                    StatusRowView(
                        title: "Последняя job",
                        value: status.title,
                        identifier: "agents.opencode.lastStatus"
                    )
                }
                StatusRowView(
                    title: "Последняя загрузка",
                    value: DateDisplay.text(appState.uploadStatus.lastSuccessAt),
                    identifier: "agents.opencode.lastUpload"
                )
                tokenRow(
                    present: appState.tokenPresence[KeychainStore.Account.opencode] == true,
                    summary: summary,
                    identifier: "agents.opencode.token"
                )
                StatusRowView(
                    title: "Плагин",
                    value: pluginText,
                    tint: pluginColor,
                    identifier: "agents.opencode.plugin"
                )
                StatusRowView(
                    title: "MCP",
                    value: mcpText,
                    tint: mcpColor,
                    identifier: "agents.opencode.mcp"
                )
            } else {
                Text("Нет данных")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var jarvisCard: some View {
        DashboardPanel(title: "Jarvis", systemImage: "sparkles") {
            if let summary = model.jarvis {
                StatusRowView(
                    title: "Записей",
                    value: "\(summary.recordCount)",
                    identifier: "agents.jarvis.records"
                )
                StatusRowView(
                    title: "Последняя память",
                    value: lastActivityText(summary),
                    identifier: "agents.jarvis.lastMemory"
                )
                if let externalID = summary.lastExternalID {
                    Text(externalID)
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .accessibilityIdentifier("agents.jarvis.lastID")
                }
                tokenRow(
                    present: appState.tokenPresence[KeychainStore.Account.jarvis] == true,
                    summary: summary,
                    identifier: "agents.jarvis.token"
                )
            } else {
                Text("Нет данных")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var queueCard: some View {
        DashboardPanel(title: "Очередь", systemImage: "tray.full") {
            if let queue = model.queue {
                HStack(spacing: 18) {
                    QueueCounterView(
                        title: "В очереди",
                        value: queue.queued,
                        identifier: "agents.queue.queued"
                    )
                    QueueCounterView(
                        title: "В работе",
                        value: queue.running,
                        identifier: "agents.queue.running"
                    )
                    QueueCounterView(
                        title: "Готово",
                        value: queue.done,
                        identifier: "agents.queue.done"
                    )
                    QueueCounterView(
                        title: "Ошибки",
                        value: queue.failed,
                        identifier: "agents.queue.failed"
                    )
                    Spacer(minLength: 0)
                }
            }
            StatusRowView(
                title: "Локальная очередь",
                value: "\(appState.uploadStatus.pending) ждут · \(appState.uploadStatus.failed) ошибок",
                identifier: "agents.localQueue"
            )
            Button("Отправить сейчас") {
                appState.retryUploadsNow()
            }
            .controlSize(.small)
            .disabled(appState.uploadStatus.isUploading)
            .accessibilityIdentifier("agents.retryUploads")
        }
    }

    private func tokenRow(
        present: Bool,
        summary: AgentSummary,
        identifier: String
    ) -> some View {
        let text: String
        let color: Color
        if !present {
            text = "Нет токена"
            color = .orange
        } else if model.clientsError != nil {
            text = "Токен есть, сервер не подтвердил"
            color = .orange
        } else if summary.activeClientNames.isEmpty {
            if summary.revokedClientCount > 0 {
                text = "Токен отозван на сервере"
                color = .red
            } else {
                text = "Токен есть, активный клиент не найден"
                color = .orange
            }
        } else {
            text = "Есть · \(summary.activeClientNames.joined(separator: ", "))"
            color = .green
        }
        return StatusRowView(
            title: "Токен",
            value: text,
            tint: color,
            identifier: identifier
        )
    }

    private func lastActivityText(_ summary: AgentSummary) -> String {
        if summary.lastActivityAt != nil {
            return DateDisplay.text(summary.lastActivityAt)
        }
        if summary.recordCount > 0 {
            return "Нет данных о последней активности"
        }
        return "Нет данных"
    }

    private var isIdle: Bool {
        (model.opencode?.recordCount ?? 0) == 0
            && (model.jarvis?.recordCount ?? 0) == 0
            && (model.queue?.queued ?? 0) == 0
            && (model.queue?.running ?? 0) == 0
    }

    private var pluginText: String {
        switch appState.pluginStatus?.state {
        case .installed: return "Установлен"
        case .notInstalled: return "Не установлен"
        case .outdated: return "Нужно обновить"
        case .error(let message): return "Ошибка: \(message)"
        case nil: return "Неизвестно"
        }
    }

    private var pluginColor: Color {
        switch appState.pluginStatus?.state {
        case .installed: return .green
        case .notInstalled, .outdated: return .orange
        case .error: return .red
        case nil: return .secondary
        }
    }

    private var mcpText: String {
        switch appState.mcpStatus?.state {
        case .configured: return "Настроен"
        case .notConfigured: return "Не настроен"
        case .error(let message): return "Ошибка: \(message)"
        case nil: return "Неизвестно"
        }
    }

    private var mcpColor: Color {
        switch appState.mcpStatus?.state {
        case .configured: return .green
        case .notConfigured: return .orange
        case .error: return .red
        case nil: return .secondary
        }
    }

    private func reload() async {
        model.setClient(appState.makeReadClient())
        await model.load()
    }
}
