import Foundation
import XCTest
@testable import SecondBrain

final class ConfigStoreTests: XCTestCase {
    private var directory: URL!
    private var store: ConfigStore!

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-config-\(UUID().uuidString)", isDirectory: true)
        store = ConfigStore(directory: directory)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
        store = nil
        directory = nil
        try super.tearDownWithError()
    }

    func testLoadingMissingFileReturnsNil() throws {
        XCTAssertNil(try store.load())
    }

    func testSaveAndLoadRoundTrip() throws {
        let config = AppConfig(
            serverURL: URL(string: "https://meme.aimerce.by")!,
            idleDebounceMilliseconds: 240_000,
            maxMessages: 500
        )

        try store.save(config)

        XCTAssertEqual(try store.load(), config)
    }

    func testFileUsesContractKeys() throws {
        try store.save(AppConfig(serverURL: URL(string: "https://brain.example.test")!))

        let data = try Data(contentsOf: store.fileURL)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["server_url"] as? String, "https://brain.example.test")
        XCTAssertNil(object["serverURL"])
    }

    func testSavingCreatesPrivateDirectoryAndFile() throws {
        try store.save(AppConfig(serverURL: URL(string: "https://brain.example.test")!))

        let directoryMode = try permissions(of: directory)
        let fileMode = try permissions(of: store.fileURL)

        XCTAssertEqual(directoryMode, 0o700)
        XCTAssertEqual(fileMode, 0o600)
    }

    func testMalformedFileThrowsInvalidConfig() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("{not json".utf8).write(to: store.fileURL)

        XCTAssertThrowsError(try store.load()) { error in
            XCTAssertEqual(error as? ConfigStoreError, .invalidConfig)
        }
    }

    func testDefaultDirectoryHonoursXDGConfigHome() {
        let url = ConfigStore.defaultDirectory(environment: ["XDG_CONFIG_HOME": "/tmp/xdg-test"])

        XCTAssertEqual(url.path, "/tmp/xdg-test/second-brain")
    }

    func testDefaultDirectoryFallsBackToHomeConfig() {
        let url = ConfigStore.defaultDirectory(environment: [:], homeDirectory: "/Users/tester")

        XCTAssertEqual(url.path, "/Users/tester/.config/second-brain")
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}
