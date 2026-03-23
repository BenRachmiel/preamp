package api

import (
	"log/slog"
	"os"
	"testing"

	"github.com/BenRachmiel/preamp/internal/scanner"
)

func TestGetScanStatusNoScanner(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getScanStatus?")

	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != false {
		t.Errorf("scanning = %v, want false", ss["scanning"])
	}
}

func TestStartScanNoScanner(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/startScan?")

	if resp["status"] != "failed" {
		t.Errorf("expected failed status when scanner not configured")
	}
}

func TestStartScanHappyPath(t *testing.T) {
	srv := testServer(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := scanner.New(srv.db, srv.cfg.MusicDir, log)
	srv.SetScanner(sc)

	resp := getJSON(t, srv, "/rest/startScan?")

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != true {
		t.Errorf("scanning = %v, want true", ss["scanning"])
	}
}

func TestGetScanStatusWithScanner(t *testing.T) {
	srv := testServer(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := scanner.New(srv.db, srv.cfg.MusicDir, log)
	srv.SetScanner(sc)

	resp := getJSON(t, srv, "/rest/getScanStatus?")

	ss := resp["scanStatus"].(map[string]any)
	if ss["scanning"] != false {
		t.Errorf("scanning = %v, want false (no scan running)", ss["scanning"])
	}
}
