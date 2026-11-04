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

    private func configuredApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["SECOND_BRAIN_SKIP_ONBOARDING"] = "1"
        return app
    }
}
