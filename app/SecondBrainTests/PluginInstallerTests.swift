import Foundation
import XCTest
@testable import SecondBrain

final class PluginInstallerTests: XCTestCase {
    private var root: URL!
    private var openCodeDirectory: URL!
    private var sourceDirectory: URL!
    private var installer: PluginInstaller!

    override func setUpWithError() throws {
        try super.setUpWithError()
        root = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-plugin-\(UUID().uuidString)", isDirectory: true)
        openCodeDirectory = root.appendingPathComponent("opencode", isDirectory: true)
        sourceDirectory = root.appendingPathComponent("SecondBrainPlugin", isDirectory: true)
        try writeSource()
        installer = PluginInstaller(
            openCodeDirectory: openCodeDirectory,
            sourceDirectory: sourceDirectory
        )
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: root)
        installer = nil
        sourceDirectory = nil
        openCodeDirectory = nil
        root = nil
        try super.tearDownWithError()
    }

    func testStatusIsNotInstalledOnACleanMachine() {
        let status = installer.status()

        XCTAssertEqual(status.state, .notInstalled)
        XCTAssertEqual(
            status.directory.path,
            openCodeDirectory.appendingPathComponent("plugins/second-brain").path
        )
    }

    func testInstallCopiesOnlyRuntimePluginFiles() throws {
        let status = try installer.install()

        XCTAssertEqual(status.state, .installed)
        let destination = status.directory
        XCTAssertTrue(FileManager.default.fileExists(atPath: destination.path))

        for name in ["index.ts", "setup.ts", "package.json", "README.md"] {
            XCTAssertTrue(
                FileManager.default.fileExists(atPath: destination.appendingPathComponent(name).path),
                "\(name) must be installed"
            )
        }
        XCTAssertFalse(
            FileManager.default.fileExists(atPath: destination.appendingPathComponent("package-lock.json").path)
        )
        XCTAssertFalse(
            FileManager.default.fileExists(atPath: destination.appendingPathComponent("test").path)
        )
        XCTAssertFalse(
            FileManager.default.fileExists(atPath: destination.appendingPathComponent("node_modules").path)
        )

        let installed = try String(
            contentsOf: destination.appendingPathComponent("index.ts"),
            encoding: .utf8
        )
        XCTAssertEqual(installed, "export default { id: 'second-brain.capture' }\n")
        XCTAssertEqual(try permissions(of: destination), 0o755)
        XCTAssertEqual(try permissions(of: destination.appendingPathComponent("index.ts")), 0o644)
    }

    func testReinstallOverwritesAModifiedInstallation() throws {
        let destination = try installer.install().directory
        try Data("// tampered\n".utf8).write(to: destination.appendingPathComponent("index.ts"))

        XCTAssertEqual(installer.status().state, .outdated)

        XCTAssertEqual(try installer.install().state, .installed)
        XCTAssertEqual(
            try String(contentsOf: destination.appendingPathComponent("index.ts"), encoding: .utf8),
            "export default { id: 'second-brain.capture' }\n"
        )
    }

    func testReinstallDoesNotTouchOtherPlugins() throws {
        let plugins = openCodeDirectory.appendingPathComponent("plugins", isDirectory: true)
        let other = plugins.appendingPathComponent("someone-else", isDirectory: true)
        try FileManager.default.createDirectory(at: other, withIntermediateDirectories: true)
        try Data("keep".utf8).write(to: other.appendingPathComponent("index.ts"))

        _ = try installer.install()

        XCTAssertEqual(
            try String(contentsOf: other.appendingPathComponent("index.ts"), encoding: .utf8),
            "keep"
        )
    }

    func testMissingSourceDirectoryIsReportedInsteadOfCrashing() {
        let broken = PluginInstaller(
            openCodeDirectory: openCodeDirectory,
            sourceDirectory: root.appendingPathComponent("missing", isDirectory: true)
        )

        guard case .error(let message) = broken.status().state else {
            return XCTFail("expected .error, got \(broken.status().state)")
        }
        XCTAssertTrue(message.contains("плагин"))
        XCTAssertThrowsError(try broken.install()) { error in
            XCTAssertEqual(error as? PluginInstallError, .sourceUnavailable)
        }
    }

    func testMissingRequiredSourceFilesAreReported() throws {
        try FileManager.default.removeItem(at: sourceDirectory.appendingPathComponent("package.json"))
        let broken = PluginInstaller(
            openCodeDirectory: openCodeDirectory,
            sourceDirectory: sourceDirectory
        )

        guard case .error(let message) = broken.status().state else {
            return XCTFail("expected .error, got \(broken.status().state)")
        }
        XCTAssertTrue(message.contains("package.json"))
        XCTAssertThrowsError(try broken.install()) { error in
            guard case .missingSourceFiles(let files) = error as? PluginInstallError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertEqual(files, ["package.json"])
        }
    }

    func testUnwritableDestinationIsReportedWithThePath() throws {
        let plugins = openCodeDirectory.appendingPathComponent("plugins", isDirectory: true)
        try FileManager.default.createDirectory(at: openCodeDirectory, withIntermediateDirectories: true)
        try Data("not a directory".utf8).write(to: plugins)

        XCTAssertThrowsError(try installer.install()) { error in
            guard case .pathOccupied(let path) = error as? PluginInstallError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertEqual(path, plugins.path)
        }
        XCTAssertEqual(installer.status().state, .notInstalled)
    }

    func testRealPermissionErrorIsReported() throws {
        let plugins = openCodeDirectory.appendingPathComponent("plugins", isDirectory: true)
        try FileManager.default.createDirectory(at: plugins, withIntermediateDirectories: true)
        try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: plugins.path)
        defer {
            try? FileManager.default.setAttributes(
                [.posixPermissions: 0o755],
                ofItemAtPath: plugins.path
            )
        }

        XCTAssertThrowsError(try installer.install()) { error in
            guard case .permissionDenied(let path) = error as? PluginInstallError else {
                return XCTFail("unexpected error \(error)")
            }
            XCTAssertEqual(path, installer.destination.path)
        }
    }

    func testInstallDoesNotLeaveStagingDirectoriesInsidePlugins() throws {
        let plugins = openCodeDirectory.appendingPathComponent("plugins", isDirectory: true)
        try FileManager.default.createDirectory(at: plugins, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(
            at: plugins.appendingPathComponent(".second-brain-stale"),
            withIntermediateDirectories: true
        )

        _ = try installer.install()

        let names = try FileManager.default.contentsOfDirectory(atPath: plugins.path)
        XCTAssertEqual(names.filter { $0.hasPrefix(".second-brain-") }, [])
    }

    func testTildePathOnlyShortensPathsUnderHome() {
        XCTAssertEqual(
            OpenCodePaths.tildePath(
                for: URL(fileURLWithPath: "/Users/tester/.config/second-brain/mcp_token"),
                homeDirectory: "/Users/tester"
            ),
            "~/.config/second-brain/mcp_token"
        )
        XCTAssertEqual(
            OpenCodePaths.tildePath(
                for: URL(fileURLWithPath: "/Users/tester"),
                homeDirectory: "/Users/tester"
            ),
            "~"
        )
        XCTAssertEqual(
            OpenCodePaths.tildePath(
                for: URL(fileURLWithPath: "/Users/tester2/token"),
                homeDirectory: "/Users/tester"
            ),
            "/Users/tester2/token"
        )
    }

    func testDefaultSourceDirectoryHonoursTheLaunchOverride() {
        let override = root.appendingPathComponent("override", isDirectory: true)

        let directory = PluginInstaller.defaultSourceDirectory(
            environment: ["SECOND_BRAIN_PLUGIN_SOURCE": override.path],
            bundle: .main
        )

        XCTAssertEqual(directory, override)
    }

    func testOpenCodeDirectoryHonoursXDGAndTheLaunchOverride() {
        XCTAssertEqual(
            OpenCodePaths.defaultDirectory(
                environment: ["XDG_CONFIG_HOME": "/tmp/xdg-test"],
                homeDirectory: "/Users/tester"
            ).path,
            "/tmp/xdg-test/opencode"
        )
        XCTAssertEqual(
            OpenCodePaths.defaultDirectory(
                environment: ["XDG_CONFIG_HOME": "/tmp/xdg-test", "SECOND_BRAIN_OPENCODE_DIR": "/tmp/custom"],
                homeDirectory: "/Users/tester"
            ).path,
            "/tmp/custom"
        )
        XCTAssertEqual(
            OpenCodePaths.defaultDirectory(environment: [:], homeDirectory: "/Users/tester").path,
            "/Users/tester/.config/opencode"
        )
    }

    private func writeSource() throws {
        try FileManager.default.createDirectory(at: sourceDirectory, withIntermediateDirectories: true)
        try Data("export default { id: 'second-brain.capture' }\n".utf8)
            .write(to: sourceDirectory.appendingPathComponent("index.ts"))
        try Data("export function setup() {}\n".utf8)
            .write(to: sourceDirectory.appendingPathComponent("setup.ts"))
        try Data(#"{"name":"second-brain-opencode-plugin","version":"0.1.0"}"#.utf8)
            .write(to: sourceDirectory.appendingPathComponent("package.json"))
        try Data("# plugin\n".utf8)
            .write(to: sourceDirectory.appendingPathComponent("README.md"))
        try Data("{}".utf8)
            .write(to: sourceDirectory.appendingPathComponent("package-lock.json"))

        let test = sourceDirectory.appendingPathComponent("test", isDirectory: true)
        try FileManager.default.createDirectory(at: test, withIntermediateDirectories: true)
        try Data("ignored".utf8).write(to: test.appendingPathComponent("setup.test.ts"))

        let modules = sourceDirectory.appendingPathComponent("node_modules/example", isDirectory: true)
        try FileManager.default.createDirectory(at: modules, withIntermediateDirectories: true)
        try Data("ignored".utf8).write(to: modules.appendingPathComponent("index.js"))
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}
