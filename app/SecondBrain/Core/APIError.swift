import Foundation

enum APIError: Error, Equatable, Sendable {
    case unauthorized(requestID: String?)
    case forbidden(requestID: String?)
    case notFound(requestID: String?)
    case rateLimited(retryAfter: Int?, requestID: String?)
    case server(status: Int, code: String, message: String, field: String?, requestID: String?)
    case invalidResponse(status: Int)
    case invalidURL
    case decoding
    case transport(localized: String, retryable: Bool)

    /// The token is missing, revoked or lacks access: the app must stop retrying
    /// and ask the user for a new token.
    var requiresReauthentication: Bool {
        switch self {
        case .unauthorized, .forbidden:
            return true
        default:
            return false
        }
    }

    /// Mirrors `docs/CONTRACTS.md` §5.1: network errors, `408`, `425`, `429` and
    /// `500/502/503/504` are safe to retry (`429` already maps to `.rateLimited`).
    var isRetryable: Bool {
        switch self {
        case .rateLimited, .transport(_, true):
            return true
        case .server(let status, _, _, _, _):
            return [408, 425, 500, 502, 503, 504].contains(status)
        case .unauthorized, .forbidden, .notFound, .invalidResponse, .invalidURL, .decoding,
             .transport(_, false):
            return false
        }
    }

    var displayMessage: String {
        switch self {
        case .unauthorized:
            return "Нужен новый токен"
        case .forbidden:
            return "У токена нет доступа к этому действию"
        case .notFound:
            return "Не найдено на сервере"
        case .rateLimited(let retryAfter, _):
            if let retryAfter {
                return "Слишком много запросов, повтор через \(retryAfter) с"
            }
            return "Слишком много запросов, попробуйте позже"
        case .server(let status, _, let message, _, _):
            return "Сервер ответил \(status): \(message)"
        case .invalidResponse(let status):
            return "Неожиданный ответ сервера (\(status))"
        case .invalidURL:
            return "Некорректный адрес сервера"
        case .decoding:
            return "Сервер вернул данные в неизвестном формате"
        case .transport(let localized, _):
            return "Нет связи с сервером: \(localized)"
        }
    }
}

extension APIError: LocalizedError {
    var errorDescription: String? { displayMessage }
}

/// The error body every endpoint returns, see `ErrorEnvelope` in `openapi.yaml`.
struct APIErrorEnvelope: Decodable, Equatable, Sendable {
    struct Payload: Decodable, Equatable, Sendable {
        struct Details: Decodable, Equatable, Sendable {
            let retryAfterSeconds: Int?
            let field: String?

            enum CodingKeys: String, CodingKey {
                case retryAfterSeconds = "retry_after_seconds"
                case field
            }
        }

        let code: String
        let message: String
        let requestID: String?
        let details: Details?

        enum CodingKeys: String, CodingKey {
            case code
            case message
            case requestID = "request_id"
            case details
        }
    }

    let error: Payload
}
