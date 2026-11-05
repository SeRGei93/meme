import Foundation
import Observation

/// A token returned exactly once by client creation. It is kept only in the
/// presenting sheet and never written to the Keychain or config.
struct IssuedToken: Identifiable, Equatable, Sendable {
    let client: ClientInfo
    let token: String

    var id: Int64 { client.id }
}

/// Client inventory, issuance, revocation and rotation.
@MainActor
@Observable
final class ClientsModel {
    private(set) var clients: [ClientInfo] = []
    private(set) var isLoading = true
    private(set) var error: String?
    private(set) var actionError: String?
    private(set) var revokingID: Int64?
    private(set) var rotatingID: Int64?
    private(set) var isCreating = false

    /// The `ui` client this installation is currently using. Revoking it from
    /// the same session would sign the app out, so the UI protects it.
    var localUIClientID: Int64?
    /// The local OpenCode client is shown with a badge but stays revocable:
    /// Settings rotation is the normal path, yet an operator may still need to
    /// cut a leaked local token immediately.
    var localOpenCodeClientID: Int64?

    private var client: APIClient?
    private var generation = 0

    init(client: APIClient? = nil) {
        self.client = client
    }

    var activeClients: [ClientInfo] {
        clients.filter { $0.revokedAt == nil }
    }

    var revokedClients: [ClientInfo] {
        clients.filter { $0.revokedAt != nil }
    }

    func setClient(_ client: APIClient?) {
        guard self.client?.configuration != client?.configuration else { return }
        self.client = client
        generation &+= 1
        clients = []
        error = nil
        actionError = nil
        revokingID = nil
        rotatingID = nil
        isCreating = false
        isLoading = false
    }

    func isLocalUI(_ info: ClientInfo) -> Bool {
        info.id == localUIClientID
    }

    func isLocalOpenCode(_ info: ClientInfo) -> Bool {
        info.id == localOpenCodeClientID
    }

    /// The app authenticates with a `ui` client. If the shared config is
    /// missing or points at a client that no longer exists or was revoked, the
    /// exact id is unknown; protecting every `ui` client is the safe fallback,
    /// because revoking the current one signs the app out with no way to
    /// recover from inside the app.
    func isProtectedUI(_ info: ClientInfo) -> Bool {
        if let localUIClientID,
           clients.contains(where: { $0.id == localUIClientID && $0.revokedAt == nil }) {
            return info.id == localUIClientID
        }
        return info.kind == ClientKind.ui.rawValue
    }

    func canRevoke(_ info: ClientInfo) -> Bool {
        info.revokedAt == nil && !isProtectedUI(info)
    }

    /// Rotation preserves the source and scopes, so it only makes sense for an
    /// active agent client. UI/admin clients and source-less clients are
    /// managed through onboarding or the CLI.
    func canRotate(_ info: ClientInfo) -> Bool {
        canRevoke(info)
            && info.kind == ClientKind.agent.rawValue
            && !(info.source ?? "").isEmpty
            && !isLocalOpenCode(info)
    }

    func isRotating(_ info: ClientInfo) -> Bool {
        rotatingID == info.id
    }

    func clearActionError() {
        actionError = nil
    }

    func load(clearActionError: Bool = true) async {
        guard let client else {
            clients = []
            error = "Нет доступа к серверу. Проверьте подключение в Settings."
            isLoading = false
            return
        }

        let generation = self.generation
        isLoading = true
        error = nil
        if clearActionError {
            actionError = nil
        }

        do {
            let list = try await client.listClients()
            guard generation == self.generation, !Task.isCancelled else { return }
            clients = Self.sorted(list.items)
            isLoading = false
        } catch {
            guard generation == self.generation, !Task.isCancelled else { return }
            self.error = ErrorDisplay.message(for: error)
            isLoading = false
        }
    }

    func retry() async {
        await load()
    }

    /// Revokes one client and refreshes the list. Returns `true` when the
    /// revocation was accepted and the refreshed list loaded successfully.
    @discardableResult
    func revoke(_ info: ClientInfo) async -> Bool {
        guard let client else {
            actionError = "Нет доступа к серверу. Проверьте подключение в Settings."
            return false
        }
        guard canRevoke(info) else {
            actionError = isProtectedUI(info)
                ? "Этот клиент используется приложением и не может быть отозван из него."
                : "Клиент уже отозван."
            return false
        }

        revokingID = info.id
        actionError = nil
        defer { revokingID = nil }

        do {
            try await client.revokeClient(id: info.id)
            await load(clearActionError: false)
            return error == nil
        } catch APIError.notFound {
            actionError = "Клиента уже нет на сервере."
            await load(clearActionError: false)
            return false
        } catch {
            actionError = ErrorDisplay.message(for: error)
            return false
        }
    }

    /// Issues an agent client. The UI deliberately does not create `ui` or
    /// `admin` clients: those carry application or administrative access and
    /// stay in onboarding/CLI, where they cannot be produced by a stray click.
    func create(name: String, source: String, scopes: [Scope]) async -> IssuedToken? {
        guard !isCreating else { return nil }
        guard let client else {
            actionError = Self.noServerMessage
            return nil
        }

        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        var source = source.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if source.isEmpty {
            source = Self.deriveSource(name)
        }
        let scopes = Self.normalizedScopes(scopes)
        if let message = Self.validationMessage(name: name, source: source, scopes: scopes) {
            actionError = message
            return nil
        }

        isCreating = true
        actionError = nil
        defer { isCreating = false }

        do {
            let response = try await client.createClient(
                CreateClientRequest(name: name, kind: .agent, source: source, scopes: scopes)
            )
            await load(clearActionError: false)
            return IssuedToken(client: response.client, token: response.token)
        } catch {
            actionError = Self.actionMessage(for: error)
            return nil
        }
    }

