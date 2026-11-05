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
        XCTAssertTrue(settings.waitForExistence(timeout: 10))
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
        XCTAssertTrue(settings.waitForExistence(timeout: 10))

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

    private func configuredApp(
        openCodeDirectory: URL? = nil,
        pluginSource: URL? = nil,
        configDirectory: URL? = nil,
        uploadsEnabled: Bool = false
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
        return app
    }
}
