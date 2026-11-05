import SwiftUI

struct ClientsView: View {
    @Environment(AppState.self) private var appState
    @State private var model = ClientsModel()
    @State private var pendingAction: PendingAction?
    @State private var showCreate = false
    @State private var pendingToken: IssuedToken?
    @State private var issued: IssuedToken?

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
        .alert(
            alertTitle,
            isPresented: Binding(
                get: { pendingAction != nil },
                set: { if !$0 { pendingAction = nil } }
            ),
            presenting: pendingAction
        ) { action in
            switch action {
            case .revoke(let info):
                Button("Отозвать", role: .destructive) {
                    pendingAction = nil
                    Task { await model.revoke(info) }
                }
            case .rotate(let info):
                Button("Ротировать", role: .destructive) {
                    pendingAction = nil
                    Task {
                        if let token = await model.rotate(info) {
                            issued = token
                        }
                    }
                }
            }
            Button("Отмена", role: .cancel) {
                pendingAction = nil
            }
        } message: { action in
            Text(alertMessage(action))
        }
        .sheet(isPresented: $showCreate, onDismiss: presentPendingToken) {
            ClientCreateSheet(model: model) { token in
                pendingToken = token
                showCreate = false
            }
        }
        .sheet(item: $issued) { token in
            TokenIssuedView(issued: token) {
                issued = nil
            }
        }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Клиенты")
                    .font(.largeTitle.bold())
                Text("Доступы к памяти: источник, scopes и статус каждого токена.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            HStack(spacing: 12) {
                Button {
                    model.clearActionError()
                    showCreate = true
                } label: {
                    Label("Новый клиент", systemImage: "plus")
                }
                .disabled(isBusy)
                .accessibilityIdentifier("clients.new")

                Text("Активных: \(model.activeClients.count)")
                    .font(.callout.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("clients.activeCount")
                Text("Отозвано: \(model.revokedClients.count)")
                    .font(.callout.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("clients.revokedCount")
                Button {
                    Task { await model.load() }
                } label: {
                    Label("Обновить", systemImage: "arrow.clockwise")
                }
                .disabled(model.isLoading || isBusy)
                .accessibilityIdentifier("clients.refresh")
            }
        }
    }

