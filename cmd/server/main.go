// Command server runs the TPSA Reviewer application: HTTP delivery, the
// background AI review worker, and the database migrations they depend on.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"third-party-review/internal/config"
	delivery "third-party-review/internal/delivery/http"
	"third-party-review/internal/delivery/http/handler"
	"third-party-review/internal/helper"
	"third-party-review/internal/repository"
	"third-party-review/internal/service/aiclient"
	"third-party-review/internal/service/assessment"
	"third-party-review/internal/service/auth"
	"third-party-review/internal/service/dashboard"
	"third-party-review/internal/service/parser"
	"third-party-review/internal/service/review"
	"third-party-review/internal/service/settings"
	"third-party-review/migrations"
	"third-party-review/web"
)

func main() {
	// The runtime image is distroless: no shell, no curl. The compose health
	// check therefore re-invokes this same binary with -healthcheck, which
	// probes the running server over loopback and exits non-zero if it is
	// unwell.
	healthcheck := flag.Bool("healthcheck", false, "probe the running server's /healthz endpoint and exit")
	flag.Parse()
	if *healthcheck {
		if err := probeHealth(); err != nil {
			fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		// Startup failures go to stderr as plain text: a JSON log line is
		// harder to read when the container is crash-looping.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Read a .env before anything looks at the environment, so `go run` works
	// with nothing exported by hand. Exported variables still win, so this is
	// inert in a container where the orchestrator supplies the environment.
	if err := config.LoadDotEnv(); err != nil {
		return fmt.Errorf("read .env: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.App)
	log.Info("starting TPSA Reviewer", "env", cfg.App.Env, "addr", cfg.App.Addr)

	// Signals cancel this context, which unwinds the worker and the server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := helper.Connect(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("connected to postgres")

	if cfg.App.RunMigrations {
		if err := helper.Migrate(db.Pool(), migrations.FS, ".", log); err != nil {
			return err
		}
	} else {
		log.Warn("RUN_MIGRATIONS is false; the schema is assumed to be current")
	}

	repos := repository.NewRepositories(db)

	reviewer, err := aiclient.New(cfg.AI, log)
	if err != nil {
		return err
	}

	// Each service is constructed from an explicit Deps naming only the
	// contracts it uses, and each constructor fails here - at startup, saying
	// which dependency is missing - rather than panicking on a request later.
	assessmentSvc, err := assessment.New(
		assessment.FromRepositories(repos, parser.New(), log))
	if err != nil {
		return err
	}
	reviewSvc, err := review.New(
		review.FromRepositories(repos, reviewer, cfg.AI, log))
	if err != nil {
		return err
	}
	authSvc, err := auth.New(
		auth.FromRepositories(repos, cfg.App.SessionTTL, log))
	if err != nil {
		return err
	}
	dashboardSvc, err := dashboard.New(
		dashboard.FromRepositories(repos, log))
	if err != nil {
		return err
	}
	settingsSvc, err := settings.New(
		settings.FromRepositories(repos, log))
	if err != nil {
		return err
	}
	// Install the persisted risk matrix before anything renders, so the very
	// first page served reflects the setting on record rather than the
	// compiled-in default.
	if err := settingsSvc.LoadRiskMatrix(ctx); err != nil {
		return fmt.Errorf("load risk matrix: %w", err)
	}

	// Create the first account from configuration when the database is empty.
	// A no-op once any user exists, so a restart can never reset an account.
	if err := authSvc.Bootstrap(ctx, cfg.App.BootstrapUser, cfg.App.BootstrapPassword); err != nil {
		return err
	}

	renderer, err := handler.NewRenderer(web.FS)
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("static assets: %w", err)
	}

	// The worker runs in-process. With one internal team's volume there is no
	// case for a separate queue service, and the job table plus SKIP LOCKED
	// already makes a second app instance safe if one is ever added.
	worker := review.NewWorker(reviewSvc, repos.Jobs, cfg.AI.MaxConcurrency, log)
	worker.Start(ctx)

	// Expired sessions are swept periodically so the table stays bounded.
	go sweepSessions(ctx, authSvc, log)

	handlers, err := handler.New(handler.Deps{
		Assessments: assessmentSvc,
		Reviews:     reviewSvc,
		Dashboards:  dashboardSvc,
		Auth:        authSvc,
		Settings:    settingsSvc,
		Templates:   renderer,
		SessionTTL:  cfg.App.SessionTTL,
		Log:         log,
	})
	if err != nil {
		return err
	}

	router := delivery.NewRouter(delivery.Deps{
		Handler:       handlers,
		Auth:          authSvc,
		SessionSecret: cfg.App.SessionSecret,
		Static:        staticFS,
		Health: func() error {
			pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return db.Ping(pingCtx)
		},
		Log: log,
	})

	srv := &http.Server{
		Addr:    cfg.App.Addr,
		Handler: router,
		// A generous write timeout: the mapping preview of a large workbook is
		// the slowest response the app serves.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.App.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
	}

	// Give in-flight reviews a moment to notice the cancelled context and
	// record their state, so nothing is left marked running.
	done := make(chan struct{})
	go func() { worker.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		log.Warn("review worker did not stop in time; jobs still marked running will be re-queued on next start")
	}

	log.Info("stopped")
	return nil
}

// sweepSessions deletes expired sessions on a slow tick.
func sweepSessions(ctx context.Context, a *auth.Service, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := a.PurgeExpiredSessions(ctx); err != nil {
				log.Warn("could not purge expired sessions", "error", err)
			} else if n > 0 {
				log.Debug("purged expired sessions", "count", n)
			}
		}
	}
}

// probeHealth is the self-check used by the container health check.
func probeHealth() error {
	addr := os.Getenv("APP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz returned %d", resp.StatusCode)
	}
	return nil
}

// newLogger builds the structured logger. Production gets JSON for ingestion;
// development gets text, which is far easier to read in a compose log.
func newLogger(cfg config.App) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if cfg.Env == "production" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
