import SwiftUI

struct ContentView: View {
    @Environment(AppState.self) private var appState
    @State private var searchModel = SearchModel()

    var body: some View {
        HSplitView {
            AppSidebarView()
                .frame(minWidth: 280, idealWidth: 320, maxWidth: 360)
            mainColumn
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

    @ViewBuilder
    private var mainColumn: some View {
        switch appState.selectedSection {
        case .search:
            SearchWorkspaceView(model: searchModel)
        case .dashboard:
            DashboardView()
        case .projects:
            ProjectsView { projectKey in
                searchModel.projectKey = projectKey
                searchModel.source = nil
                appState.selectedSection = .search
            }
        case .agents:
            AgentsView()
        case .graph:
            GraphView()
        }
    }
}