    @ViewBuilder
    private var content: some View {
        if model.clients.isEmpty {
            if model.isLoading {
                ProgressView("Загружаем клиентов…")
                    .frame(maxWidth: .infinity, minHeight: 280)
                    .accessibilityIdentifier("clients.loading")
            } else if let error = model.error {
                ErrorPaneView(
                    title: "Не удалось загрузить клиентов",
                    message: error,
                    identifierPrefix: "clients"
                ) {
                    Task { await model.retry() }
                }
            } else {
                emptyState
            }
        } else {
            if let error = model.error {
                InlineErrorText(
                    message: "Список мог устареть: \(error)",
                    identifier: "clients.error"
                )
            }
            if let actionError = model.actionError {
                InlineErrorText(message: actionError, identifier: "clients.actionError")
            }

            if !model.activeClients.isEmpty {
                clientsPanel(
                    title: "Активные",
                    systemImage: "checkmark.shield",
                    clients: model.activeClients
                )
            }
            if !model.revokedClients.isEmpty {
                clientsPanel(
                    title: "Отозванные",
                    systemImage: "xmark.shield",
                    clients: model.revokedClients
                )
            }
        }
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Image(systemName: "key.slash")
                .font(.system(size: 30))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text("Клиентов пока нет")
                .font(.headline)
            Text(
                "Выпустите агента кнопкой «Новый клиент», подключите приложение "
                    + "через onboarding или создайте клиента через CLI."
            )
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 440)
        }
        .frame(maxWidth: .infinity, minHeight: 280)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("clients.empty")
    }

    private func clientsPanel(
        title: String,
        systemImage: String,
        clients: [ClientInfo]
    ) -> some View {
        DashboardPanel(title: title, systemImage: systemImage) {
            VStack(spacing: 0) {
                ForEach(Array(clients.enumerated()), id: \.element.id) { index, client in
                    if index > 0 {
                        Divider()
                    }
                    clientRow(client)
                }
            }
        }
    }

    private func clientRow(_ info: ClientInfo) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(info.name)
                    .font(.callout.weight(.semibold))
                    .accessibilityIdentifier("clients.name.\(info.id)")
                if model.isLocalUI(info) {
                    badge("Это приложение", color: .blue, identifier: "clients.badge.\(info.id)")
                } else if model.isLocalOpenCode(info) {
                    badge("Локальный OpenCode", color: .teal, identifier: "clients.badge.\(info.id)")
                }
                Spacer(minLength: 8)
                statusBadge(info)
            }

            HStack(spacing: 10) {
                Text(kindText(info.kind))
                Text("source: \(info.source ?? "—")")
                    .accessibilityIdentifier("clients.source.\(info.id)")
                if !info.scopes.isEmpty {
                    Text(info.scopes.joined(separator: ", "))
                        .accessibilityIdentifier("clients.scopes.\(info.id)")
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)

            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text("Создан: \(DateDisplay.text(info.createdAt))")
                    .accessibilityIdentifier("clients.createdAt.\(info.id)")
                Text("Активность: \(DateDisplay.text(info.lastSeenAt))")
                    .accessibilityIdentifier("clients.lastSeen.\(info.id)")
                if let revokedAt = info.revokedAt {
                    Text("Отозван: \(DateDisplay.text(revokedAt))")
                        .accessibilityIdentifier("clients.revokedAt.\(info.id)")
                }
                Spacer(minLength: 8)
                actionButton(for: info)
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 7)
        .contentShape(Rectangle())
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("clients.row.\(info.id)")
    }

    @ViewBuilder
    private func actionButton(for info: ClientInfo) -> some View {
        if model.canRevoke(info) {
            HStack(spacing: 6) {
                if model.canRotate(info) {
                    Button("Ротация") {
                        pendingAction = .rotate(info)
                    }
                    .controlSize(.small)
                    .disabled(isBusy)
                    .accessibilityIdentifier("clients.rotate.\(info.id)")
                } else if model.isLocalOpenCode(info) {
                    Text("Ротация — в Settings")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("clients.rotationHint.\(info.id)")
                }
                Button("Отозвать") {
                    pendingAction = .revoke(info)
                }
                .controlSize(.small)
                .tint(.red)
                .disabled(isBusy)
                .accessibilityIdentifier("clients.revoke.\(info.id)")
            }
        } else if model.isProtectedUI(info), info.revokedAt == nil {
            Text("Защищён от отзыва")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("clients.protected.\(info.id)")
        }
    }

    private var isBusy: Bool {
        model.revokingID != nil || model.rotatingID != nil || model.isCreating
    }

    private var alertTitle: String {
        switch pendingAction {
        case .revoke?: return "Отозвать клиента?"
        case .rotate?: return "Ротировать клиента?"
        case nil: return ""
        }
    }

    private func alertMessage(_ action: PendingAction) -> String {
        switch action {
        case .revoke(let info):
            return "Токен клиента «\(info.name)» перестанет работать сразу. "
                + "Восстановить его нельзя — можно только выпустить новый."
        case .rotate(let info):
            return "Старый токен клиента «\(info.name)» будет отозван сразу. "
                + "Новый клиент получит тот же source и scopes; новый токен покажется один раз."
        }
    }

    private func presentPendingToken() {
        guard let token = pendingToken else { return }
        pendingToken = nil
        issued = token
    }

    private func statusBadge(_ info: ClientInfo) -> some View {
        let active = info.revokedAt == nil
        return Text(active ? "Активен" : "Отозван")
            .font(.caption2.weight(.medium))
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(
                (active ? Color.green : Color.secondary).opacity(0.14),
                in: Capsule()
            )
            .foregroundStyle(active ? .green : .secondary)
            .accessibilityLabel(active ? "Активен" : "Отозван")
            .accessibilityIdentifier("clients.status.\(info.id)")
    }

    private func badge(_ text: String, color: Color, identifier: String) -> some View {
        Text(text)
            .font(.caption2)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(color.opacity(0.14), in: Capsule())
            .foregroundStyle(color)
            .accessibilityIdentifier(identifier)
    }

    private func kindText(_ kind: String) -> String {
        switch kind {
        case "agent": return "агент"
        case "ui": return "UI"
        case "admin": return "admin"
        default: return kind
        }
    }

    private enum PendingAction: Identifiable {
        case revoke(ClientInfo)
        case rotate(ClientInfo)

        var id: String {
            switch self {
            case .revoke(let info): return "revoke-\(info.id)"
            case .rotate(let info): return "rotate-\(info.id)"
            }
        }
    }

    private func reload() async {
        model.setClient(appState.makeAdminClient())
        let local = appState.localAppClients
        model.localUIClientID = local["ui"]?.id
        model.localOpenCodeClientID = local["opencode"]?.id
        await model.load()
    }
}
