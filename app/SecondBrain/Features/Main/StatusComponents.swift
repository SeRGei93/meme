import SwiftUI

extension JobStatus {
    var title: String {
        switch self {
        case .queued: return "В очереди"
        case .running: return "Выполняется"
        case .done: return "Готово"
        case .failed: return "Ошибка"
        }
    }
}

struct QueueCounterView: View {
    let title: String
    let value: Int
    let identifier: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text("\(value)")
                .font(.title3.monospacedDigit().weight(.semibold))
                .accessibilityIdentifier(identifier)
        }
    }
}

struct InlineErrorText: View {
    let message: String
    let identifier: String

    var body: some View {
        Text(message)
            .font(.caption)
            .foregroundStyle(.red)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityIdentifier(identifier)
    }
}

struct ErrorPaneView: View {
    let title: String
    let message: String
    let identifierPrefix: String
    let retry: () -> Void

    var body: some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 30))
                .foregroundStyle(.red)
                .accessibilityHidden(true)
            Text(title)
                .font(.headline)
            Text(message)
                .font(.callout)
                .foregroundStyle(.red)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 420)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("\(identifierPrefix).error")
            Button("Повторить", action: retry)
                .accessibilityIdentifier("\(identifierPrefix).retry")
        }
        .padding(24)
        .frame(maxWidth: .infinity, minHeight: 320)
    }
}
