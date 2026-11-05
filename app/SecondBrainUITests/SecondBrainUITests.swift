import Foundation
import XCTest

final class SecondBrainUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testLaunchShowsMainWindow() throws {
        let app = configuredApp()
        app.launch()

        XCTAssertTrue(app.windows.firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Second Brain"].waitForExistence(timeout: 5))
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
        XCTAssertEqual(
            headers["Authorization"] as? String,
            "Bearer {file:\(configDirectory.appendingPathComponent("mcp_token").path)}"
        )

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
        configDirectory: URL? = nil
    ) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["SECOND_BRAIN_SKIP_ONBOARDING"] = "1"
        app.launchEnvironment["SECOND_BRAIN_DISABLE_UPLOADS"] = "1"
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
