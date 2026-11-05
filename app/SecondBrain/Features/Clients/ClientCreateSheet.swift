import AppKit
import SwiftUI

/// Form for issuing an agent client. Only `kind=agent` is exposed here: `ui`
/// and `admin` clients carry application/admin access and stay in the CLI.
struct ClientCreateSheet: View {
    let model: ClientsModel
    let onIssued: (IssuedToken) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var source = ""
    @State private var scopes: Set<Scope> = Set(ClientsModel.agentScopes)

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Новый клиент")
                .font(.title2.bold())

            Form {
                Section("Клиент") {
                    TextField("Имя", text: $name, prompt: Text("claude-laptop"))
                        .accessibilityIdentifier("clients.create.name")

                    TextField(
                        "Source",
                        text: $source,
                        prompt: Text(ClientsModel.deriveSource(name))
                    )
                    .accessibilityIdentifier("clients.create.source")

                    Text(
                        "Если оставить Source пустым, он будет выведен из имени: "
                            + "\(ClientsModel.deriveSource(name)). Source неизменяем после "
                            + "выпуска; клиенты UI и admin создаются через CLI."
                    )
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }

                Section("Scopes") {
                    ForEach(ClientsModel.agentScopes, id: \.self) { scope in
                        Toggle(scopeTitle(scope), isOn: binding(for: scope))
                            .accessibilityIdentifier("clients.create.scope.\(scope.rawValue)")
                    }
                    Text("ingest нужен только для HTTP fallback; для MCP достаточно mcp:read и mcp:write.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                if let error = model.actionError {
                    Text(error)
                        .font(.callout)
                        .foregroundStyle(.red)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("clients.create.error")
                }
            }
            .formStyle(.grouped)

            HStack {
                Button("Отмена") {
                    dismiss()
                }
                .keyboardShortcut(.cancelAction)
                .disabled(model.isCreating)
                .accessibilityIdentifier("clients.create.cancel")

                Spacer()

                if model.isCreating {
                    ProgressView()
                        .controlSize(.small)
                }

                Button("Выпустить токен") {
                    Task { await submit() }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSubmit)
                .accessibilityIdentifier("clients.create.submit")
            }
        }
        .padding(20)
        .frame(width: 600)
        .interactiveDismissDisabled(model.isCreating)
        .onAppear {
            #if DEBUG
            let environment = ProcessInfo.processInfo.environment
            if name.isEmpty, let value = environment["SECOND_BRAIN_UI_CREATE_NAME"] {
                name = value
            }
            #endif
            model.clearActionError()
        }
    }

    private var canSubmit: Bool {
        !model.isCreating
            && !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !scopes.isEmpty
    }

    private func submit() async {
        guard let token = await model.create(
            name: name,
            source: source,
            scopes: ClientsModel.agentScopes.filter(scopes.contains)
        ) else {
            return
        }
        onIssued(token)
    }

    private func binding(for scope: Scope) -> Binding<Bool> {
        Binding(
            get: { scopes.contains(scope) },
            set: { enabled in
                if enabled {
                    scopes.insert(scope)
                } else {
                    scopes.remove(scope)
                }
            }
        )
    }

    private func scopeTitle(_ scope: Scope) -> String {
        switch scope {
        case .ingest: return "ingest — приём бандлов/HTTP fallback"
        case .mcpRead: return "mcp:read — чтение через MCP"
        case .mcpWrite: return "mcp:write — запись через MCP"
        case .ui: return "ui"
        case .admin: return "admin"
        }
    }
}

/// The only place a plaintext token is ever shown. It stays in this view: no
/// Keychain, config or log write happens here.
struct TokenIssuedView: View {
    let issued: IssuedToken
    let onDone: () -> Void

    @State private var copied = false
    @State private var copyFailed = false
    @State private var confirmingDiscard = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                Image(systemName: "checkmark.seal.fill")
                    .font(.title)
                    .foregroundStyle(.green)
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Токен выпущен")
                        .font(.title2.bold())
                    Text("Клиент «\(issued.client.name)»")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            }

            HStack(spacing: 10) {
                Text("source: \(issued.client.source ?? "—")")
                Text(issued.client.scopes.joined(separator: ", "))
            }
            .font(.caption)
            .foregroundStyle(.secondary)

            Text(
                "Скопируйте токен сейчас: он показывается один раз и не сохраняется "
                    + "приложением. Позже его можно только заменить ротацией."
            )
            .font(.callout)
            .fixedSize(horizontal: false, vertical: true)

            HStack(spacing: 8) {
                Text(issued.token)
                    .font(.system(.body, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(2)
                    .truncationMode(.middle)
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 6))
                    .accessibilityIdentifier("clients.token.value")

                Button("Скопировать") {
                    copyToken()
                }
                .accessibilityIdentifier("clients.token.copy")
            }

            if copied {
                Label("Скопировано в буфер обмена", systemImage: "checkmark")
                    .font(.caption)
                    .foregroundStyle(.green)
                    .accessibilityIdentifier("clients.token.copied")
            } else if copyFailed {
                Text("Не удалось скопировать. Выделите токен и скопируйте вручную.")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .accessibilityIdentifier("clients.token.copyFailed")
            }

            HStack {
                Spacer()
                Button("Готово") {
                    if copied {
                        onDone()
                    } else {
                        confirmingDiscard = true
                    }
                }
                .keyboardShortcut(.defaultAction)
                .accessibilityIdentifier("clients.token.done")
            }
        }
        .padding(24)
        .frame(width: 620)
        .interactiveDismissDisabled(!copied)
        .alert("Токен не скопирован", isPresented: $confirmingDiscard) {
            Button("Остаться", role: .cancel) {}
            Button("Закрыть без копирования", role: .destructive) {
                onDone()
            }
        } message: {
            Text("Токен больше не будет показан. Его можно только заменить ротацией.")
        }
    }

    private func copyToken() {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        copied = pasteboard.setString(issued.token, forType: .string)
        copyFailed = !copied
    }
}
