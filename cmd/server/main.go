// Command server is the entry point: it opens the database, wires the three
// layers together, and starts the Gin HTTP server.
//
// Everything here is plumbing. There is no business logic in this file, and
// there should never be.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver with database/sql
	"github.com/joho/godotenv"

	"github.com/sandip/docker-gin-go-user-api/internal/handler"
	"github.com/sandip/docker-gin-go-user-api/internal/repository"
	"github.com/sandip/docker-gin-go-user-api/internal/service"
	"github.com/sandip/docker-gin-go-user-api/internal/worker"
)

const (
	defaultPort = "8093"
	defaultDSN  = "root:@tcp(127.0.0.1:3307)/go_user_db?parseTime=true"
)

func main() {
	// ---- 1. Configuration -------------------------------------------------
	// Load .env if present (local development). In production there is no
	// .env file — real environment variables are set by the platform — so a
	// missing file is fine, not an error. Values already set in the actual
	// environment win over the file.
	loadedEnvFile := godotenv.Load() == nil

	// Environment variables with sane defaults: no code change needed to point
	// at a different database or port.
	dsn := envOr("DB_DSN", defaultDSN)
	port := envOr("PORT", defaultPort)
	mode := envOr("GIN_MODE", gin.DebugMode)

	// Gin already read GIN_MODE in its package init(), which runs before main()
	// — and therefore before godotenv.Load() above. A value from the .env file
	// would be silently ignored, so set the mode explicitly now that the
	// environment is fully populated.
	gin.SetMode(mode)

	// Structured logging: every line carries key=value attributes a log
	// aggregator can filter on, instead of prose it would have to parse.
	// SetDefault makes slog.Info/Error work everywhere without passing the
	// logger around — and reroutes the old log package into slog too.
	slog.SetDefault(newLogger(mode))

	if loadedEnvFile {
		slog.Info("loaded configuration from .env")
	}

	// ---- 2. Database ------------------------------------------------------
	db, err := openDB(dsn)
	if err != nil {
		// slog has no Fatal on purpose: log the error, then exit explicitly.
		slog.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("connected to MySQL")

	// ---- 3. Dependency injection -----------------------------------------
	// This is the whole "DI container": three constructor calls you can read.
	// Spring does this with annotations and reflection; Go does it with code.
	// The background worker: a buffered channel (the queue) plus one consumer
	// goroutine draining it. Handlers only enqueue jobs; the actual work runs
	// here, after the HTTP response has already been sent.
	jobs := worker.New(100) // room for a burst of 100 queued jobs
	jobs.Start()            // the consumer goroutine clocks in

	repo := repository.NewMySQLUserRepo(db) // record keeper gets the filing cabinet
	svc := service.NewUserService(repo)     // manager gets the record keeper
	h := handler.NewUserHandler(svc, jobs)  // receptionist gets the manager + the kitchen

	// ---- 4. Gin engine ----------------------------------------------------
	// gin.Default() = a bare engine + two middlewares:
	//   Logger()   -> one log line per request
	//   Recovery() -> turns a panic into a 500 instead of killing the server
	router := gin.Default()
	h.RegisterRoutes(router)

	// ---- 5. HTTP server with timeouts ------------------------------------
	// gin's router.Run() is fine for a demo, but it uses http.Server defaults,
	// which means no timeouts at all -- a slow client can hold a connection
	// open forever. Building the server by hand lets us set them.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ---- 6. Start, then wait for Ctrl+C ----------------------------------
	// signal.NotifyContext gives a context that cancels on Ctrl+C or SIGTERM.
	// This is context.Context from the earlier notes, doing production work.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("server listening", "url", "http://localhost:"+port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done() // blocks here until the signal arrives
	slog.Info("shutting down")

	// ---- 7. Graceful shutdown --------------------------------------------
	// Stop accepting new requests, give in-flight ones up to 10 seconds to
	// finish, then exit. Without this, Ctrl+C cuts active requests mid-write.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "err", err)
	}

	// The HTTP server is stopped, so nothing can enqueue anymore. Now close
	// the queue and let the worker finish whatever jobs are still on it —
	// order matters: producers must be gone before the channel closes.
	if err := jobs.Shutdown(shutdownCtx); err != nil {
		slog.Warn("worker shut down with jobs still pending", "err", err)
	}

	slog.Info("stopped cleanly")
}

// newLogger picks the slog output format from the Gin mode: human-friendly
// key=value text while developing, one JSON object per line in release mode —
// the shape log collectors (CloudWatch, Loki, ELK) expect to ingest.
func newLogger(mode string) *slog.Logger {
	if mode == gin.ReleaseMode {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// openDB opens the pool and verifies the connection actually works.
func openDB(dsn string) (*sql.DB, error) {
	// sql.Open does NOT connect -- it only validates the DSN and prepares a
	// lazy pool. That is why the Ping below matters.
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// Connection pool settings. Without these, Go will happily open unlimited
	// connections under load and MySQL will start refusing them.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Fail fast: if the database is unreachable, find out at startup rather
	// than on the first user request.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// envOr reads an environment variable, falling back to a default.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
