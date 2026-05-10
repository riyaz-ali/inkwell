// Command inkwell is the HTTP server backing the Inkwell writing assistant.
//
// It wires configuration, a SQLite connection pool, the Anthropic client, and
// a chi-based HTTP router, then serves the /web/ frontend plus the /api/drafts
// endpoints.
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crawshaw.io/sqlite/sqlitex"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/go-chi/chi/v5"
	stock "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/riyaz-ali/inkwell/internal/config"
	"github.com/riyaz-ali/inkwell/internal/drafts"
	"github.com/riyaz-ali/inkwell/internal/schema"
)

const (
	dbPoolSize     = 4
	shutdownGrace  = 10 * time.Second
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Configure the root logger. If stdout is a terminal, pretty-print;
	// otherwise emit structured JSON suitable for log aggregators.
	var logger zerolog.Logger
	{
		var out io.Writer = os.Stdout
		if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
			out = zerolog.ConsoleWriter{Out: os.Stdout}
		}

		logger = zerolog.New(out).Level(cfg.LogLevel).With().Timestamp().Logger()
		ctx = logger.WithContext(ctx)
		log.Logger = logger
	}

	// Open the SQLite pool and apply schema migrations.
	pool, err := sqlitex.Open(cfg.DatabaseURL, 0, dbPoolSize)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database")
	}
	defer func() { _ = pool.Close() }()

	{
		conn := pool.Get(ctx)
		if err = schema.Apply(conn); err != nil {
			log.Fatal().Err(err).Msg("failed to apply schema")
		}
		pool.Put(conn)
	}

	// Construct the Anthropic client. The SDK reads ANTHROPIC_API_KEY from the
	// environment by default; we pass it explicitly so the source of truth is
	// our config struct.
	ai := anthropic.NewClient(option.WithAPIKey(cfg.AnthropicAPIKey))

	// Build the router and mount handlers.
	r := chi.NewRouter()
	r.Use(stock.RequestID, stock.Recoverer, injectLogger(&logger))

	// Streaming uses a two-step protocol so the browser's native EventSource
	// (which is GET-only) can be used: POST stages the prompt and returns a
	// one-shot ticket; GET consumes the ticket and streams Server-Sent Events.
	tickets := drafts.NewTicketStore()
	defer tickets.Close()

	r.Method(http.MethodPost, "/api/drafts", drafts.Create(pool))
	r.Method(http.MethodGet, "/api/drafts/{id}", drafts.Get(pool))
	r.Method(http.MethodPost, "/api/drafts/{id}/revisions", drafts.StageRevise(pool, tickets))
	r.Method(http.MethodGet, "/api/drafts/{id}/revisions/stream", drafts.StreamRevise(pool, &ai, tickets))
	r.Handle("/*", http.FileServer(http.Dir(cfg.WebDir)))

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: r,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	// Start the server and shut down cleanly on signal.
	go func() {
		<-ctx.Done()
		log.Info().Msg("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info().Str("addr", cfg.ListenAddr).Msg("inkwell listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Send()
	}
}

// injectLogger attaches the root logger to every request context so handlers
// can retrieve it via zerolog.Ctx(r.Context()).
func injectLogger(l *zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(l.WithContext(r.Context())))
		})
	}
}
