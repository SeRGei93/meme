import AppKit
import SwiftUI

struct ContentView: View {
    @Environment(AppState.self) private var appState

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: "brain.head.profile")
                .font(.system(size: 64, weight: .light))
                .foregroundStyle(.tint)

            VStack(spacing: 6) {
                Text("Second Brain")
                    .font(.largeTitle.bold())
                Text("Локальная память для OpenCode и Jarvis")
                    .foregroundStyle(.secondary)
            }

            Text(appState.statusText)
                .font(.callout)
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(.quaternary, in: Capsule())

            HStack(spacing: 12) {
                SettingsLink {
                    Text("Settings…")
                }
                Button("Quit") {
                    NSApp.terminate(nil)
                }
            }
            .padding(.top, 6)
        }
        .padding(40)
        .frame(minWidth: 520, minHeight: 360)
    }
}
