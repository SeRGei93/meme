package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/store"
)

const (
	maxBodyBytes = 10 << 20 // 10 MiB, from the v1 contract.
	clientsPath  = "/api/v1/clients"
)

type createClientRequest struct {
	Name   string       `json:"name"`
	Kind   auth.Kind    `json:"kind"`
	Source string       `json:"source"`
	Scopes []auth.Scope `json:"scopes"`
}

type createClientResponse struct {
	Client clientResponse `json:"client"`
	Token  string         `json:"token"`
}

type clientListResponse struct {
	Items []clientResponse `json:"items"`
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

func (s *server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListClients(r.Context())
	if err != nil {
		s.logger.Error("list clients",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	items := make([]clientResponse, 0, len(clients))
	for _, client := range clients {
		items = append(items, toClientResponse(client))
	}
	writeJSON(w, http.StatusOK, clientListResponse{Items: items})
}

func (s *server) handleRevokeClient(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimPrefix(r.URL.Path, clientsPath+"/")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"client id must be a positive integer",
			map[string]any{"field": "id"})
		return
	}

	switch err := s.store.RevokeClient(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "client not found", nil)
		return
	case err != nil:
		s.logger.Error("revoke client",
			"error", err,
			"client_id", id,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	s.logger.Info("client revoked",
		"client_id", id,
		"request_id", requestIDFrom(r.Context()),
	)
	w.WriteHeader(http.StatusNoContent)
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
		writeJSONDecodeError(w, r, err)
		return
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"request body must contain a single JSON object", nil)
		return
	}

	client, token, err := s.store.CreateClient(r.Context(), request.Name, request.Kind, request.Source, request.Scopes)
	switch {
	case errors.Is(err, store.ErrClientNameTaken):
		writeError(w, r, http.StatusConflict, "conflict", "client name is already in use",
			map[string]any{"field": "name"})
		return
	case errors.Is(err, store.ErrClientSourceTaken):
		writeError(w, r, http.StatusConflict, "conflict",
			"client source is already in use; pass an explicit source if it was derived from the name",
			map[string]any{"field": "source"})
		return
	case errors.Is(err, store.ErrClientConflict):
		writeError(w, r, http.StatusConflict, "conflict", "client conflicts with an active client", nil)
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

func writeJSONDecodeError(w http.ResponseWriter, r *http.Request, err error) {
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
	if value, ok := client.KnowledgeSource(); ok {
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
