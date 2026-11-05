import Foundation

/// Typed client for the Second Brain HTTP API. Every read endpoint is covered;
/// requests are plain `URLSession` + async/await, errors are mapped to
/// `APIError` so the UI can distinguish "retry later" from "new token needed".
struct APIClient: Sendable {
    struct Configuration: Sendable, Equatable {
        var baseURL: URL
        var token: String?
        var timeout: TimeInterval = 30
    }

    let configuration: Configuration
    private let session: URLSession

    init(configuration: Configuration, session: URLSession = .shared) {
        self.configuration = configuration
        self.session = session
    }

    init(baseURL: URL, token: String? = nil, session: URLSession = .shared) {
        self.init(configuration: Configuration(baseURL: baseURL, token: token), session: session)
    }

    // MARK: - Endpoints

    /// `/health` is public and, by contract, returns the same `Health` body with
    /// HTTP 503 when the database is unavailable. Both statuses are decoded.
    func health() async throws -> Health {
        let request = try makeRequest(
            path: "health", method: "GET", query: [], body: nil, authenticated: false
        )
        let (http, data) = try await perform(request)

        if http.statusCode == 200 || http.statusCode == 503,
           let health = try? Self.decoder.decode(Health.self, from: data) {
            return health
        }
        if http.statusCode == 200 || http.statusCode == 503 {
            throw APIError.decoding
        }
        throw Self.mapError(status: http.statusCode, headers: http.allHeaderFields, body: data)
    }

    func search(
        query: String,
        limit: Int? = nil,
        projectKey: String? = nil,
        source: String? = nil,
        since: Date? = nil,
        until: Date? = nil
    ) async throws -> SearchResponse {
        var items = [URLQueryItem(name: "q", value: query)]
        if let limit {
            items.append(URLQueryItem(name: "limit", value: String(limit)))
        }
        if let projectKey, !projectKey.isEmpty {
            items.append(URLQueryItem(name: "project_key", value: projectKey))
        }
        if let source, !source.isEmpty {
            items.append(URLQueryItem(name: "source", value: source))
        }
        if let since {
            items.append(URLQueryItem(name: "since", value: APIDate.string(from: since)))
        }
        if let until {
            items.append(URLQueryItem(name: "until", value: APIDate.string(from: until)))
        }
        return try await send(path: "api/v1/search", query: items)
    }

    func record(id: Int64) async throws -> Record {
        try await send(path: "api/v1/records/\(id)")
    }

    func projects() async throws -> ProjectList {
        try await send(path: "api/v1/projects")
    }

    func stats() async throws -> Stats {
        try await send(path: "api/v1/stats")
    }

    // MARK: - Ingest

    /// Uploads one spool bundle. `Idempotency-Key` is always the bundle id, so a
    /// retry after a timeout or a 5xx replays instead of creating a duplicate.
    func ingest(_ bundle: SpoolBundle) async throws -> IngestResponse? {
        let body = try bundle.ingestBody()
        let request = try makeRequest(
            path: "api/v1/ingest",
            method: "POST",
            query: [],
            body: body,
            authenticated: true,
            extraHeaders: ["Idempotency-Key": bundle.idempotencyKey]
        )
        let (http, data) = try await perform(request)
        guard (200..<300).contains(http.statusCode) else {
            throw Self.mapError(status: http.statusCode, headers: http.allHeaderFields, body: data)
        }
        // The upload queue only needs the status code: any 2xx means the
        // bundle is safely stored (a replay is just as good). An older server
        // or a proxy without a JSON body must not keep the file forever.
        return try? Self.decoder.decode(IngestResponse.self, from: data)
    }

    // MARK: - Clients (admin)

    func listClients() async throws -> ClientList {
        try await send(path: "api/v1/clients")
    }

    func createClient(_ request: CreateClientRequest) async throws -> CreateClientResponse {
        let body = try JSONEncoder().encode(request)
        return try await send(path: "api/v1/clients", method: "POST", body: body)
    }

    func revokeClient(id: Int64) async throws {
        try await sendNoContent(path: "api/v1/clients/\(id)", method: "DELETE")
    }

    // MARK: - Transport

    private func send<Response: Decodable>(
        path: String,
        method: String = "GET",
        query: [URLQueryItem] = [],
        body: Data? = nil,
        authenticated: Bool = true,
        extraHeaders: [String: String] = [:]
    ) async throws -> Response {
        let request = try makeRequest(
            path: path, method: method, query: query, body: body,
            authenticated: authenticated, extraHeaders: extraHeaders
        )
        let (http, data) = try await perform(request)

        guard (200..<300).contains(http.statusCode) else {
            throw Self.mapError(status: http.statusCode, headers: http.allHeaderFields, body: data)
        }

        do {
            return try Self.decoder.decode(Response.self, from: data)
        } catch {
            throw APIError.decoding
        }
    }

