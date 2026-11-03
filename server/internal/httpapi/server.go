// Package httpapi implements the v1 REST API surface.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/store"
)

// Options configures the HTTP API.
type Options struct {
	Store      *store.Store
	AdminToken string
	Version    string
	Logger     *slog.Logger
}

type route struct {
	method     string
	path       string
	oneSegment bool // match exactly one non-empty segment after path
	handler    http.HandlerFunc
}

func (rt route) matches(path string) bool {
	if !rt.oneSegment {
		return path == rt.path
	}
	rest, ok := strings.CutPrefix(path, rt.path)
	return ok && rest != "" && !strings.Contains(rest, "/")
}

type server struct {
	store      *store.Store
	adminToken string
	version    string
	logger     *slog.Logger
	routes     []route
}

// New returns the fully wired HTTP handler.
func New(opts Options) http.Handler {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	s := &server{
		store:      opts.Store,
		adminToken: opts.AdminToken,
		version:    opts.Version,
		logger:     opts.Logger,
	}
	s.routes = []route{
		{method: http.MethodGet, path: "/health", handler: s.handleHealth},
		{method: http.MethodGet, path: clientsPath, handler: s.requireScopes(auth.ScopeAdmin)(s.handleListClients)},
		{method: http.MethodPost, path: clientsPath, handler: s.requireScopes(auth.ScopeAdmin)(s.handleCreateClient)},
		{method: http.MethodDelete, path: clientsPath + "/", oneSegment: true, handler: s.requireScopes(auth.ScopeAdmin)(s.handleRevokeClient)},
	}

	return withRequestID(s.withLogging(s.withRecovery(s)))
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, rt := range s.routes {
		if !rt.matches(r.URL.Path) {
			continue
		}
		if rt.method == r.Method {
			rt.handler(w, r)
			return
		}
		allowed = append(allowed, rt.method)
	}

	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed",
			fmt.Sprintf("method %s is not allowed for %s", r.Method, r.URL.Path),
			map[string]any{"allow": allowed})
		return
	}

	writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
}

type requestIDKey struct{}

type principalKey struct{}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(random)
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func principalFrom(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(auth.Principal)
	return principal, ok
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(body)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (s *server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestIDFrom(r.Context()),
			"response_bytes", recorder.bytes,
		}
		if principal, ok := principalFrom(r.Context()); ok {
			if principal.Bootstrap {
				attrs = append(attrs, "principal", "bootstrap")
			} else if principal.Client != nil {
				attrs = append(attrs, "client_id", principal.Client.ID)
			}
		}
		s.logger.Log(r.Context(), level, "http request", attrs...)
	})
}

func (s *server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered",
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
					"request_id", requestIDFrom(r.Context()),
				)
				if recorder, ok := w.(*statusRecorder); !ok || recorder.status == 0 {
					writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

var errUnauthorized = errors.New("unauthorized")

func (s *server) requireScopes(scopes ...auth.Scope) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if len(r.Header.Values("Authorization")) > 1 {
				s.writeUnauthorized(w, r)
				return
			}

			principal, err := s.authenticate(r.Context(), r.Header.Get("Authorization"))
			if errors.Is(err, errUnauthorized) {
				s.writeUnauthorized(w, r)
				return
			}
			if err != nil {
				s.logger.Error("authenticate request",
					"error", err,
					"request_id", requestIDFrom(r.Context()),
				)
				writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
				return
			}

			if !principal.HasAnyScope(scopes) {
				writeError(w, r, http.StatusForbidden, "forbidden",
					"token does not have a required scope",
					map[string]any{"required_any_of": auth.ScopeStrings(scopes)})
				return
			}

			ctx := context.WithValue(r.Context(), principalKey{}, principal)
			next(w, r.WithContext(ctx))
		}
	}
}

func (s *server) authenticate(ctx context.Context, header string) (auth.Principal, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return auth.Principal{}, errUnauthorized
	}
	token := parts[1]

	if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1 {
		return auth.Principal{Scopes: auth.AllScopes(), Bootstrap: true}, nil
	}

	client, err := s.store.FindClientByTokenHash(ctx, auth.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return auth.Principal{}, errUnauthorized
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("look up bearer token: %w", err)
	}

	return auth.Principal{Client: &client, Scopes: client.Scopes}, nil
}

func (s *server) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", nil)
}
