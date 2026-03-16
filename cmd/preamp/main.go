package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BenRachmiel/preamp/internal/api"
	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
	"github.com/BenRachmiel/preamp/internal/scanner"

	"zombiezen.com/go/sqlite/sqlitex"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("loading config", "err", err)
		os.Exit(1)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Error("opening database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	if cfg.AuthDisabled {
		log.Warn("PREAMP_NO_AUTH=1 — auth disabled, all requests pass through")
	} else if cfg.EncryptionKey == "" {
		log.Error("PREAMP_ENCRYPTION_KEY is required (or set PREAMP_NO_AUTH=1 to disable auth)")
		os.Exit(1)
	}

	// Seed dev credential if configured.
	if cfg.DevUsername != "" && cfg.DevPassword != "" && cfg.EncryptionKey != "" {
		if err := seedDevCredential(database, cfg); err != nil {
			log.Error("seeding dev credential", "err", err)
			os.Exit(1)
		}
		log.Info("dev credential seeded", "username", cfg.DevUsername)
	}

	srv := api.NewServer(cfg, database, log)

	sc := scanner.New(database, cfg.MusicDir, cfg.CoverArtDir, log)
	srv.SetScanner(sc)

	// Run initial scan in background.
	go func() {
		if err := sc.Run(); err != nil {
			log.Error("initial scan failed", "err", err)
		}
	}()

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
}

func seedDevCredential(database *db.DB, cfg *config.Config) error {
	encrypted, err := auth.EncryptPassword(cfg.EncryptionKey, cfg.DevPassword)
	if err != nil {
		return err
	}

	conn, put, err := database.WriteConn()
	if err != nil {
		return err
	}
	defer put()

	return sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, encrypted_password, client_name)
		 VALUES (?, ?, ?, 'dev')
		 ON CONFLICT(username) DO UPDATE SET encrypted_password = excluded.encrypted_password`,
		&sqlitex.ExecOptions{Args: []any{db.NewID(), cfg.DevUsername, encrypted}})
}
