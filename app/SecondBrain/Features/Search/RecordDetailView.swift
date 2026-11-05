import AppKit
import SwiftUI

struct RecordDetailView: View {
    let record: Record
    let project: Project?

    @State private var rawSearch: String = {
        #if DEBUG
        return ProcessInfo.processInfo.environment["SECOND_BRAIN_UI_RAW_QUERY"] ?? ""
        #else
        return ""
        #endif
    }()
    @State private var matchedLines: [String] = []
    @State private var matchCount = 0
    @State private var rawSearching = false
    @State private var showFullRawText = false
    @State private var showAllMatches = false
    @State private var rawPreviewText = ""
    @State private var rawLength = 0
    @State private var copied = false

    private let rawPreviewLimit = 100_000
    private let rawMatchLimit = 200

    private var card: Card { record.card }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                summarySection
                questionsSection
                keywordsSection
                atomsSection
                rawTextSection
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .accessibilityIdentifier("record.detail")
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                SourceBadge(source: card.source)
                Text(kindTitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Text(statusTitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 0)
                Text("ревизия \(record.revision)")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }

            Text(card.title)
                .font(.title2.bold())
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("record.title")

            HStack(spacing: 6) {
                Image(systemName: "folder")
                    .font(.caption2)
                    .accessibilityHidden(true)
                if let project {
                    Text(project.name.isEmpty ? project.key : project.name)
                        .font(.callout)
                    Text(project.key)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                } else {
                    Text(card.projectKey)
                        .font(.callout)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("record.project")

            if let path = project?.path, !path.isEmpty {
                Text(path)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            HStack(spacing: 12) {
                Label(
                    card.updatedAt.formatted(date: .abbreviated, time: .shortened),
                    systemImage: "clock"
                )
                if let startedAt = card.startedAt {
                    Text("начато \(startedAt.formatted(date: .abbreviated, time: .shortened))")
                }
                if !card.isFinal {
                    Text("черновик")
                        .foregroundStyle(.orange)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            .accessibilityIdentifier("record.date")
        }
    }

    // MARK: - Sections

    private var summarySection: some View {
        section("Кратко") {
            Text(card.summary.isEmpty ? "Нет summary" : card.summary)
                .font(.body)
                .foregroundStyle(card.summary.isEmpty ? .secondary : .primary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("record.summary")
        }
    }

    private var questionsSection: some View {
        section("Вопросы") {
            if card.questions.isEmpty {
                emptyText("Вопросов нет")
            } else {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(Array(card.questions.enumerated()), id: \.offset) { _, question in
                        HStack(alignment: .top, spacing: 6) {
                            Text("•")
                                .foregroundStyle(.secondary)
                            Text(question)
                                .textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .accessibilityIdentifier("record.questions")
            }
        }
    }

    private var keywordsSection: some View {
        section("Ключевые слова") {
            if card.keywords.isEmpty {
                emptyText("Ключевых слов нет")
            } else {
                LazyVGrid(
                    columns: [GridItem(.adaptive(minimum: 80, maximum: 220), spacing: 6)],
                    alignment: .leading,
                    spacing: 6
                ) {
                    ForEach(card.keywords, id: \.self) { keyword in
                        Text(keyword)
                            .font(.caption)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 3)
                            .background(.quaternary, in: Capsule())
                    }
                }
                .accessibilityIdentifier("record.keywords")
            }
        }
    }

    private var atomsSection: some View {
        section("Атомы") {
            if card.atoms.isEmpty {
                emptyText("Атомов нет")
            } else {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(Array(card.atoms.enumerated()), id: \.offset) { _, atom in
                        HStack(alignment: .top, spacing: 6) {
                            Image(systemName: "circle.fill")
                                .font(.system(size: 4))
                                .padding(.top, 6)
                                .foregroundStyle(.secondary)
                                .accessibilityHidden(true)
                            Text(atom)
                                .textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .accessibilityIdentifier("record.atoms")
            }
        }
    }

    private var rawTextSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text("Исходный текст")
                    .font(.headline)
                Spacer(minLength: 0)
                if !trimmedRawSearch.isEmpty {
                    if rawSearching {
                        ProgressView()
                            .controlSize(.mini)
                    } else {
                        Text(matchText)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .accessibilityIdentifier("record.rawMatches")
                    }
                }
                Button {
                    copyRawText()
                } label: {
                    Label(
                        copied ? "Скопировано" : "Копировать",
                        systemImage: copied ? "checkmark" : "doc.on.doc"
                    )
                }
                .controlSize(.small)
                .accessibilityIdentifier("record.copyRaw")
                .accessibilityLabel(copied ? "Исходный текст скопирован" : "Копировать исходный текст")
            }

            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField("Найти в тексте…", text: $rawSearch)
                    .textFieldStyle(.plain)
                    .accessibilityIdentifier("record.rawSearch")
                    .accessibilityLabel("Поиск в исходном тексте")
                if !rawSearch.isEmpty {
                    Button {
                        rawSearch = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("record.rawSearchClear")
                    .accessibilityLabel("Очистить поиск в тексте")
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .background(.background, in: RoundedRectangle(cornerRadius: 6))
            .overlay(
                RoundedRectangle(cornerRadius: 6)
                    .strokeBorder(.quaternary, lineWidth: 1)
            )

            rawTextBody
        }
        .padding(12)
        .task(id: record.card.id) {
            let text = record.rawText
            let limit = rawPreviewLimit
            let result = await Task.detached(priority: .userInitiated) {
                (String(text.prefix(limit)), text.count)
            }.value
            guard !Task.isCancelled else { return }
            rawPreviewText = result.0
            rawLength = result.1
        }
        .task(id: rawSearch) {
            await updateRawMatches()
        }
        .background(.background.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .strokeBorder(.quaternary, lineWidth: 1)
        )
    }

    private func rawText(_ text: String) -> some View {
        Text(text)
            .font(.callout)
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityIdentifier("record.rawText")
            .accessibilityLabel("Исходный текст записи")
    }

    private var kindTitle: String {
        switch card.kind {
        case "session": return "сессия"
        case "memory": return "память"
        default: return card.kind
        }
    }

    private var statusTitle: String {
        switch card.cardStatus {
        case "ok": return "готова"
        case "queued": return "в очереди"
        case "heuristic": return "эвристика"
        case "failed": return "ошибка"
        default: return card.cardStatus
        }
    }

    private var trimmedRawSearch: String {
        rawSearch.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var matchText: String {
        matchCount == 1 ? "1 совпадение" : "\(matchCount) совпадений"
    }

    @ViewBuilder
    private var rawTextBody: some View {
        if trimmedRawSearch.isEmpty {
            if rawPreviewText.isEmpty {
                ProgressView()
                    .controlSize(.small)
            } else {
                rawText(showFullRawText ? record.rawText : rawPreviewText)
            }
            if rawLength > rawPreviewLimit, !showFullRawText {
                Text("Показан фрагмент \(rawPreviewLimit) символов из \(rawLength).")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Button("Показать весь текст") {
                    showFullRawText = true
                }
                .controlSize(.small)
                .accessibilityIdentifier("record.showFullRawText")
            }
        } else if rawSearching {
            Text("Ищем в тексте…")
                .font(.callout)
                .foregroundStyle(.secondary)
        } else if matchCount == 0 {
            emptyText("Совпадений нет")
        } else {
            rawText(
                matchedLines
                    .prefix(showAllMatches ? matchedLines.count : rawMatchLimit)
                    .joined(separator: "\n")
            )
            if matchCount > rawMatchLimit, !showAllMatches {
                Text("Показаны первые \(rawMatchLimit) из \(matchCount) строк.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Button("Показать все совпадения") {
                    showAllMatches = true
                }
                .controlSize(.small)
                .accessibilityIdentifier("record.showAllRawMatches")
            }
        }
    }

    private func updateRawMatches() async {
        let query = trimmedRawSearch
        guard !query.isEmpty else {
            matchedLines = []
            matchCount = 0
            rawSearching = false
            return
        }
        rawSearching = true
        try? await Task.sleep(for: .milliseconds(150))
        guard !Task.isCancelled else { return }

        let text = record.rawText
        let lines = await Task.detached(priority: .userInitiated) {
            RawTextSearch.matchingLines(in: text, query: query)
        }.value
        guard !Task.isCancelled else { return }
        matchedLines = lines
        matchCount = lines.count
        showAllMatches = false
        rawSearching = false
    }

    private func copyRawText() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(record.rawText, forType: .string)
        copied = true
        Task {
            try? await Task.sleep(for: .seconds(1.5))
            guard !Task.isCancelled else { return }
            copied = false
        }
    }

    private func section<Content: View>(
        _ title: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.headline)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func emptyText(_ text: String) -> some View {
        Text(text)
            .font(.callout)
            .foregroundStyle(.secondary)
    }
}