    /// Revokes the old token, then creates a replacement with the same name,
    /// source and scopes. Revocation comes first because a source is unique
    /// among active clients. If creation fails, the old client stays revoked
    /// and the error explains how to recover.
    func rotate(_ info: ClientInfo) async -> IssuedToken? {
        guard rotatingID == nil else { return nil }
        guard let client else {
            actionError = Self.noServerMessage
            return nil
        }
        guard !isLocalOpenCode(info) else {
            actionError = "Локальный OpenCode ротируется в Settings → Повернуть токены…"
            return nil
        }
        guard canRotate(info), let source = info.source else {
            actionError = isProtectedUI(info)
                ? "Этот клиент используется приложением: ротация доступна в onboarding."
                : "Ротация доступна только активным агентам с source."
            return nil
        }

        // Preserve the scopes verbatim. A client created via CLI may carry a
        // scope the create form never offers; silently narrowing it would make
        // the replacement less capable than the client it replaces.
        let scopes = info.scopes.compactMap(Scope.init(rawValue:))
        guard !scopes.isEmpty, scopes.count == info.scopes.count else {
            actionError = "У клиента есть scopes, которые приложение не умеет сохранить. "
                + "Ротируйте его через CLI."
            return nil
        }

        rotatingID = info.id
        actionError = nil
        defer { rotatingID = nil }

        do {
            try await client.revokeClient(id: info.id)
        } catch {
            actionError = Self.actionMessage(for: error)
            return nil
        }

        do {
            let response = try await client.createClient(
                CreateClientRequest(name: info.name, kind: .agent, source: source, scopes: scopes)
            )
            await load(clearActionError: false)
            return IssuedToken(client: response.client, token: response.token)
        } catch {
            actionError = "Старый токен отозван, но новый клиент не создан: "
                + Self.actionMessage(for: error)
            await load(clearActionError: false)
            return nil
        }
    }

    // MARK: - Validation

    nonisolated static let agentScopes: [Scope] = [.ingest, .mcpRead, .mcpWrite]

    private nonisolated static let noServerMessage =
        "Нет доступа к серверу. Проверьте подключение в Settings."

    static func normalizedScopes(_ scopes: [Scope]) -> [Scope] {
        agentScopes.filter(scopes.contains)
    }

    /// Mirrors `auth.DeriveSource`: ASCII letters/digits survive, everything
    /// else becomes a single dash, the result is capped at 64 characters.
    static func deriveSource(_ name: String) -> String {
        var result = ""
        var lastDash = false
        for scalar in name.lowercased().unicodeScalars {
            let value = scalar.value
            let isLetter = value >= 97 && value <= 122
            let isDigit = value >= 48 && value <= 57
            if isLetter || isDigit {
                result.unicodeScalars.append(scalar)
                lastDash = false
            } else if !result.isEmpty && !lastDash {
                result.append("-")
                lastDash = true
            }
        }

        var slug = result.trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        if slug.count > 64 {
            slug = String(slug.prefix(64))
                .trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        }
        return slug.isEmpty ? "agent" : slug
    }

    static func validationMessage(
        name: String,
        source: String,
        scopes: [Scope]
    ) -> String? {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty {
            return "Укажите имя клиента."
        }
        if name.count > 100 || name.range(
            of: "^[A-Za-z0-9._:-]+$",
            options: .regularExpression
        ) == nil {
            return "Имя: до 100 символов, только латиница, цифры, точка, дефис, двоеточие и подчёркивание."
        }

        let source = source.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if source.isEmpty {
            return "Укажите source."
        }
        if source.count > 64 || source.range(
            of: "^[a-z0-9][a-z0-9._-]*$",
            options: .regularExpression
        ) == nil {
            return "Source: строчные латинские буквы, цифры, точка, дефис и подчёркивание; "
                + "начинается с буквы или цифры, до 64 символов."
        }

        if normalizedScopes(scopes).isEmpty {
            return "Выберите хотя бы один scope."
        }
        return nil
    }

    static func actionMessage(for error: Error) -> String {
        guard case let APIError.server(status, _, message, field, _) = error else {
            return ErrorDisplay.message(for: error)
        }
        switch status {
        case 409:
            switch field {
            case "source":
                return "Этот source уже занят активным клиентом. Выберите другой."
            case "name":
                return "Это имя уже занято активным клиентом. Выберите другое."
            default:
                return "Конфликт с активным клиентом. Проверьте имя и source."
            }
        case 422:
            return "Сервер отклонил данные: \(message)"
        default:
            return ErrorDisplay.message(for: error)
        }
    }

    /// Active clients first, then revoked ones (newest revocation first).
    /// Names are the stable tie-breaker.
    static func sorted(_ clients: [ClientInfo]) -> [ClientInfo] {
        clients.sorted { lhs, rhs in
            switch (lhs.revokedAt, rhs.revokedAt) {
            case (nil, nil):
                break
            case (nil, _):
                return true
            case (_, nil):
                return false
            case let (left?, right?):
                if left != right { return left > right }
            }
            return lhs.name.localizedStandardCompare(rhs.name) == .orderedAscending
        }
    }
}
