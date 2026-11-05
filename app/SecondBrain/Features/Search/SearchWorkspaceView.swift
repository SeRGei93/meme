import AppKit
import SwiftUI

struct SearchWorkspaceView: View {
    @Environment(AppState.self) private var appState
    let model: SearchModel
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            HSplitView {
                SearchResultsPane(model: model, searchFocused: $searchFocused)
                    .frame(minWidth: 300, idealWidth: 380)
                RecordDetailPane(model: model)
                    .frame(minWidth: 360, idealWidth: 520)
            }
        }
        .onAppear {
            model.setClient(appState.makeReadClient())
            model.ensureProjectsLoaded()
            if appState.consumeSearchFocusRequest() {
                searchFocused = true
            }
        }
        .onChange(of: appState.serverURL) { _, _ in
            model.setClient(appState.makeReadClient())
        }
        .onChange(of: appState.tokenRevision) { _, _ in
            model.setClient(appState.makeReadClient())
        }
        .onChange(of: appState.isConfigured) { _, _ in
            model.setClient(appState.makeReadClient())
        }
        .onChange(of: appState.searchFocusRequest) { _, _ in
            if appState.consumeSearchFocusRequest() {
                searchFocused = true
            }
        }
        .onDisappear {
            model.cancelPendingRequests()
        }
    }

    private var header: some View {
        @Bindable var model = model
        return VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text("Память")
                    .font(.largeTitle.bold())
                Spacer()
                if !model.isIdle, !model.isLoading {
                    Text(countText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("search.count")
                }
            }

            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField("Поиск по памяти…", text: $model.query)
                    .textFieldStyle(.plain)
                    .focused($searchFocused)
                    .onKeyPress(.downArrow) { handleArrow(1); return .handled }
                    .onKeyPress(.upArrow) { handleArrow(-1); return .handled }
                    .onKeyPress(.escape) {
                        guard !model.query.isEmpty else { return .ignored }
                        model.query = ""
                        return .handled
                    }
                    .accessibilityIdentifier("search.field")
                    .accessibilityLabel("Поиск по памяти")
                    .accessibilityHint("Нажмите ⌘K, стрелки вверх и вниз выбирают результат")

                if model.isLoading {
                    ProgressView()
                        .controlSize(.small)
                        .accessibilityIdentifier("search.loading")
                }

                if !model.query.isEmpty {
                    Button {
                        model.query = ""
                        searchFocused = true
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("search.clear")
                    .accessibilityLabel("Очистить поиск")
                }

                Text("⌘K")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 4))
                    .accessibilityHidden(true)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(.background, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(.quaternary, lineWidth: 1)
            )

            filterBar
            if let projectKey = model.projectKey {
                projectBadge(projectKey)
            }
        }
        .padding(.horizontal, 20)
        .padding(.top, 18)
        .padding(.bottom, 12)
    }

    private func projectBadge(_ projectKey: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "folder.fill")
                .font(.caption2)
                .foregroundStyle(.orange)
                .accessibilityHidden(true)
            Text("Проект: \(model.selectedProject?.name ?? projectKey)")
                .font(.caption)
                .lineLimit(1)
                .truncationMode(.middle)
                .accessibilityIdentifier("search.projectBadge")
            Button {
                model.projectKey = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("search.clearProject")
            .accessibilityLabel("Сбросить фильтр по проекту")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(.orange.opacity(0.10), in: Capsule())
    }

    private var filterBar: some View {
        @Bindable var model = model
        return HStack(spacing: 10) {
            Picker("Проект", selection: $model.projectKey) {
                Text("Все проекты").tag(String?.none)
                ForEach(model.projects) { project in
                    Text(project.name.isEmpty ? project.key : project.name)
                        .tag(Optional(project.key))
                }
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .frame(maxWidth: 220)
            .accessibilityIdentifier("search.projectFilter")
            .accessibilityLabel("Фильтр по проекту")
            .help("Фильтр по проекту")

            Picker("Источник", selection: $model.source) {
                Text("Все источники").tag(MemorySource?.none)
                ForEach(MemorySource.allCases) { source in
                    Text(source.title).tag(Optional(source))
                }
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .frame(maxWidth: 170)
            .accessibilityIdentifier("search.sourceFilter")
            .accessibilityLabel("Фильтр по источнику")
            .help("Фильтр по источнику")

            Picker("Дата", selection: $model.dateRange) {
                ForEach(SearchDateRange.allCases) { range in
                    Text(range.title).tag(range)
                }
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .frame(maxWidth: 150)
            .accessibilityIdentifier("search.dateFilter")
            .accessibilityLabel("Фильтр по дате")
            .help("Фильтр по дате")

            if model.hasActiveFilters {
                Button("Сбросить") {
                    model.resetFilters()
                }
                .controlSize(.small)
                .accessibilityIdentifier("search.resetFilters")
            }

            Spacer(minLength: 0)
        }
    }

    private var countText: String {
        if model.total > model.results.count {
            return "\(model.results.count) из \(model.total)"
        }
        return "Найдено: \(model.total)"
    }

    private func handleArrow(_ delta: Int) {
        model.moveSelection(by: delta)
    }
}

private struct SearchResultsPane: View {
    let model: SearchModel
    @FocusState.Binding var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            if model.isIdle {
                SearchMessageView(
                    systemImage: model.projectKey == nil ? "magnifyingglass" : "folder",
                    title: model.projectKey == nil ? "Найдите в памяти" : "Поиск по проекту",
                    message: model.projectKey == nil
                        ? "Введите запрос, чтобы найти решения, вопросы и заметки из OpenCode и Jarvis."
                        : "Фильтр «\(model.selectedProject?.name ?? model.projectKey ?? "")» применён. Введите запрос, чтобы найти записи этого проекта.",
                    identifier: "search.idle"
                )
            } else if let error = model.error {
                SearchErrorView(title: "Не удалось выполнить поиск", message: error) {
                    model.retrySearch()
                }
            } else if model.results.isEmpty {
                if model.isLoading {
                    VStack(spacing: 10) {
                        ProgressView()
                        Text("Ищем…")
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .accessibilityIdentifier("search.loadingPane")
                    .accessibilityLabel("Идёт поиск")
                } else {
                    SearchMessageView(
                        systemImage: "text.magnifyingglass",
                        title: "Ничего не найдено",
                        message: "Попробуйте другой запрос или сбросьте фильтры.",
                        identifier: "search.noResults",
                        actionTitle: model.hasActiveFilters ? "Сбросить фильтры" : nil,
                        action: model.hasActiveFilters ? { model.resetFilters() } : nil
                    )
                }
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(spacing: 8) {
                            ForEach(model.results) { card in
                                SearchResultCard(
                                    card: card,
                                    isSelected: model.selectedID == card.id
                                ) {
                                    model.selectedID = card.id
                                    searchFocused = true
                                }
                                .id(card.id)
                            }
                        }
                        .padding(12)
                    }
                    .accessibilityIdentifier("search.results")
                    .onChange(of: model.selectedID) { _, id in
                        guard let id else { return }
                        withAnimation(.easeInOut(duration: 0.15)) {
                            proxy.scrollTo(id, anchor: .center)
                        }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }
}

private struct SearchResultCard: View {
    let card: Card
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    SourceBadge(source: card.source)
                    if !card.isFinal {
                        Text("черновик")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(.quaternary, in: Capsule())
                    }
                    Spacer(minLength: 0)
                    Text(card.updatedAt.formatted(date: .abbreviated, time: .omitted))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }

                Text(card.title)
                    .font(.headline)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)

                if !card.summary.isEmpty {
                    Text(card.summary)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                        .multilineTextAlignment(.leading)
                }

                HStack(spacing: 6) {
                    Image(systemName: "folder")
                        .font(.caption2)
                        .accessibilityHidden(true)
                    Text(card.projectKey)
                        .font(.caption)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if !card.keywords.isEmpty {
                        Text("·")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                        Text(card.keywords.prefix(3).joined(separator: ", "))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                .foregroundStyle(.secondary)
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
                isSelected ? Color.accentColor.opacity(0.12) : Color.clear,
                in: RoundedRectangle(cornerRadius: 8)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(
                        isSelected ? Color.accentColor.opacity(0.55) : Color(nsColor: .separatorColor),
                        lineWidth: 1
                    )
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("search.result.\(card.id)")
        .accessibilityLabel("\(card.title), \(card.source), \(card.projectKey)")
        .accessibilityHint("Открыть карточку")
        .accessibilityAddTraits(isSelected ? [.isSelected] : [])
    }
}

struct SourceBadge: View {
    let source: String

    var body: some View {
        Text(title)
            .font(.caption2.weight(.semibold))
            .foregroundStyle(tint)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(tint.opacity(0.12), in: Capsule())
            .accessibilityLabel("Источник: \(title)")
    }

    private var title: String {
        switch source {
        case MemorySource.jarvis.rawValue: return "Jarvis"
        case MemorySource.opencode.rawValue: return "OpenCode"
        default: return source.capitalized
        }
    }

    private var tint: Color {
        switch source {
        case MemorySource.jarvis.rawValue: return .purple
        case MemorySource.opencode.rawValue: return .blue
        default: return .secondary
        }
    }
}

private struct RecordDetailPane: View {
    let model: SearchModel

    var body: some View {
        Group {
            if model.selectedID == nil {
                SearchMessageView(
                    systemImage: "doc.text.magnifyingglass",
                    title: "Выберите запись",
                    message: "Карточка откроется после выбора результата или нажатия ↑/↓.",
                    identifier: "record.placeholder"
                )
            } else if model.detailLoading {
                VStack(spacing: 10) {
                    ProgressView()
                    Text("Загружаем запись…")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("record.detailLoading")
            } else if let error = model.detailError {
                SearchErrorView(
                    title: "Не удалось открыть запись",
                    message: error,
                    identifierPrefix: "record"
                ) {
                    model.retryDetail()
                }
            } else if let record = model.detail {
                RecordDetailView(record: record, project: model.selectedProject)
                    .id(record.card.id)
            } else {
                SearchMessageView(
                    systemImage: "doc.text.magnifyingglass",
                    title: "Выберите запись",
                    message: "Карточка откроется после выбора результата.",
                    identifier: "record.placeholder"
                )
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct SearchMessageView: View {
    let systemImage: String
    let title: String
    let message: String
    let identifier: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        VStack(spacing: 10) {
            Image(systemName: systemImage)
                .font(.system(size: 32, weight: .light))
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text(title)
                .font(.headline)
            Text(message)
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 320)
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .controlSize(.small)
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier(identifier)
    }
}

private struct SearchErrorView: View {
    let title: String
    let message: String
    var identifierPrefix = "search"
    let retry: () -> Void

    var body: some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 28))
                .foregroundStyle(.red)
                .accessibilityHidden(true)
            Text(title)
                .font(.headline)
            Text(message)
                .font(.callout)
                .foregroundStyle(.red)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 360)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("\(identifierPrefix).error")
            Button("Повторить", action: retry)
                .controlSize(.small)
                .accessibilityIdentifier("\(identifierPrefix).retry")
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
