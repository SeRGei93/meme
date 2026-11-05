package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SeRGei93/meme/server/internal/card"
	"github.com/SeRGei93/meme/server/internal/config"
	"github.com/SeRGei93/meme/server/internal/finalizer"
	"github.com/SeRGei93/meme/server/internal/httpapi"
	"github.com/SeRGei93/meme/server/internal/queue"
	"github.com/SeRGei93/meme/server/internal/store"
)

// version is set by the build via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(os.Args[1:]); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}

	switch command {
	case "serve":
		if len(args) > 1 {
			return fmt.Errorf("unexpected arguments: %s", strings.Join(args[1:], " "))
		}
		return serve()
	case "healthcheck":
		if len(args) > 1 {
			return fmt.Errorf("unexpected arguments: %s", strings.Join(args[1:], " "))
		}
		return healthcheck()
	case "client":
		return clientCommand(args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(os.Stdout, "usage: brain [serve|healthcheck|client create]")
		return nil
	default:
		return fmt.Errorf("unknown command %q (want serve, healthcheck or client)", command)
	}
}

func serve() error {
	logger := slog.Default()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Workers outlive the signal context so in-flight ingests can finish while
	// the HTTP server drains; they are stopped explicitly after Shutdown.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var background sync.WaitGroup
	defer func() {
		stopWorkers()
		background.Wait()
		if err := st.Close(); err != nil {
			logger.Error("close store", "error", err)
		}
	}()

	schemaVersion, err := st.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	generator, err := newCardGenerator(cfg, st, logger)
	if err != nil {
		return err
	}

	worker := &queue.Worker{
		Store:     st,
		Processor: generator,
		Logger:    logger,
	}
	finalizer := &finalizer.Finalizer{Store: st, Logger: logger}

	background.Add(2)
	go func() {
		defer background.Done()
		if err := worker.Run(workerCtx); err != nil {
			logger.Error("job worker stopped", "error", err)
		}
	}()
	go func() {
		defer background.Done()
		if err := finalizer.Run(workerCtx); err != nil {
			logger.Error("finalizer stopped", "error", err)
		}
	}()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(httpapi.Options{Store: st, AdminToken: cfg.AdminToken, Version: version, Logger: logger}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	logger.Info("server starting",
		"version", version,
		"addr", cfg.Addr,
		"db", cfg.DBPath,
		"schema_version", schemaVersion,
	)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("server stopping", "reason", ctx.Err().Error())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	stopWorkers()
	background.Wait()
	logger.Info("server stopped")
	return nil
}

func newCardGenerator(cfg config.Config, st *store.Store, logger *slog.Logger) (queue.Processor, error) {
	generator := &card.Generator{
		Store:         st,
		Model:         cfg.LLM.Model,
		MaxInputChars: cfg.LLM.MaxInputChars,
		Logger:        logger,
	}
	if !cfg.LLM.Enabled() {
		logger.Warn("card generation disabled: BRAIN_LLM_BASE_URL and BRAIN_LLM_MODEL are not set")
		return generator, nil
	}

	client, err := card.NewOpenAIClient(card.OpenAIOptions{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("configure card generator: %w", err)
	}
	generator.Client = client
	logger.Info("card generation enabled", "model", cfg.LLM.Model)
	return generator, nil
}

func healthcheck() error {
	url := healthcheckURL(os.Getenv)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("healthcheck: %s returned %d", url, response.StatusCode)
	}
	return nil
}

func healthcheckURL(getenv func(string) string) string {
	addr := strings.TrimSpace(getenv("BRAIN_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", "8080"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health"
}