    private func sendNoContent(
        path: String,
        method: String,
        body: Data? = nil
    ) async throws {
        let request = try makeRequest(
            path: path, method: method, query: [], body: body, authenticated: true
        )
        let (http, data) = try await perform(request)
        guard (200..<300).contains(http.statusCode) else {
            throw Self.mapError(status: http.statusCode, headers: http.allHeaderFields, body: data)
        }
    }

    private func perform(_ request: URLRequest) async throws -> (HTTPURLResponse, Data) {
        do {
            let (data, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                throw APIError.invalidResponse(status: -1)
            }
            return (http, data)
        } catch let error as APIError {
            throw error
        } catch let error as URLError {
            throw APIError.transport(localized: error.localizedDescription, retryable: error.code != .cancelled)
        } catch {
            throw APIError.transport(localized: error.localizedDescription, retryable: true)
        }
    }

    private func makeRequest(
        path: String,
        method: String,
        query: [URLQueryItem],
        body: Data?,
        authenticated: Bool,
        extraHeaders: [String: String] = [:]
    ) throws -> URLRequest {
        let url = configuration.baseURL.appendingPathComponent(path)
        guard var components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            throw APIError.invalidURL
        }
        if !query.isEmpty {
            components.queryItems = query
        }
        guard let finalURL = components.url else {
            throw APIError.invalidURL
        }

        var request = URLRequest(url: finalURL, timeoutInterval: configuration.timeout)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if authenticated {
            guard let token = configuration.token, !token.isEmpty else {
                throw APIError.unauthorized(requestID: nil)
            }
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        for (name, value) in extraHeaders {
            request.setValue(value, forHTTPHeaderField: name)
        }
        return request
    }

    // MARK: - Errors and decoding

    private static func mapError(status: Int, headers: [AnyHashable: Any], body: Data) -> APIError {
        let envelope = try? decoder.decode(APIErrorEnvelope.self, from: body)
        let requestID = envelope?.error.requestID

        switch status {
        case 401:
            return .unauthorized(requestID: requestID)
        case 403:
            return .forbidden(requestID: requestID)
        case 404:
            return .notFound(requestID: requestID)
        case 429:
            let header = headerValue("Retry-After", in: headers)
            let retryAfter = header.flatMap(parseRetryAfter) ?? envelope?.error.details?.retryAfterSeconds
            return .rateLimited(retryAfter: retryAfter, requestID: requestID)
        default:
            return .server(
                status: status,
                code: envelope?.error.code ?? "http_\(status)",
                message: envelope?.error.message ?? "HTTP \(status)",
                requestID: requestID
            )
        }
    }

    private static func headerValue(_ name: String, in headers: [AnyHashable: Any]) -> String? {
        for (key, value) in headers where String(describing: key).caseInsensitiveCompare(name) == .orderedSame {
            return value as? String
        }
        return nil
    }

    /// `Retry-After` is either delay-seconds or an HTTP-date.
    private static func parseRetryAfter(_ raw: String) -> Int? {
        if let seconds = Int(raw) {
            return max(0, seconds)
        }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        guard let date = formatter.date(from: raw) else { return nil }
        return max(0, Int(date.timeIntervalSinceNow.rounded(.up)))
    }

    nonisolated(unsafe) static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let string = try container.decode(String.self)
            guard let date = APIDate.parse(string) else {
                throw DecodingError.dataCorruptedError(
                    in: container,
                    debugDescription: "Unsupported RFC 3339 date: \(string)"
                )
            }
            return date
        }
        return decoder
    }()
}

/// Go marshals `time.Time` with nanosecond precision. Current Foundation parses
/// that directly; the truncation path stays as a guard for older toolchains.
enum APIDate {
    nonisolated(unsafe) private static let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    nonisolated(unsafe) private static let whole: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    static func parse(_ string: String) -> Date? {
        if let date = fractional.date(from: string) { return date }
        if let date = whole.date(from: string) { return date }

        guard let dot = string.firstIndex(of: ".") else { return nil }
        let suffix = string[dot...]
        guard let zoneIndex = suffix.firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) else {
            return nil
        }
        let digits = suffix[suffix.index(after: dot)..<zoneIndex].prefix(3)
        guard !digits.isEmpty else { return nil }
        let rebuilt = String(string[..<dot]) + "." + String(digits) + String(suffix[zoneIndex...])
        return fractional.date(from: rebuilt)
    }

    static func string(from date: Date) -> String {
        fractional.string(from: date)
    }
}
