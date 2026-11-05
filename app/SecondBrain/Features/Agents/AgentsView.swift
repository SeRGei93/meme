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
                Text("Источники, клиенты и последняя активность. Каждый агент приходит со своим токеном.")
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
        if model.summaries.isEmpty {
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
            } else if model.clientsError != nil {
                ErrorPaneView(
                    title: "Не удалось загрузить клиентов",
                    message: model.clientsError ?? "",
                    identifierPrefix: "agents"
                ) {
                    Task { await model.retry() }
                }
            } else {
                Text("Источников пока нет — выпустите токен агенту через admin API.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 200)
                    .accessibilityIdentifier("agents.empty")
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

            LazyVGrid(
                columns: [GridItem(.adaptive(minimum: 320), spacing: 12)],
                alignment: .leading,
                spacing: 12
            ) {
                ForEach(model.summaries) { summary in
                    agentCard(summary)
                }
            }
            queueCard
        }
    }

    private func agentCard(_ summary: AgentSummary) -> some View {
        let prefix = "agents.source.\(summary.source)"
        let token = tokenStatus(for: summary)

        return DashboardPanel(
            title: summary.title,
            systemImage: summary.isLocalOpenCode ? "terminal" : "person.crop.circle.badge.checkmark"
        ) {
            StatusRowView(
                title: "Записей",
                value: "\(summary.recordCount)",
                identifier: "\(prefix).records"
            )
            StatusRowView(
                title: "Последняя активность",
                value: lastActivityText(summary),
                identifier: "\(prefix).lastActivity"
            )
            if let externalID = summary.lastExternalID {
                Text(externalID)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .accessibilityIdentifier("\(prefix).lastID")
            }
            if let status = summary.lastJobStatus {
                StatusRowView(
                    title: "Последняя job",
                    value: status.title,
                    identifier: "\(prefix).lastStatus"
                )
            }
            StatusRowView(
                title: "Клиенты",
                value: clientText(summary),
                tint: summary.activeClientNames.isEmpty && summary.revokedClientCount > 0 ? .red : .primary,
                identifier: "\(prefix).clients"
            )
            StatusRowView(
                title: "Токен",
                value: token.text,
                tint: token.color,
                identifier: "\(prefix).token"
            )

            if summary.isLocalOpenCode {
                StatusRowView(
                    title: "Последняя загрузка",
                    value: DateDisplay.text(appState.uploadStatus.lastSuccessAt),
                    identifier: "\(prefix).lastUpload"
                )
                StatusRowView(
                    title: "Плагин",
                    value: pluginText,
                    tint: pluginColor,
                    identifier: "\(prefix).plugin"
                )
                StatusRowView(
                    title: "MCP",
                    value: mcpText,
                    tint: mcpColor,
                    identifier: "\(prefix).mcp"
                )
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

    private func tokenStatus(for summary: AgentSummary) -> (text: String, color: Color) {
        if summary.isLocalOpenCode {
            let present = appState.tokenPresence[KeychainStore.Account.opencode] == true
            if !present {
                return ("Нет токена", .orange)
            }
            if model.clientsError != nil {
                return ("Токен есть, сервер не подтвердил", .orange)
            }
        }

        if !summary.activeClientNames.isEmpty {
            return ("На сервере · \(summary.activeClientNames.joined(separator: ", "))", .green)
        }
        if summary.revokedClientCount > 0 {
            return ("Только отозванные клиенты", .red)
        }
        return ("Нет активного клиента", .orange)
    }

    private func clientText(_ summary: AgentSummary) -> String {
        let active = summary.activeClientNames.joined(separator: ", ")
        if active.isEmpty {
            return summary.revokedClientCount > 0
                ? "Только отозванные (\(summary.revokedClientCount))"
                : "Нет клиентов"
        }
        if summary.revokedClientCount > 0 {
            return "\(active) · отозвано: \(summary.revokedClientCount)"
        }
        return active
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
        model.summaries.allSatisfy { $0.recordCount == 0 }
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
        model.localOpenCodeClient = appState.localAppClients["opencode"]
        await model.load()
    }
}
