package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/BenRachmiel/preamp/internal/api"
	"github.com/BenRachmiel/preamp/internal/auth"
	"github.com/BenRachmiel/preamp/internal/config"
	"github.com/BenRachmiel/preamp/internal/db"
	"github.com/BenRachmiel/preamp/internal/manage"
	"github.com/BenRachmiel/preamp/internal/scanner"
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

	sc := scanner.New(database, cfg.MusicDir, log)
	srv.SetScanner(sc)

	// Management UI.
	if cfg.ManageEnabled {
		var authenticator manage.Authenticator
		if cfg.AdminSecretFile != "" {
			authenticator, err = manage.NewSecretAuthenticator(cfg.AdminSecretFile)
			if err != nil {
				log.Error("initializing admin secret auth", "err", err)
				os.Exit(1)
			}
			log.Info("management UI enabled", "mode", "file-secret")
		}
		if cfg.OIDCIssuer != "" {
			ctx := context.Background()
			authenticator, err = manage.NewOIDCAuthenticator(ctx,
				cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURI)
			if err != nil {
				log.Error("OIDC discovery failed — management UI disabled", "err", err)
			} else {
				log.Info("management UI enabled", "mode", "oidc", "issuer", cfg.OIDCIssuer)
			}
		}

		if authenticator != nil {
			mgr := manage.NewServer(database, cfg, authenticator, log)
			defer mgr.Close()
			srv.SetManageHandler(mgr.Handler())
		}
	}

	// Run initial scan in background.
	go func() {
		if err := sc.Run(); err != nil {
			log.Error("initial scan failed", "err", err)
		}
	}()

	// Subsonic API (public).
	subsonicSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.SubsonicHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Admin API (internal only).
	adminSrv := &http.Server{
		Addr:              cfg.AdminListenAddr,
		Handler:           srv.AdminHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "port", "subsonic")
		if err := subsonicSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("subsonic server error", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		log.Info("listening", "addr", cfg.AdminListenAddr, "port", "admin")
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("admin server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := subsonicSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("subsonic shutdown error", "err", err)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("admin shutdown error", "err", err)
	}
}

func seedDevCredential(database *db.DB, cfg *config.Config) error {
	encrypted, err := auth.EncryptPassword(cfg.EncryptionKey, cfg.DevPassword)
	if err != nil {
		return err
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.DevPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing dev password: %w", err)
	}

	conn, put, err := database.WriteConn()
	if err != nil {
		return err
	}
	defer put()

	id := "dev-" + cfg.DevUsername
	return sqlitex.ExecuteTransient(conn,
		`INSERT INTO credential (id, username, hashed_api_key, encrypted_password, client_name, legacy_auth)
		 VALUES (?, ?, ?, ?, 'dev', 1)
		 ON CONFLICT(id) DO UPDATE SET
		   hashed_api_key = excluded.hashed_api_key,
		   encrypted_password = excluded.encrypted_password`,
		&sqlitex.ExecOptions{Args: []any{id, cfg.DevUsername, hashed, encrypted}})
}
