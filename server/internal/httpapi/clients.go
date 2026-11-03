package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/store"
)

const maxBodyBytes = 10 << 20 // 10 MiB, from the v1 contract.

type createClientRequest struct {
	Name   string       `json:"name"`
	Kind   auth.Kind    `json:"kind"`
	Scopes []auth.Scope `json:"scopes"`
}

type createClientResponse struct {
	Client clientResponse `json:"client"`
	Token  string         `json:"token"`
}

type clientResponse struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Kind       auth.Kind    `json:"kind"`
	Scopes     []auth.Scope `json:"scopes"`
	Source     *string      `json:"source"`
	CreatedAt  time.Time    `json:"created_at"`
	LastSeenAt *time.Time   `json:"last_seen_at"`
	RevokedAt  *time.Time   `json:"revoked_at"`
}

func (s *server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request createClientRequest
	if err := decoder.Decode(&request); err != nil {
		s.writeCreateClientDecodeError(w, r, err)
		return
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"request body must contain a single JSON object", nil)
		return
	}

	if err := auth.ValidateClient(request.Name, request.Kind, request.Scopes); err != nil {
		s.writeClientValidationError(w, r, err)
		return
	}

	client, token, err := s.store.CreateClient(r.Context(), request.Name, request.Kind, request.Scopes)
	switch {
	case errors.Is(err, store.ErrClientNameTaken):
		writeError(w, r, http.StatusConflict, "conflict", "client name is already in use", nil)
		return
	case errors.Is(err, store.ErrInvalidClient):
		s.writeClientValidationError(w, r, err)
		return
	case err != nil:
		s.logger.Error("create client",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	principal, _ := principalFrom(r.Context())
	s.logger.Info("client created",
		"client_id", client.ID,
		"kind", string(client.Kind),
		"bootstrap", principal.Bootstrap,
		"request_id", requestIDFrom(r.Context()),
	)

	writeJSON(w, http.StatusCreated, createClientResponse{
		Client: toClientResponse(client),
		Token:  token,
	})
}

func (s *server) writeCreateClientDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large",
			fmt.Sprintf("request body exceeds %d bytes", maxBodyBytes), nil)
		return
	}

	if errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"request body must be a JSON object", nil)
		return
	}

	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body", nil)
		return
	}

	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		field := typeError.Field
		if field == "" {
			field = "body"
		}
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed",
			fmt.Sprintf("field %q has the wrong type", field),
			map[string]any{"field": field})
		return
	}

	if field, ok := unknownJSONField(err); ok {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed",
			fmt.Sprintf("unknown field %q", field),
			map[string]any{"field": field})
		return
	}

	writeError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body", nil)
}

func unknownJSONField(err error) (string, bool) {
	const prefix = "json: unknown field "
	message := err.Error()
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	field := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	field = strings.Trim(field, `"`)
	return field, field != ""
}

func (s *server) writeClientValidationError(w http.ResponseWriter, r *http.Request, err error) {
	var validationError *auth.ValidationError
	if errors.As(err, &validationError) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", validationError.Message,
			map[string]any{"field": validationError.Field})
		return
	}
	writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
}

func toClientResponse(client auth.Client) clientResponse {
	scopes := make([]auth.Scope, len(client.Scopes))
	copy(scopes, client.Scopes)

	var source *string
	if value, ok := client.Kind.Source(); ok {
		source = &value
	}

	return clientResponse{
		ID:         client.ID,
		Name:       client.Name,
		Kind:       client.Kind,
		Scopes:     scopes,
		Source:     source,
		CreatedAt:  client.CreatedAt,
		LastSeenAt: client.LastSeenAt,
		RevokedAt:  client.RevokedAt,
	}
}
