import SwiftUI

struct ContentView: View {
    @Environment(AppState.self) private var appState

    var body: some View {
        HSplitView {
            AppSidebarView()
                .frame(minWidth: 280, idealWidth: 320, maxWidth: 360)
            SearchWorkspaceView()
                .frame(minWidth: 680)
        }
        .frame(minWidth: 980, minHeight: 640)
        .task {
            await appState.bootstrap()
        }
        .sheet(isPresented: Binding(
            get: { appState.isOnboardingPresented },
            set: { appState.isOnboardingPresented = $0 }
        )) {
            OnboardingView()
                .environment(appState)
        }
    }
}
