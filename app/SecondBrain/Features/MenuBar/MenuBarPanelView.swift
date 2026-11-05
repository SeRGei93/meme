import AppKit
import SwiftUI

/// Contents of the menu bar popover: connection and queue status, quick search
/// and navigation shortcuts. Shown with `.menuBarExtraStyle(.window)` so the
/// search field and result list are usable without opening the main window.
struct MenuBarPanelView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.openWindow) private var openWindow
    @State private var search = MenuBarSearchModel()
    @FocusState private var searchFocused: Bool

    private var status: UploadStatus { appState.uploadStatus }

    private var queue: UploadQueuePresentation {
        UploadQueuePresentation(
            status: appState.uploadStatus,
            requiresReauthentication: appState.requiresReauthentication
        )
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            statusSection
            Divider()
            searchSection
            Divider()
            footer
        }
        .padding(14)
        .frame(width: 380)
        .task {
            search.setClient(appState.makeReadClient())
        }
        .onAppear {
            DispatchQueue.main.async { searchFocused = true }
        }
        .onChange(of: appState.serverURL) { _, _ in
            search.setClient(appState.makeReadClient())
        }
        .onChange(of: appState.tokenRevision) { _, _ in
            search.setClient(appState.makeReadClient())
        }
        .onDisappear {
            search.resetForDismissal()
        }
    }

    // MARK: - Header and status

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Second Brain")
                    .font(.headline)
                HStack(spacing: 5) {
                    Circle()
                        .fill(connectionColor)
                        .frame(width: 6, height: 6)
                        .accessibilityHidden(true)
                    Text(connectionText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .accessibilityIdentifier("menubar.connection")
                }
            }
            Spacer(minLength: 8)
            Button {
                openDashboard()
            } label: {
                Label("Обзор", systemImage: "rectangle.grid.2x2")
            }
            .controlSize(.small)
            .accessibilityIdentifier("menubar.dashboard")
            .help("Открыть обзор")
        }
    }

    private var statusSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 16) {
                QueueCounterView(
                    title: "В очереди",
                    value: status.pending,
                    identifier: "menubar.pending"
                )
                QueueCounterView(
                    title: "Ошибки",
                    value: status.failed,
                    identifier: "menubar.failed"
                )
                Spacer(minLength: 0)
                HStack(spacing: 5) {
                    if status.isUploading {
                        ProgressView()
                            .controlSize(.small)
                    }
                    Text(queue.title)
                        .font(.caption)
                        .foregroundStyle(queue.tint)
                        .lineLimit(1)
                        .accessibilityIdentifier("menubar.queueState")
                }
            }

            HStack(spacing: 8) {
                Button("Отправить сейчас") {
                    appState.retryUploadsNow()
                }
                .controlSize(.small)
                .disabled(status.isUploading)
                .accessibilityIdentifier("menubar.retryUploads")

                Spacer(minLength: 0)
            }

            if let error = status.lastError, !error.isEmpty {
                InlineErrorText(message: error, identifier: "menubar.uploadError")
            }
        }
    }

    // MARK: - Quick search

    private var searchSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField("Быстрый поиск…", text: $search.query)
                    .textFieldStyle(.plain)
                    .focused($searchFocused)
                    .onSubmit(openFirstResult)
                    .accessibilityIdentifier("menubar.search")
                    .accessibilityLabel("Быстрый поиск по памяти")
                if !search.query.isEmpty {
                    Button {
                        search.clear()
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("menubar.clearSearch")
                    .accessibilityLabel("Очистить поиск")
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
            .background(.background, in: RoundedRectangle(cornerRadius: 7))
            .overlay(
                RoundedRectangle(cornerRadius: 7)
                    .strokeBorder(.quaternary, lineWidth: 1)
            )

            searchResults
        }
    }

    @ViewBuilder
    private var searchResults: some View {
        if let error = search.error {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                InlineErrorText(message: error, identifier: "menubar.searchError")
                Button("Повторить") {
                    search.retry()
                }
                .controlSize(.mini)
                .accessibilityIdentifier("menubar.searchRetry")
            }
        } else if search.isSearching {
            HStack(spacing: 6) {
                ProgressView()
                    .controlSize(.small)
                Text("Ищем…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .accessibilityIdentifier("menubar.searching")
        } else if !search.hasQuery {
            Text("Введите запрос — результаты появятся здесь.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("menubar.searchHint")
        } else if search.results.isEmpty {
            Text("Ничего не найдено")
                .font(.caption)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("menubar.searchEmpty")
        } else {
            ScrollView {
                LazyVStack(spacing: 4) {
                    ForEach(search.results) { card in
                        MenuBarResultRow(card: card) {
                            open(card)
                        }
                    }
                }
            }
            .frame(maxHeight: 220)
        }
    }

    // MARK: - Footer

    private var footer: some View {
        HStack(spacing: 12) {
            Button("Открыть окно") {
                showMainWindow()
            }
            .controlSize(.small)
            .accessibilityIdentifier("menubar.openWindow")

            SettingsLink {
                Text("Настройки…")
            }
            .controlSize(.small)
            .accessibilityIdentifier("menubar.settings")

            Spacer(minLength: 0)

            Button("Выйти") {
                NSApp.terminate(nil)
            }
            .controlSize(.small)
            .accessibilityIdentifier("menubar.quit")
        }
    }

    // MARK: - Actions

    private func open(_ card: Card) {
        appState.openRecord(card.id)
        showMainWindow()
    }

    private func openFirstResult() {
        guard let card = search.results.first else { return }
        open(card)
    }

    private func openDashboard() {
        appState.selectedSection = .dashboard
        showMainWindow()
    }

    private func showMainWindow() {
        openWindow(id: "main")
        NSApp.activate(ignoringOtherApps: true)
    }

    // MARK: - Status text

    private var connectionText: String {
        switch appState.connection {
        case .unknown:
            return appState.serverURL?.host ?? "Не подключено"
        case .checking:
            return "Проверяем сервер…"
        case .healthy:
            return appState.serverURL?.host ?? "Подключено"
        case .unauthenticated:
            return "Нужен новый токен"
        case .failed(let message):
            return message
        }
    }

    private var connectionColor: Color {
        switch appState.connection {
        case .healthy:
            return appState.requiresReauthentication ? .orange : .green
        case .checking, .unknown:
            return .secondary
        case .unauthenticated, .failed:
            return .red
        }
    }

}

/// Status bar icon. The symbol mirrors the upload queue so problems are visible
/// without opening the popover.
struct MenuBarLabel: View {
    let appState: AppState

    var body: some View {
        let queue = UploadQueuePresentation(
            status: appState.uploadStatus,
            requiresReauthentication: appState.requiresReauthentication
        )
        Label("Second Brain", systemImage: queue.systemImage)
            .accessibilityLabel("Second Brain")
            .accessibilityValue(queue.accessibilityStatus)
    }
}

private struct MenuBarResultRow: View {
    let card: Card
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 3) {
                Text(card.title)
                    .font(.callout.weight(.medium))
                    .lineLimit(1)
                    .truncationMode(.tail)
                HStack(spacing: 6) {
                    SourceBadge(source: card.source)
                    Text(card.projectKey)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: 4)
                    Text(card.updatedAt.formatted(date: .abbreviated, time: .shortened))
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }
            .padding(7)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 6))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("menubar.result.\(card.id)")
        .accessibilityLabel("\(card.title), \(SourceStyle.title(for: card.source))")
        .accessibilityHint("Открыть карточку")
    }
}
