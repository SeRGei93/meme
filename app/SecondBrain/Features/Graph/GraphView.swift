import SwiftUI

struct GraphView: View {
    @Environment(AppState.self) private var appState
    @State private var model = GraphModel()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .task { await reload() }
        .onChange(of: appState.serverURL) { _, _ in
            Task { await reload() }
        }
        .onChange(of: appState.tokenRevision) { _, _ in
            Task { await reload() }
        }
        .onChange(of: model.projectKey) { _, _ in
            Task { await model.load() }
        }
        .onChange(of: model.source) { _, _ in
            Task { await model.load() }
        }
        .sheet(
            isPresented: $model.isDetailPresented,
            onDismiss: { model.dismissDetail() }
        ) {
            GraphRecordSheet(model: model)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Граф")
                        .font(.largeTitle.bold())
                    Text("Связи между записями по проекту и ключевым словам.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if model.isLayouting {
                    HStack(spacing: 5) {
                        ProgressView()
                            .controlSize(.small)
                        Text("Раскладка…")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .accessibilityIdentifier("graph.layoutingStatus")
                }
                if model.isTruncated {
                    Label("Показаны не все узлы", systemImage: "exclamationmark.circle")
                        .font(.caption)
                        .foregroundStyle(.orange)
                        .accessibilityIdentifier("graph.truncated")
                        .help("Сервер ограничил число записей в графе")
                }
                if model.graph != nil {
                    Text("\(model.visualNodes.count) узлов · \(model.visualEdges.count) связей")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("graph.count")
                        .accessibilityValue(
                            "\(model.visualNodes.count) узлов, \(model.visualEdges.count) связей"
                        )
                }
                Button {
                    Task { await reload() }
                } label: {
                    Label("Обновить", systemImage: "arrow.clockwise")
                }
                .disabled(model.isLoading || model.isLayouting)
                .accessibilityIdentifier("graph.refresh")
            }

            filterBar
            if let projectsError = model.projectsError {
                InlineErrorText(
                    message: "Список проектов недоступен: \(projectsError)",
                    identifier: "graph.projectsError"
                )
            }
            if let sourcesError = model.sourcesError {
                InlineErrorText(
                    message: "Список источников недоступен: \(sourcesError)",
                    identifier: "graph.sourcesError"
                )
            }
        }
        .padding(.horizontal, 20)
        .padding(.top, 18)
        .padding(.bottom, 12)
    }

    private var filterBar: some View {
        HStack(spacing: 10) {
            Picker("Проект", selection: $model.projectKey) {
                Text("Все проекты").tag(String?.none)
                ForEach(projectChoices) { option in
                    Text(option.name).tag(Optional(option.key))
                }
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .frame(maxWidth: 240)
            .accessibilityIdentifier("graph.projectFilter")
            .accessibilityLabel("Фильтр по проекту")
            .help("Фильтр по проекту")

            Picker("Источник", selection: $model.source) {
                Text("Все источники").tag(String?.none)
                ForEach(model.sourceOptions) { option in
                    Text(option.label).tag(Optional(option.source))
                }
            }
            .pickerStyle(.menu)
            .labelsHidden()
            .frame(maxWidth: 170)
            .accessibilityIdentifier("graph.sourceFilter")
            .accessibilityLabel("Фильтр по источнику")
            .help("Фильтр по источнику")

            if model.projectKey != nil || model.source != nil {
                Button("Сбросить фильтры") {
                    model.resetFilters()
                }
                .controlSize(.small)
                .accessibilityIdentifier("graph.clearFilters")
            }

            Spacer(minLength: 8)

            legend
        }
    }

    private var legend: some View {
        HStack(spacing: 12) {
            ForEach(model.sourceOptions) { option in
                legendItem(option)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(legendAccessibilityLabel)
    }

    private func legendItem(_ option: SourceOption) -> some View {
        HStack(spacing: 5) {
            Circle()
                .fill(SourceStyle.tint(for: option.source).gradient)
                .frame(width: 9, height: 9)
                .accessibilityHidden(true)
            Text("\(option.title) · \(model.count(for: option.source))")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    private var legendAccessibilityLabel: String {
        let parts = model.sourceOptions.map {
            "\($0.title): \(model.count(for: $0.source))"
        }
        return parts.isEmpty ? "Легенда пуста" : "Легенда: " + parts.joined(separator: ", ")
    }

    private var projectChoices: [ProjectOption] {
        var options = model.projectOptions
        if let key = model.projectKey, !options.contains(where: { $0.key == key }) {
            options.append(ProjectOption(key: key, name: key))
        }
        return options
    }

    @ViewBuilder
    private var content: some View {
        if let error = model.error, model.graph == nil {
            ErrorPaneView(
                title: "Не удалось загрузить граф",
                message: error,
                identifierPrefix: "graph"
            ) {
                Task { await reload() }
            }
        } else if model.graph == nil {
            ProgressView("Загружаем граф…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("graph.loading")
        } else if model.visualNodes.isEmpty && model.isLayouting {
            ProgressView("Раскладываем узлы…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("graph.layouting")
        } else if model.visualNodes.isEmpty {
            emptyPane
        } else {
            ZStack(alignment: .topLeading) {
                GraphCanvasView(
                    nodes: model.visualNodes,
                    edges: model.visualEdges,
                    selectedID: model.selectedNodeID,
                    onSelect: model.selectNode
                )
                .padding(12)

                if model.isLoading {
                    ProgressView()
                        .controlSize(.small)
                        .padding(20)
                        .accessibilityIdentifier("graph.reloading")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private var emptyPane: some View {
        let projectIsEmpty = model.projectKey != nil && !model.hasNodesInResponse
        let filtersHideNodes = model.hasNodesInResponse
        return VStack(spacing: 10) {
            Image(
                systemName: filtersHideNodes
                    ? "line.3.horizontal.decrease.circle"
                    : "point.3.connected.trianglepath.dotted"
            )
            .font(.system(size: 32, weight: .light))
            .foregroundStyle(.secondary)
            .accessibilityHidden(true)
            Text(emptyTitle(projectIsEmpty: projectIsEmpty, filtersHideNodes: filtersHideNodes))
                .font(.headline)
            Text(emptyMessage(projectIsEmpty: projectIsEmpty, filtersHideNodes: filtersHideNodes))
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 360)
            if model.projectKey != nil || model.source != nil {
                Button("Сбросить фильтры") {
                    model.resetFilters()
                }
                .controlSize(.small)
                .accessibilityIdentifier("graph.emptyReset")
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("graph.empty")
    }

    private func emptyTitle(projectIsEmpty: Bool, filtersHideNodes: Bool) -> String {
        if filtersHideNodes { return "Нет узлов с этими фильтрами" }
        if projectIsEmpty { return "В этом проекте пока нет записей" }
        return "Граф пока пуст"
    }

    private func emptyMessage(projectIsEmpty: Bool, filtersHideNodes: Bool) -> String {
        if filtersHideNodes {
            return "Измените проект или источник, чтобы увидеть связи."
        }
        if projectIsEmpty {
            return "Снимите фильтр по проекту или дождитесь новых записей."
        }
        return "Связи появятся, когда в памяти будет несколько записей от агентов."
    }

    private func reload() async {
        model.setClient(appState.makeReadClient())
        await model.load()
    }
}

private struct GraphRecordSheet: View {
    let model: GraphModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Label("Карточка", systemImage: "doc.text")
                    .font(.headline)
                Spacer()
                Button("Закрыть") {
                    dismiss()
                }
                .keyboardShortcut(.cancelAction)
                .accessibilityIdentifier("graph.detail.close")
            }
            .padding(12)
            Divider()
            content
        }
        .frame(minWidth: 680, minHeight: 560)
        .accessibilityIdentifier("graph.detail")
    }

    @ViewBuilder
    private var content: some View {
        if model.detailLoading {
            ProgressView("Загружаем запись…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("graph.detailLoading")
        } else if let error = model.detailError {
            ErrorPaneView(
                title: "Не удалось открыть запись",
                message: error,
                identifierPrefix: "graphDetail"
            ) {
                model.retryDetail()
            }
        } else if let record = model.detail {
            RecordDetailView(
                record: record,
                project: model.project(for: record.card.projectKey)
            )
            .id(record.card.id)
        } else {
            ProgressView()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("graph.detailPlaceholder")
        }
    }
}
