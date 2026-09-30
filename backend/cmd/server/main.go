package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jackc/pgx/v5"

	"github.com/su10/hubtender/backend/internal/auth"
	"github.com/su10/hubtender/backend/internal/config"
	infradb "github.com/su10/hubtender/backend/internal/infrastructure/db"
	"github.com/su10/hubtender/backend/internal/middleware"
	"github.com/su10/hubtender/backend/internal/realtime"
)

// main bootstraps config/logger/Sentry/DB/realtime/auth, then delegates
// dependency wiring to buildDeps (wire.go) and route registration to
// newRouter (routes.go). Healthcheck + key-loading helpers live in support.go.
func main() {
	// -------------------------------------------------------------------------
	// 0. Healthcheck mode — used by Dockerfile HEALTHCHECK because the
	//    distroless runtime image has no shell or wget/curl. Invoking the same
	//    binary keeps the image minimal and the health probe accurate.
	// -------------------------------------------------------------------------
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		runHealthcheck()
		return
	}

	// -------------------------------------------------------------------------
	// 1. Config
	// -------------------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// -------------------------------------------------------------------------
	// 2. Logger
	// -------------------------------------------------------------------------
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	log.Logger = logger

	// -------------------------------------------------------------------------
	// 2a. Sentry — error tracking. Empty DSN → no-op.
	// -------------------------------------------------------------------------
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      cfg.SentryEnvironment,
			Release:          cfg.SentryRelease,
			TracesSampleRate: 0.1,
			EnableTracing:    true,
		}); err != nil {
			log.Fatal().Err(err).Msg("sentry init failed")
		}
		defer sentry.Flush(2 * time.Second)
		log.Info().
			Str("env", cfg.SentryEnvironment).
			Str("release", cfg.SentryRelease).
			Msg("sentry initialised")
	}

	// -------------------------------------------------------------------------
	// 3. Root context — cancelled on shutdown signal so background goroutines
	//    (listener, broker) can exit cleanly.
	// -------------------------------------------------------------------------
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// -------------------------------------------------------------------------
	// 4. Database pool
	// -------------------------------------------------------------------------
	pool, err := infradb.NewPool(rootCtx, cfg.DatabaseURL, infradb.PoolConfig{
		MaxConns:        cfg.DBMaxConns,
		MinConns:        cfg.DBMinConns,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()
	log.Info().Msg("database pool connected")

	// -------------------------------------------------------------------------
	// 5. Dedicated listener connection for pg_notify LISTEN.
	//    A dedicated *pgx.Conn is used instead of the pool because LISTEN holds
	//    the connection for its entire lifetime. Borrowing from the pool would
	//    permanently remove one slot, starving concurrent request handlers.
	// -------------------------------------------------------------------------
	listenerConn, err := pgx.Connect(rootCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open dedicated listener connection")
	}
	// listenerConn is closed after srv.Shutdown in the graceful-shutdown block.
	log.Info().Msg("listener connection opened")

	// -------------------------------------------------------------------------
	// 6. Realtime hub, broker, listener
	// -------------------------------------------------------------------------
	hub := realtime.NewHub(logger)
	broker := realtime.NewBroker(hub, 200*time.Millisecond, logger)
	listener := realtime.NewListener(listenerConn, cfg.DatabaseURL, broker, logger)

	// Run listener in the background; it exits when rootCtx is cancelled.
	go listener.Run(rootCtx)

	// -------------------------------------------------------------------------
	// 7. Auth verification setup. Backend accepts only app-issued JWTs.
	// -------------------------------------------------------------------------
	signingKey, err := loadAppSigningKey(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load app JWT signing key")
	}
	appIssuer, err := auth.NewIssuer(auth.IssuerConfig{
		SigningKey: signingKey,
		Issuer:     cfg.AppJWTIssuer,
		Audience:   cfg.AppJWTAudience,
		AccessTTL:  cfg.AppAccessTokenTTL,
		RefreshTTL: cfg.AppRefreshTokenTTL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to construct app JWT issuer")
	}
	authRepo := auth.NewRepository(pool)
	mailer := auth.NewSMTPMailer(auth.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	authSvc := auth.NewService(authRepo, appIssuer).
		WithMailer(mailer).
		WithAppEnv(cfg.AppEnv).
		WithAppBaseURL(cfg.AppBaseURL)
	authH := auth.NewHandler(authSvc)
	log.Info().
		Bool("mailer_configured", mailer.IsConfigured()).
		Str("app_env", cfg.AppEnv).
		Str("app_base_url", cfg.AppBaseURL).
		Msg("password-recovery flow configured")

	verifyCfg := middleware.VerifyConfig{
		AppPublicKey: &signingKey.Private.PublicKey,
		AppIssuer:    cfg.AppJWTIssuer,
		AppAudience:  cfg.AppJWTAudience,
	}
	mcpIssuer, err := auth.NewIssuer(auth.IssuerConfig{
		SigningKey: signingKey,
		Issuer:     cfg.AppJWTIssuer,
		Audience:   cfg.MCPAudience,
		AccessTTL:  cfg.MCPAccessTokenTTL,
		RefreshTTL: cfg.MCPRefreshTokenTTL,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to construct MCP OAuth JWT issuer")
	}
	mcpVerifyCfg := middleware.VerifyConfig{
		AppPublicKey: &signingKey.Private.PublicKey,
		AppIssuer:    cfg.AppJWTIssuer,
		AppAudience:  cfg.MCPAudience,
	}
	log.Info().Str("kid", signingKey.KID).Str("iss", cfg.AppJWTIssuer).Msg("app JWT issuer ready")

	// -------------------------------------------------------------------------
	// 8. Repositories, cache, services, handlers — see wire.go.
	// -------------------------------------------------------------------------
	d := buildDeps(rootCtx, pool, hub, verifyCfg, cfg, logger, mcpIssuer)

	// -------------------------------------------------------------------------
	// 9. Router — see routes.go.
	// -------------------------------------------------------------------------
	r := newRouter(cfg, d, authH, verifyCfg, mcpVerifyCfg, logger)

	// -------------------------------------------------------------------------
	// 10. HTTP server with graceful shutdown
	// -------------------------------------------------------------------------
	srv := &http.Server{
		Addr:         cfg.BindHost + ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine so shutdown can proceed on the main goroutine.
	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("port", cfg.Port).Msg("server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Этап 2.4 (§2): recovery потерянных enqueue / зависших calculating —
	// один скан сразу после успешного startup, затем периодически.
	go d.recalcRecovery.Run(rootCtx)
	go d.verifRetention.Run(rootCtx)
	if d.telegramBot != nil {
		go d.telegramBot.RunPoller(rootCtx)
		go d.telegramBot.RunSender(rootCtx)
	}

	// Wait for OS signal or server error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	case err := <-serverErr:
		log.Fatal().Err(err).Msg("server error")
	}

	// Step 1. Stop accepting new realtime events and cancel pending debounced
	// publishes so no goroutine tries to push to a closing client.
	broker.Close()

	// Step 2. Close the WebSocket hub: every connected client's send channel
	// is closed, which unblocks the writer goroutines and makes them send a
	// normal close-frame to the browser. Done BEFORE srv.Shutdown because
	// hijacked WebSocket connections are NOT tracked by http.Server and
	// would otherwise leak past the shutdown deadline.
	hub.Close()

	// Step 3. Cancel root context — listener goroutine exits its LISTEN loop.
	rootCancel()

	// Step 3a. Stop the recalc queue: drop pending debounce timers and wait for
	// any in-flight recalc to finish before the DB pool is closed.
	d.recalcQueue.Close()
	d.verifQueue.Close()
	d.aiTriageQueue.Close()

	// Step 4. Graceful HTTP shutdown — wait up to 15 s for in-flight requests.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed; forcing close")
		_ = srv.Close()
	} else {
		log.Info().Msg("server shutdown complete")
	}

	// Step 5. Close the dedicated listener connection after the HTTP server
	// has drained. The listener goroutine has already exited (rootCtx cancelled).
	_ = listenerConn.Close(context.Background())
	log.Info().Msg("listener connection closed")
}
