import SwiftUI

struct ProjectsView: View {
    @Environment(AppState.self) private var appState
    let onOpenProject: (String) -> Void

    @State private var model = ProjectsModel()

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
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Проекты")
                        .font(.largeTitle.bold())
                    Text("Выберите проект, чтобы открыть поиск по его записям.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if !model.projects.isEmpty {
                    Text("Всего: \(model.projects.count)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("projects.count")
                }
                Button {
                    Task { await reload() }
                } label: {
                    Label("Обновить", systemImage: "arrow.clockwise")
                }
                .disabled(model.isLoading)
                .accessibilityIdentifier("projects.refresh")
            }

            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField("Фильтр по проектам…", text: $model.filter)
                    .textFieldStyle(.plain)
                    .accessibilityIdentifier("projects.filter")
                    .accessibilityLabel("Фильтр по проектам")
                if !model.filter.isEmpty {
                    Button {
                        model.filter = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("projects.clearFilter")
                    .accessibilityLabel("Очистить фильтр")
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
            .background(.background, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(.quaternary, lineWidth: 1)
            )
        }
        .padding(.horizontal, 20)
        .padding(.top, 18)
        .padding(.bottom, 12)
    }

    @ViewBuilder
    private var content: some View {
        if model.isLoading && model.projects.isEmpty {
            ProgressView("Загружаем проекты…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityIdentifier("projects.loading")
        } else if let error = model.error, model.projects.isEmpty {
            ErrorPaneView(
                title: "Не удалось загрузить проекты",
                message: error,
                identifierPrefix: "projects"
            ) {
                Task { await model.retry() }
            }
            .frame(maxHeight: .infinity)
        } else if model.projects.isEmpty {
            messagePane(
                systemImage: "folder",
                title: "Проектов пока нет",
                message: "Проекты появляются после первой записи из OpenCode или Jarvis.",
                identifier: "projects.empty"
            )
        } else if model.filteredProjects.isEmpty {
            messagePane(
                systemImage: "text.magnifyingglass",
                title: "Ничего не найдено",
                message: "Измените фильтр или очистите его.",
                identifier: "projects.noResults"
            )
        } else {
            ScrollView {
                LazyVStack(spacing: 8) {
                    if let error = model.error {
                        InlineErrorText(message: error, identifier: "projects.warning")
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    ForEach(model.filteredProjects) { project in
                        ProjectRow(project: project) {
                            onOpenProject(project.key)
                        }
                    }
                }
                .padding(16)
            }
            .accessibilityIdentifier("projects.list")
        }
    }

    private func messagePane(
        systemImage: String,
        title: String,
        message: String,
        identifier: String
    ) -> some View {
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
                .frame(maxWidth: 340)
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier(identifier)
    }

    private func reload() async {
        model.setClient(appState.makeReadClient())
        await model.load()
    }
}

private struct ProjectRow: View {
    let project: Project
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: "folder.fill")
                    .font(.title3)
                    .foregroundStyle(.orange)
                    .accessibilityHidden(true)

                VStack(alignment: .leading, spacing: 3) {
                    Text(project.name.isEmpty ? project.key : project.name)
                        .font(.headline)
                        .lineLimit(1)
                    Text(project.key)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if !project.path.isEmpty {
                        Text(project.path)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                }

                Spacer(minLength: 12)

                VStack(alignment: .trailing, spacing: 3) {
                    Text("Записей: \(project.recordCount)")
                        .font(.callout.monospacedDigit().weight(.semibold))
                    Text(DateDisplay.text(project.lastActivityAt))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }

                Image(systemName: "chevron.right")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .strokeBorder(.quaternary, lineWidth: 1)
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("project.row.\(project.key)")
        .accessibilityLabel("\(project.name), записей: \(project.recordCount)")
        .accessibilityHint("Открыть поиск по проекту")
    }
}
