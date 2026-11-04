import Foundation
import XCTest
@testable import SecondBrain

final class OpenCodeConfigTests: XCTestCase {
    private var directory: URL!
    private var config: OpenCodeConfig!
    private let serverURL = URL(string: "https://meme.aimerce.by")!
    private let tokenPath = "~/.config/second-brain/mcp_token"

    override func setUpWithError() throws {
        try super.setUpWithError()
        directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("second-brain-opencode-\(UUID().uuidString)", isDirectory: true)
        config = OpenCodeConfig(directory: directory)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
        config = nil
        directory = nil
        try super.tearDownWithError()
    }

    func testConfigureCreatesAValidRemoteServerWithABearerFileReference() throws {
        let fileURL = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        XCTAssertEqual(fileURL.lastPathComponent, "opencode.json")
        XCTAssertEqual(try permissions(of: fileURL), 0o600)

        let object = try json(fileURL)
        let entry = try entry(in: object)
        XCTAssertEqual(entry["type"] as? String, "remote")
        XCTAssertEqual(entry["url"] as? String, "https://meme.aimerce.by/mcp")
        XCTAssertEqual(entry["oauth"] as? Bool, false)
        XCTAssertEqual(entry["codemode"] as? Bool, false)
        let headers = try XCTUnwrap(entry["headers"] as? [String: Any])
        XCTAssertEqual(
            headers["Authorization"] as? String,
            "Bearer {file:~/.config/second-brain/mcp_token}"
        )

        let text = try String(contentsOf: fileURL, encoding: .utf8)
        XCTAssertFalse(text.contains("sb_"), "the token itself must never be written to opencode.json")
        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            .configured
        )
    }

    func testConfigurePreservesUnrelatedSettingsAndServers() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("""
        {
          "$schema": "https://opencode.ai/config.json",
          "model": "anthropic/claude-sonnet-5-5",
          "plugins": ["opencode-acme-plugin"],
          "mcp": {
            "timeout": 30000,
            "servers": {
              "other": {"type": "remote", "url": "https://other.example.test/mcp"}
            }
          }
        }
        """.utf8).write(to: directory.appendingPathComponent("opencode.json"))

        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        let object = try json(directory.appendingPathComponent("opencode.json"))
        XCTAssertEqual(object["$schema"] as? String, "https://opencode.ai/config.json")
        XCTAssertEqual(object["model"] as? String, "anthropic/claude-sonnet-5-5")
        XCTAssertEqual(object["plugins"] as? [String], ["opencode-acme-plugin"])
        let mcp = try XCTUnwrap(object["mcp"] as? [String: Any])
        XCTAssertEqual(mcp["timeout"] as? Int, 30000)
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        let other = try XCTUnwrap(servers["other"] as? [String: Any])
        XCTAssertEqual(other["url"] as? String, "https://other.example.test/mcp")
        XCTAssertNotNil(servers["second-brain"])
    }

    func testConfigureMergesIntoAnExistingSecondBrainEntry() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("""
        {"mcp":{"servers":{"second-brain":{
          "type":"remote",
          "url":"https://old.example.test/mcp",
          "oauth":true,
          "enabled":true,
          "timeout":5000,
          "headers":{"X-Custom":"keep","Authorization":"Bearer old"}
        }}}}
        """.utf8).write(to: directory.appendingPathComponent("opencode.json"))

        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        let entry = try entry(in: try json(directory.appendingPathComponent("opencode.json")))
        XCTAssertEqual(entry["url"] as? String, "https://meme.aimerce.by/mcp")
        XCTAssertEqual(entry["oauth"] as? Bool, false)
        XCTAssertNil(entry["timeout"], "a V1 scalar timeout would make the V2 entry invalid")
        XCTAssertNil(entry["enabled"])
        let headers = try XCTUnwrap(entry["headers"] as? [String: Any])
        XCTAssertEqual(headers["X-Custom"] as? String, "keep")
        XCTAssertEqual(headers["Authorization"] as? String, "Bearer {file:~/.config/second-brain/mcp_token}")
    }

    func testConfigurePreservesAStructuredTimeout() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("""
        {"mcp":{"servers":{"second-brain":{
          "type":"remote",
          "disabled":true,
          "protocol":"streamable-http",
          "timeout":{"execution":5000},
          "headers":{"X-Custom":"keep"}
        }}}}
        """.utf8).write(to: directory.appendingPathComponent("opencode.json"))

        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        let entry = try entry(in: try json(directory.appendingPathComponent("opencode.json")))
        let timeout = try XCTUnwrap(entry["timeout"] as? [String: Any])
        XCTAssertEqual(timeout["execution"] as? Int, 5000)
        XCTAssertEqual(entry["disabled"] as? Bool, true)
        XCTAssertEqual(entry["protocol"] as? String, "streamable-http")
        XCTAssertEqual(
            (entry["headers"] as? [String: Any])?["X-Custom"] as? String,
            "keep"
        )
    }

    func testConfigureKeepsASymlinkedConfigAsALink() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let target = directory.appendingPathComponent("dotfiles-opencode.json")
        try Data(#"{"model":"anthropic/claude-sonnet-5-5"}"#.utf8).write(to: target)
        let link = directory.appendingPathComponent("opencode.json")
        try FileManager.default.createSymbolicLink(at: link, withDestinationURL: target)

        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        let values = try link.resourceValues(forKeys: [.isSymbolicLinkKey])
        XCTAssertEqual(values.isSymbolicLink, true)
        XCTAssertEqual(try json(target)["model"] as? String, "anthropic/claude-sonnet-5-5")
        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            OpenCodeConfigState.configured
        )
    }

    func testConfigureDoesNotRewriteAnAlreadyConfiguredFile() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appendingPathComponent("opencode.json")
        try Data("""
        {
          // a comment that must survive a no-op configure
          "mcp": {"servers": {"second-brain": {
            "type": "remote",
            "url": "https://meme.aimerce.by/mcp",
            "oauth": false,
            "codemode": false,
            "headers": {"Authorization": "Bearer {file:~/.config/second-brain/mcp_token}"}
          }}}
        }
        """.utf8).write(to: file)
        let before = try Data(contentsOf: file)

        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        XCTAssertEqual(try Data(contentsOf: file), before)
        XCTAssertTrue(try String(contentsOf: file, encoding: .utf8).contains("// a comment"))
    }

    func testStatusRejectsALegacyEnabledFlag() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("""
        {"mcp":{"servers":{"second-brain":{
          "type":"remote","url":"https://meme.aimerce.by/mcp","oauth":false,
          "codemode":false,"enabled":true,
          "headers":{"Authorization":"Bearer {file:~/.config/second-brain/mcp_token}"}
        }}}}
        """.utf8).write(to: directory.appendingPathComponent("opencode.json"))

        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            OpenCodeConfigState.notConfigured
        )
    }

    func testStatusRejectsAScalarTimeout() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("""
        {"mcp":{"servers":{"second-brain":{
          "type":"remote","url":"https://meme.aimerce.by/mcp","oauth":false,
          "codemode":false,"timeout":5000,
          "headers":{"Authorization":"Bearer {file:~/.config/second-brain/mcp_token}"}
        }}}}
        """.utf8).write(to: directory.appendingPathComponent("opencode.json"))

        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            OpenCodeConfigState.notConfigured
        )
    }

    func testConfigureReadsJSONCAndKeepsTheExistingFile() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let fileURL = directory.appendingPathComponent("opencode.jsonc")
        try Data("""
        {
          // keep the schema
          "$schema": "https://opencode.ai/config.json",
          "model": "anthropic/claude-sonnet-5-5", // trailing
          /* a block comment with a URL https://example.test */
          "mcp": {
            "servers": {
              "other": {"type": "remote", "url": "https://other.example.test/mcp",},
            },
          },
        }
        """.utf8).write(to: fileURL)

        let written = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        XCTAssertEqual(written, fileURL)
        XCTAssertFalse(FileManager.default.fileExists(atPath: directory.appendingPathComponent("opencode.json").path))
        let object = try json(fileURL)
        XCTAssertEqual(object["model"] as? String, "anthropic/claude-sonnet-5-5")
        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            .configured
        )
    }

    func testConfigurePrefersJSONWhenBothFilesExist() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: directory.appendingPathComponent("opencode.json"))
        try Data("{}".utf8).write(to: directory.appendingPathComponent("opencode.jsonc"))

        let written = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        XCTAssertEqual(written.lastPathComponent, "opencode.json")
    }

    func testStatusDistinguishesMissingStaleAndBrokenConfigs() throws {
        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            .notConfigured
        )

        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try Data(#"{"mcp":{"servers":{}}}"#.utf8)
            .write(to: directory.appendingPathComponent("opencode.json"))
        XCTAssertEqual(
            config.status(serverURL: serverURL, tokenPath: tokenPath).state,
            .notConfigured
        )

        try Data("{not json".utf8).write(to: directory.appendingPathComponent("opencode.json"))
        guard case .error(let message) = config.status(serverURL: serverURL, tokenPath: tokenPath).state else {
            return XCTFail("expected .error")
        }
        XCTAssertFalse(message.isEmpty)
    }

    func testStatusIsNotConfiguredWhenTheURLChanged() throws {
        _ = try config.configure(serverURL: serverURL, tokenPath: tokenPath)

        XCTAssertEqual(
            config.status(
                serverURL: URL(string: "https://new.example.test")!,
                tokenPath: tokenPath
            ).state,
            .notConfigured
        )
    }

    func testInvalidJSONIsNotOverwritten() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let fileURL = directory.appendingPathComponent("opencode.json")
        let original = "{not json"
        try Data(original.utf8).write(to: fileURL)

        XCTAssertThrowsError(try config.configure(serverURL: serverURL, tokenPath: tokenPath))
        XCTAssertEqual(try String(contentsOf: fileURL, encoding: .utf8), original)
    }

    func testConfigureKeepsABasePath() throws {
        _ = try config.configure(
            serverURL: URL(string: "https://brain.example.test/brain/")!,
            tokenPath: tokenPath
        )

        let entry = try entry(in: try json(directory.appendingPathComponent("opencode.json")))
        XCTAssertEqual(entry["url"] as? String, "https://brain.example.test/brain/mcp")
    }

    private func json(_ url: URL) throws -> [String: Any] {
        let data = try Data(contentsOf: url)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func entry(in object: [String: Any]) throws -> [String: Any] {
        let mcp = try XCTUnwrap(object["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        return try XCTUnwrap(servers["second-brain"] as? [String: Any])
    }

    private func permissions(of url: URL) throws -> Int {
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return try XCTUnwrap(attributes[.posixPermissions] as? NSNumber).intValue
    }
}
