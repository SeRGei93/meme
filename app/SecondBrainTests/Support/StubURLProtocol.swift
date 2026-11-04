import Foundation

/// Minimal URLProtocol stub so APIClient tests never touch the network.
final class StubURLProtocol: URLProtocol {
    struct Response {
        var status: Int = 200
        var headers: [String: String] = ["Content-Type": "application/json"]
        var body: Data = Data()
    }

    private static let lock = NSLock()
    nonisolated(unsafe) private static var handler: (@Sendable (URLRequest) throws -> Response)?
    nonisolated(unsafe) private static var recordedRequests: [URLRequest] = []

    static func reset() {
        lock.lock()
        defer { lock.unlock() }
        handler = nil
        recordedRequests = []
    }

    static func setHandler(_ handler: @escaping @Sendable (URLRequest) throws -> Response) {
        lock.lock()
        defer { lock.unlock() }
        self.handler = handler
    }

    static var requests: [URLRequest] {
        lock.lock()
        defer { lock.unlock() }
        return recordedRequests
    }

    static func session() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.lock.lock()
        Self.recordedRequests.append(request)
        let handler = Self.handler
        Self.lock.unlock()

        guard let handler else {
            client?.urlProtocol(self, didFailWithError: URLError(.unknown))
            return
        }

        do {
            let response = try handler(request)
            let http = HTTPURLResponse(
                url: request.url ?? URL(string: "https://example.test")!,
                statusCode: response.status,
                httpVersion: "HTTP/1.1",
                headerFields: response.headers
            )!
            client?.urlProtocol(self, didReceive: http, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: response.body)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}
}
