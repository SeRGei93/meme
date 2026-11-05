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
            openPendingRecord()
        }
        .onChange(of: appState.pendingRecordID) { _, _ in
            openPendingRecord()
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

    /// Menu bar quick search hands a record id to the window; consume it once
    /// so a later navigation does not reopen an unrelated card. The client is
    /// set before selecting, because the search view may be created after this
    /// callback and its `onAppear` no longer wipes the selection.
    private func openPendingRecord() {
        guard let id = appState.consumePendingRecord() else { return }
        searchModel.setClient(appState.makeReadClient())
        // A changed client schedules a re-run of the current query; clearing it
        // first guarantees that re-run cannot replace the requested record.
        if !searchModel.trimmedQuery.isEmpty {
            searchModel.query = ""
        }
        searchModel.openRecord(id: id)
    }
}
