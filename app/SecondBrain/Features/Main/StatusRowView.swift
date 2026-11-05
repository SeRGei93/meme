import SwiftUI

/// Two-column status line used by Dashboard and Agents. The identifier is put
/// on the value text so UI tests read the value, not the label.
struct StatusRowView: View {
    let title: String
    let value: String
    var tint: Color = .primary
    let identifier: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(title)
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer(minLength: 8)
            Text(value)
                .font(.callout)
                .foregroundStyle(tint)
                .multilineTextAlignment(.trailing)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier(identifier)
        }
    }
}
