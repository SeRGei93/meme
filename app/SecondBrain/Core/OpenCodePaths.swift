import Foundation

/// Locations OpenCode uses on this machine. The app writes the global plugin
/// directory and the global `opencode.json(c)`; both are outside the app sandbox
/// and are therefore overridable for tests and unusual `XDG_CONFIG_HOME`
/// setups.
enum OpenCodePaths {
    static func defaultDirectory(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        homeDirectory: String = NSHomeDirectory()
    ) -> URL {
        if let override = environment["SECOND_BRAIN_OPENCODE_DIR"], !override.isEmpty {
            return URL(fileURLWithPath: override, isDirectory: true)
        }

        let base: URL
        if let xdg = environment["XDG_CONFIG_HOME"], !xdg.isEmpty {
            base = URL(fileURLWithPath: xdg, isDirectory: true)
        } else {
            base = URL(fileURLWithPath: homeDirectory, isDirectory: true)
                .appendingPathComponent(".config", isDirectory: true)
        }
        return base.appendingPathComponent("opencode", isDirectory: true)
    }

    /// Shortens a path under the home directory to the `~/...` form OpenCode
    /// understands in `{file:...}` config substitutions.
    static func tildePath(for url: URL, homeDirectory: String = NSHomeDirectory()) -> String {
        let path = url.standardizedFileURL.path
        let home = URL(fileURLWithPath: homeDirectory, isDirectory: true).standardizedFileURL.path
        if path == home { return "~" }
        if path.hasPrefix(home + "/") {
            return "~" + path.dropFirst(home.count)
        }
        return path
    }
}
