import Foundation
import Observation

/// Read-only client inventory plus revocation. Tokens are never returned by
/// the list endpoint, so this screen can only describe and revoke clients;
/// issuance lives in a separate flow.
@MainActor
@Observable
final class ClientsModel {
    private(set) var clients: [ClientInfo] = []
    private(set) var isLoading = true
    private(set) var error: String?
    private(set) var actionError: String?
    private(set) var revokingID: Int64?

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
