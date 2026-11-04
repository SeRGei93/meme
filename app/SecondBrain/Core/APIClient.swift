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

    func health() async throws -> Health {
        try await send(path: "health", authenticated: false)
    }

    func search(query: String, limit: Int? = nil, project: String? = nil) async throws -> SearchResponse {
        var items = [URLQueryItem(name: "q", value: query)]
        if let limit {
            items.append(URLQueryItem(name: "limit", value: String(limit)))
        }
        if let project {
            items.append(URLQueryItem(name: "project", value: project))
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

    // MARK: - Transport

    private func send<Response: Decodable>(
        path: String,
        query: [URLQueryItem] = [],
        authenticated: Bool = true
    ) async throws -> Response {
        let request = try makeRequest(path: path, query: query, authenticated: authenticated)
        let data: Data
        let response: URLResponse

        do {
            (data, response) = try await session.data(for: request)
        } catch let error as URLError {
            throw APIError.transport(localized: error.localizedDescription, retryable: error.code != .cancelled)
        } catch {
            throw APIError.transport(localized: error.localizedDescription, retryable: true)
        }

        guard let http = response as? HTTPURLResponse else {
            throw APIError.invalidResponse(status: -1)
        }

        guard (200..<300).contains(http.statusCode) else {
            throw Self.mapError(status: http.statusCode, headers: http.allHeaderFields, body: data)
        }

        do {
            return try Self.decoder.decode(Response.self, from: data)
        } catch {
            throw APIError.decoding
        }
    }

    private func makeRequest(path: String, query: [URLQueryItem], authenticated: Bool) throws -> URLRequest {
        let url = configuration.baseURL.appendingPathComponent(path)
        guard var components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            throw APIError.invalidResponse(status: -1)
        }
        if !query.isEmpty {
            components.queryItems = query
        }
        guard let finalURL = components.url else {
            throw APIError.invalidResponse(status: -1)
        }

        var request = URLRequest(url: finalURL, timeoutInterval: configuration.timeout)
        request.httpMethod = "GET"
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if authenticated {
            guard let token = configuration.token, !token.isEmpty else {
                throw APIError.unauthorized(requestID: nil)
            }
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
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
            let header = headers.first { key, _ in
                String(describing: key).caseInsensitiveCompare("Retry-After") == .orderedSame
            }?.value
            let retryAfter = (header as? String).flatMap(Int.init)
            return .rateLimited(retryAfter: retryAfter)
        default:
            return .server(
                status: status,
                code: envelope?.error.code ?? "http_\(status)",
                message: envelope?.error.message ?? "HTTP \(status)",
                requestID: requestID
            )
        }
    }

    static let decoder: JSONDecoder = {
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

/// Go marshals `time.Time` with nanosecond precision, which `ISO8601DateFormatter`
/// does not always accept. Try the common shapes and truncate the fraction when
/// needed.
enum APIDate {
    private static let fractional: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let whole: ISO8601DateFormatter = {
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
}
