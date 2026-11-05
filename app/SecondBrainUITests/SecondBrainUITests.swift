import AppKit
import Foundation
import XCTest

final class SecondBrainUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    override func tearDownWithError() throws {
        // Some tests intentionally leave the app running (menu-bar behaviour).
        // Terminate it so the next test starts with a fresh window instead of
        // attaching to an instance whose main window was closed.
        let app = XCUIApplication()
        if app.state != .notRunning {
            app.terminate()
        }
        try super.tearDownWithError()
    }

    func testLaunchShowsMainWindow() throws {
        let app = configuredApp()
        app.launch()

        XCTAssertTrue(app.windows.firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Second Brain"].waitForExistence(timeout: 5))
    }

    func testUploadQueueShowsCountersEmptyStateAndRetryAction() throws {
        let app = configuredApp()
        app.launch()

        XCTAssertTrue(app.staticTexts["upload.empty"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["upload.pendingValue"].exists)
        XCTAssertTrue(app.staticTexts["upload.sentValue"].exists)
        XCTAssertTrue(app.staticTexts["upload.failedValue"].exists)
        XCTAssertTrue(app.buttons["main.retryUploads"].exists)
        XCTAssertFalse(
            app.buttons["main.clearFailed"].exists,
            "clear failed should only appear when there is something to clear"
        )

        let state = app.staticTexts["upload.state"]
        XCTAssertTrue(state.waitForExistence(timeout: 5))
        XCTAssertEqual(state.value as? String, "Очередь пуста")
    }

    func testUploadQueueShowsAndClearsFailedBundles() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-ui-upload-\(UUID().uuidString)", isDirectory: true)
        let configDirectory = root.appendingPathComponent("second-brain", isDirectory: true)
        let spool = configDirectory.appendingPathComponent("spool", isDirectory: true)
        try FileManager.default.createDirectory(at: spool, withIntermediateDirectories: true)
        let config: [String: Any] = ["server_url": "https://127.0.0.1:9"]
        try JSONSerialization.data(withJSONObject: config)
            .write(to: configDirectory.appendingPathComponent("config.json"))
        try Data("{not json".utf8).write(to: spool.appendingPathComponent("broken.json"))
        defer { try? FileManager.default.removeItem(at: root) }

        let app = configuredApp(configDirectory: configDirectory, uploadsEnabled: true)
        app.launch()

        let failed = app.staticTexts["upload.failedValue"]
        XCTAssertTrue(failed.waitForExistence(timeout: 10))
        let oneFailure = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value == %@", "Ошибки: 1"),
            object: failed
        )
        XCTAssertEqual(XCTWaiter().wait(for: [oneFailure], timeout: 10), .completed)
        XCTAssertTrue(app.staticTexts["upload.error"].waitForExistence(timeout: 5))

        let clear = app.buttons["main.clearFailed"]
        XCTAssertTrue(clear.waitForExistence(timeout: 5))
        clear.click()

        XCTAssertTrue(app.staticTexts["upload.empty"].waitForExistence(timeout: 10))
        XCTAssertEqual(failed.value as? String, "Ошибки: 0")
        XCTAssertFalse(
            FileManager.default.fileExists(
                atPath: spool.appendingPathComponent("failed/broken.json").path
            )
        )
    }

    func testDashboardShowsCountsQueueAndIngestionErrors() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "dashboard",
            uiTokens: "opencode,jarvis"
        )
        app.launch()

        XCTAssertTrue(app.staticTexts["dashboard.recordsTotal"].waitForExistence(timeout: 10))
        XCTAssertEqual(app.staticTexts["dashboard.recordsTotal"].value as? String, "3")
        XCTAssertEqual(app.staticTexts["dashboard.projectsTotal"].value as? String, "2")
        XCTAssertEqual(app.staticTexts["dashboard.clientsTotal"].value as? String, "1")
        XCTAssertTrue(app.staticTexts["dashboard.lastIngest"].waitForExistence(timeout: 5))

        let failedJob = element("dashboard.failedJob.10", in: app)
        XCTAssertTrue(failedJob.waitForExistence(timeout: 5))
        XCTAssertTrue(
            app.staticTexts.matching(
                NSPredicate(format: "value CONTAINS %@ OR label CONTAINS %@", "upstream timeout", "upstream timeout")
            ).firstMatch.waitForExistence(timeout: 5),
            "ingestion errors must be visible without reading server logs"
        )
        XCTAssertTrue(app.staticTexts["dashboard.errorsEmpty"].exists == false)
    }

    func testProjectsOpenSearchWithProjectFilter() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "projects"
        )
        app.launch()

        let row = app.buttons["project.row.github.com/example/meme"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["projects.count"].exists)
        row.click()

        XCTAssertTrue(app.textFields["search.field"].waitForExistence(timeout: 10))
        let badge = app.staticTexts["search.projectBadge"]
        XCTAssertTrue(badge.waitForExistence(timeout: 10))
        let expected = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value == %@", "Проект: meme"),
            object: badge
        )
        XCTAssertEqual(XCTWaiter().wait(for: [expected], timeout: 10), .completed)
        XCTAssertTrue(element("search.idle", in: app).waitForExistence(timeout: 5))
    }

    func testAgentsShowOpenCodeAndJarvisStatus() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "agents",
            uiTokens: "opencode,jarvis"
        )
        app.launch()

        XCTAssertTrue(app.staticTexts["agents.opencode.lastCapture"].waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts["agents.jarvis.lastMemory"].exists)
        XCTAssertEqual(
            app.staticTexts["agents.opencode.records"].value as? String,
            "2"
        )
        XCTAssertEqual(
            app.staticTexts["agents.jarvis.records"].value as? String,
            "1"
        )
        XCTAssertTrue(
            (app.staticTexts["agents.opencode.token"].value as? String)?
                .contains("opencode-macbook") == true,
            "token status should name the active OpenCode client"
        )
        XCTAssertTrue(
            (app.staticTexts["agents.jarvis.token"].value as? String)?
                .contains("jarvis") == true
        )
        XCTAssertTrue(app.staticTexts["agents.queue.failed"].exists)
    }

    func testGraphRendersNodesFiltersBySourceAndOpensCard() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "graph"
        )
        app.launch()

        let opencodeNode = app.buttons["graph.node.2"]
        XCTAssertTrue(opencodeNode.waitForExistence(timeout: 20))
        XCTAssertTrue(app.buttons["graph.node.1"].exists)
        XCTAssertTrue(app.buttons["graph.node.3"].exists)

        let sourceFilter = element("graph.sourceFilter", in: app)
        XCTAssertTrue(sourceFilter.waitForExistence(timeout: 5))
        sourceFilter.click()
        let jarvis = app.descendants(matching: .menuItem)
            .matching(NSPredicate(format: "title == %@", "Jarvis"))
            .firstMatch
        XCTAssertTrue(jarvis.waitForExistence(timeout: 5))
        jarvis.click()

        XCTAssertTrue(app.buttons["graph.node.1"].waitForExistence(timeout: 10))
        let removed = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "exists == false"),
            object: opencodeNode
        )
        XCTAssertEqual(
            XCTWaiter().wait(for: [removed], timeout: 10),
            .completed,
            "source filter must hide OpenCode nodes"
        )

        app.buttons["graph.node.1"].click()
        let title = app.staticTexts["record.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        XCTAssertEqual(title.value as? String, "Jarvis SQLite notes")
    }

    func testGraphShowsEmptyState() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "graph",
            uiGraph: "empty"
        )
        app.launch()

        XCTAssertTrue(element("graph.empty", in: app).waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts["Граф пока пуст"].exists)
    }

    func testGraphShowsErrorAndRetry() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiSection: "graph",
            uiGraph: "error"
        )
        app.launch()

        XCTAssertTrue(element("graph.error", in: app).waitForExistence(timeout: 20))
        let retry = app.buttons["graph.retry"]
        XCTAssertTrue(retry.exists)
        retry.click()
        XCTAssertTrue(element("graph.error", in: app).waitForExistence(timeout: 10))
    }

    func testMenuBarQuickSearchOpensCard() throws {
        let app = configuredApp(
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiMenuQuery: "sqlite"
        )
        app.launch()

        let window = app.windows.firstMatch
        XCTAssertTrue(window.waitForExistence(timeout: 20))
        window.buttons[XCUIIdentifierCloseWindow].click()

        let statusItem = app.statusItems["Second Brain"]
        XCTAssertTrue(statusItem.waitForExistence(timeout: 10))
        statusItem.click()

        let search = app.textFields["menubar.search"]
        XCTAssertTrue(search.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["menubar.queueState"].exists)
        XCTAssertTrue(app.staticTexts["menubar.pending"].exists)

        XCTAssertEqual(search.value as? String, "sqlite")
        let result = app.buttons["menubar.result.2"]
        XCTAssertTrue(result.waitForExistence(timeout: 15))
        result.click()

        let title = app.staticTexts["record.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 15))
        XCTAssertEqual(title.value as? String, "SQLite migration ordering")
    }

    func testMenuBarOpensDashboard() throws {
        let app = configuredApp(uiToken: "sb_ui_test", mockAPI: true)
        app.launch()
        XCTAssertTrue(app.windows.firstMatch.waitForExistence(timeout: 20))

        let statusItem = app.statusItems["Second Brain"]
        XCTAssertTrue(statusItem.waitForExistence(timeout: 10))
        statusItem.click()

        let dashboard = app.buttons["menubar.dashboard"]
        XCTAssertTrue(dashboard.waitForExistence(timeout: 10))
        dashboard.click()
        XCTAssertTrue(app.staticTexts["dashboard.recordsTotal"].waitForExistence(timeout: 15))
    }

    func testSearchScreenIsUsableInLightAppearance() throws {
        let app = configuredApp()
        app.launchArguments += ["-AppleInterfaceStyle", "Light"]
        app.launch()

        XCTAssertTrue(app.textFields["search.field"].waitForExistence(timeout: 10))
        XCTAssertTrue(element("search.idle", in: app).waitForExistence(timeout: 5))
        XCTAssertTrue(element("search.projectFilter", in: app).exists)
        XCTAssertTrue(element("search.sourceFilter", in: app).exists)
        XCTAssertTrue(element("search.dateFilter", in: app).exists)
    }

    func testSearchDetailRawTextCopyAndSelection() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-ui-search-\(UUID().uuidString)", isDirectory: true)
        let configDirectory = root.appendingPathComponent("second-brain", isDirectory: true)
        try FileManager.default.createDirectory(at: configDirectory, withIntermediateDirectories: true)
        let config: [String: Any] = ["server_url": "https://ui.test"]
        try JSONSerialization.data(withJSONObject: config)
            .write(to: configDirectory.appendingPathComponent("config.json"))
        defer { try? FileManager.default.removeItem(at: root) }

        let app = configuredApp(
            configDirectory: configDirectory,
            uploadsEnabled: false,
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiQuery: "sqlite",
            uiRawQuery: "numeric prefix"
        )
        app.launch()

        let search = app.textFields["search.field"]
        XCTAssertTrue(search.waitForExistence(timeout: 10))
        XCTAssertEqual(search.value as? String, "sqlite")

        let title = app.staticTexts["record.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        XCTAssertEqual(title.value as? String, "SQLite migration ordering")
        XCTAssertEqual(
            app.staticTexts["record.summary"].value as? String,
            "Migrations were sorted lexicographically."
        )
        XCTAssertTrue(app.staticTexts["record.date"].exists)
        XCTAssertTrue(app.staticTexts["Why did migration 004 run first?"].exists)
        XCTAssertTrue(app.staticTexts["sqlite"].exists)
        XCTAssertTrue(app.staticTexts["Sort migrations by their numeric prefix."].exists)
        XCTAssertTrue(app.staticTexts["github.com/example/meme"].exists)

        let matches = app.staticTexts["record.rawMatches"]
        XCTAssertTrue(matches.waitForExistence(timeout: 5))
        XCTAssertEqual(matches.value as? String, "1 совпадение")
        XCTAssertTrue(
            app.staticTexts.matching(
                NSPredicate(format: "value CONTAINS %@ OR label CONTAINS %@", "numeric prefix", "numeric prefix")
            ).firstMatch.waitForExistence(timeout: 5),
            "the raw-text view must show the matching lines"
        )

        NSPasteboard.general.clearContents()
        app.buttons["record.copyRaw"].click()
        let copiedRaw = NSPasteboard.general.string(forType: .string)
        XCTAssertTrue(copiedRaw?.contains("Migrations now sort by numeric prefix.") == true)
        XCTAssertTrue(
            copiedRaw?.contains("Use the migration test.") == true,
            "copy must include the full raw text, not only the filtered view"
        )

        search.click()
        app.typeKey(XCUIKeyboardKey.downArrow.rawValue, modifierFlags: [])
        XCTAssertTrue(
            app.staticTexts.matching(
                NSPredicate(format: "value == %@", "Jarvis SQLite notes")
            ).firstMatch.waitForExistence(timeout: 10),
            "selecting a result must load its detail"
        )

        let sourceFilter = element("search.sourceFilter", in: app)
        XCTAssertTrue(sourceFilter.exists)
        sourceFilter.click()
        let jarvis = app.descendants(matching: .menuItem)
            .matching(NSPredicate(format: "title == %@", "Jarvis"))
            .firstMatch
        XCTAssertTrue(jarvis.waitForExistence(timeout: 5))
        jarvis.click()
        let count = app.staticTexts["search.count"]
        XCTAssertTrue(count.waitForExistence(timeout: 10))
        XCTAssertEqual(count.value as? String, "Найдено: 1")

        let clear = app.buttons["search.clear"]
        XCTAssertTrue(clear.waitForExistence(timeout: 5))
        clear.click()
        XCTAssertTrue(element("search.idle", in: app).waitForExistence(timeout: 10))
    }

    func testSearchShowsServerErrorAndRetry() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-ui-search-error-\(UUID().uuidString)", isDirectory: true)
        let configDirectory = root.appendingPathComponent("second-brain", isDirectory: true)
        try FileManager.default.createDirectory(at: configDirectory, withIntermediateDirectories: true)
        let config: [String: Any] = ["server_url": "https://ui.test"]
        try JSONSerialization.data(withJSONObject: config)
            .write(to: configDirectory.appendingPathComponent("config.json"))
        defer { try? FileManager.default.removeItem(at: root) }

        let app = configuredApp(
            configDirectory: configDirectory,
            uploadsEnabled: false,
            uiToken: "sb_ui_test",
            mockAPI: true,
            uiQuery: "fail"
        )
        app.launch()

        XCTAssertTrue(app.textFields["search.field"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["search.error"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["search.retry"].exists)
    }

    func testSettingsOpens() throws {
        let app = configuredApp()
        app.launch()

        let settings = app.buttons["Settings…"]
        XCTAssertTrue(settings.waitForExistence(timeout: 10))
        settings.click()

        XCTAssertTrue(app.windows.element(boundBy: 1).waitForExistence(timeout: 10))
    }

    func testClosingTheWindowKeepsTheAppRunning() throws {
        let app = configuredApp()
        app.launch()

        let window = app.windows.firstMatch
        XCTAssertTrue(window.waitForExistence(timeout: 10))
        window.buttons[XCUIIdentifierCloseWindow].click()

        XCTAssertNotEqual(app.state, .notRunning)
    }

    func testLaunchAtLoginCanBeToggled() throws {
        let app = configuredApp()
        app.launch()

        app.buttons["Settings…"].click()
        let settings = app.windows["Second Brain Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 20))
        let toggle = settings.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))

        let initial = String(describing: toggle.value ?? "nil")
        toggle.click()
        let toggled = String(describing: toggle.value ?? "nil")
        XCTAssertNotEqual(toggled, initial, "Launch at login did not change state")

        // Restore the previous state so the test is repeatable.
        toggle.click()
        XCTAssertEqual(String(describing: toggle.value ?? "nil"), initial)
    }

    func testSettingsInstallsTheOpenCodePluginAndShowsItsStatus() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-ui-plugin-\(UUID().uuidString)", isDirectory: true)
        let source = root.appendingPathComponent("SecondBrainPlugin", isDirectory: true)
        let openCode = root.appendingPathComponent("opencode", isDirectory: true)
        let configDirectory = root.appendingPathComponent("second-brain", isDirectory: true)
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: configDirectory, withIntermediateDirectories: true)
        try Data("export default { id: 'second-brain.capture' }\n".utf8)
            .write(to: source.appendingPathComponent("index.ts"))
        try Data("export function setup() {}\n".utf8)
            .write(to: source.appendingPathComponent("setup.ts"))
        try Data(#"{"name":"second-brain-opencode-plugin","exports":{".":"./index.ts"}}"#.utf8)
            .write(to: source.appendingPathComponent("package.json"))
        try Data(#"{"server_url":"https://brain.example.test"}"#.utf8)
            .write(to: configDirectory.appendingPathComponent("config.json"))
        defer { try? FileManager.default.removeItem(at: root) }

        let app = configuredApp(
            openCodeDirectory: openCode,
            pluginSource: source,
            configDirectory: configDirectory
        )
        app.launch()

        let settingsButton = app.buttons["Settings…"]
        XCTAssertTrue(settingsButton.waitForExistence(timeout: 10))
        settingsButton.click()

        let settings = app.windows["Second Brain Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 20))

        let status = settings.staticTexts["settings.pluginStatus"]
        XCTAssertTrue(status.waitForExistence(timeout: 10))
        let notInstalled = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value == %@", "Не установлен"),
            object: status
        )
        XCTAssertEqual(XCTWaiter().wait(for: [notInstalled], timeout: 10), .completed)

        let install = settings.buttons["settings.installPlugin"]
        XCTAssertTrue(install.waitForExistence(timeout: 10))
        install.click()

        let installed = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "value == %@", "Установлен"),
            object: status
        )
        XCTAssertEqual(XCTWaiter().wait(for: [installed], timeout: 10), .completed)
        let installedPlugin = openCode.appendingPathComponent("plugins/second-brain")
        XCTAssertTrue(
            FileManager.default.fileExists(atPath: installedPlugin.appendingPathComponent("index.ts").path)
        )
        XCTAssertTrue(
            FileManager.default.fileExists(atPath: installedPlugin.appendingPathComponent("setup.ts").path),
            "a partial bundle must not look installed"
        )
        XCTAssertEqual(status.value as? String, "Установлен")

        let configURL = openCode.appendingPathComponent("opencode.json")
        let object = try XCTUnwrap(
            JSONSerialization.jsonObject(with: Data(contentsOf: configURL)) as? [String: Any]
        )
        let mcp = try XCTUnwrap(object["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        let entry = try XCTUnwrap(servers["second-brain"] as? [String: Any])
        XCTAssertEqual(entry["url"] as? String, "https://brain.example.test/mcp")
        XCTAssertEqual(entry["oauth"] as? Bool, false)
        let headers = try XCTUnwrap(entry["headers"] as? [String: Any])
        let authorization = try XCTUnwrap(headers["Authorization"] as? String)
        let tokenPath = configDirectory.appendingPathComponent("mcp_token").path
        XCTAssertTrue(authorization.hasPrefix("Bearer {file:"))
        XCTAssertTrue(authorization.hasSuffix("}"))
        let embeddedPath = String(
            authorization.dropFirst("Bearer {file:".count).dropLast()
        )
        if embeddedPath.hasPrefix("~") {
            // The app abbreviates paths under its home directory, which can
            // differ from the UI-test runner's home.
            XCTAssertTrue(
                tokenPath.hasSuffix(String(embeddedPath.dropFirst())),
                "auth header points to \(embeddedPath), expected \(tokenPath)"
            )
        } else {
            XCTAssertEqual(embeddedPath, tokenPath)
        }

        let reinstall = settings.buttons["settings.installPlugin"]
        let readyToReinstall = XCTNSPredicateExpectation(
            predicate: NSPredicate(format: "label == %@", "Переустановить"),
            object: reinstall
        )
        XCTAssertEqual(XCTWaiter().wait(for: [readyToReinstall], timeout: 10), .completed)
        reinstall.click()
        XCTAssertEqual(status.value as? String, "Установлен")
    }

    func testOnboardingShowsConnectionFormOnCleanInstall() throws {
        let app = XCUIApplication()
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-ui-\(UUID().uuidString)", isDirectory: true)
        app.launchEnvironment["SECOND_BRAIN_CONFIG_DIR"] = directory.path
        app.launch()

        XCTAssertTrue(app.textFields["onboarding.serverURL"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.secureTextFields["onboarding.adminToken"].exists)
        XCTAssertTrue(app.buttons["onboarding.connect"].exists)
    }

    private func element(_ identifier: String, in app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)[identifier].firstMatch
    }

    private func configuredApp(
        openCodeDirectory: URL? = nil,
        pluginSource: URL? = nil,
        configDirectory: URL? = nil,
        uploadsEnabled: Bool = false,
        uiToken: String? = nil,
        mockAPI: Bool = false,
        uiQuery: String? = nil,
        uiRawQuery: String? = nil,
        uiSection: String? = nil,
        uiTokens: String? = nil,
        uiGraph: String? = nil,
        uiMenuQuery: String? = nil
    ) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["SECOND_BRAIN_SKIP_ONBOARDING"] = "1"
        if !uploadsEnabled {
            app.launchEnvironment["SECOND_BRAIN_DISABLE_UPLOADS"] = "1"
        }
        if let openCodeDirectory {
            app.launchEnvironment["SECOND_BRAIN_OPENCODE_DIR"] = openCodeDirectory.path
        }
        if let pluginSource {
            app.launchEnvironment["SECOND_BRAIN_PLUGIN_SOURCE"] = pluginSource.path
        }
        if let configDirectory {
            app.launchEnvironment["SECOND_BRAIN_CONFIG_DIR"] = configDirectory.path
        }
        if let uiToken {
            app.launchEnvironment["SECOND_BRAIN_UI_TOKEN"] = uiToken
        }
        if mockAPI {
            app.launchEnvironment["SECOND_BRAIN_UI_MOCK"] = "1"
        }
        if let uiQuery {
            app.launchEnvironment["SECOND_BRAIN_UI_QUERY"] = uiQuery
        }
        if let uiRawQuery {
            app.launchEnvironment["SECOND_BRAIN_UI_RAW_QUERY"] = uiRawQuery
        }
        if let uiSection {
            app.launchEnvironment["SECOND_BRAIN_UI_SECTION"] = uiSection
        }
        if let uiTokens {
            app.launchEnvironment["SECOND_BRAIN_UI_TOKENS"] = uiTokens
        }
        if let uiGraph {
            app.launchEnvironment["SECOND_BRAIN_UI_GRAPH"] = uiGraph
        }
        if let uiMenuQuery {
            app.launchEnvironment["SECOND_BRAIN_UI_MENU_QUERY"] = uiMenuQuery
        }
        return app
    }
}
