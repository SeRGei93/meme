import Foundation

enum APIError: Error, Equatable, Sendable {
    case unauthorized(requestID: String?)
    case forbidden(requestID: String?)
    case notFound(requestID: String?)
    case rateLimited(retryAfter: Int?)
    case server(status: Int, code: String, message: String, requestID: String?)
    case invalidResponse(status: Int)
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

    var isRetryable: Bool {
        switch self {
        case .rateLimited, .transport(_, true):
            return true
        case .server(let status, _, _, _):
            return status >= 500
        case .unauthorized, .forbidden, .notFound, .invalidResponse, .decoding, .transport(_, false):
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
        case .rateLimited(let retryAfter):
            if let retryAfter {
                return "Слишком много запросов, повтор через \(retryAfter) с"
            }
            return "Слишком много запросов, попробуйте позже"
        case .server(let status, _, let message, _):
            return "Сервер ответил \(status): \(message)"
        case .invalidResponse(let status):
            return "Неожиданный ответ сервера (\(status))"
        case .decoding:
            return "Сервер вернул данные в неизвестном формате"
        case .transport(let localized, _):
            return "Нет связи с сервером: \(localized)"
        }
    }
}

/// The error body every endpoint returns, see `ErrorEnvelope` in `openapi.yaml`.
struct APIErrorEnvelope: Decodable, Equatable, Sendable {
    struct Payload: Decodable, Equatable, Sendable {
        let code: String
        let message: String
        let requestID: String?

        enum CodingKeys: String, CodingKey {
            case code
            case message
            case requestID = "request_id"
        }
    }

    let error: Payload
}
