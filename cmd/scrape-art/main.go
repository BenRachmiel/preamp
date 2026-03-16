package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BenRachmiel/preamp/internal/art"
	"github.com/BenRachmiel/preamp/internal/db"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Search MusicBrainz but don't download art")
	saveToFolder := flag.Bool("save-to-folder", false, "Also save cover.jpg into album directories (env: PREAMP_ART_SAVE_TO_FOLDER=1)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	dataDir := os.Getenv("PREAMP_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}

	dbPath := filepath.Join(dataDir, "preamp.db")
	coverArtDir := filepath.Join(dataDir, "covers")

	database, err := db.Open(dbPath)
	if err != nil {
		log.Error("opening database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	// Env var overrides flag.
	toFolder := *saveToFolder || os.Getenv("PREAMP_ART_SAVE_TO_FOLDER") == "1"

	scraper := art.NewScraper(database, coverArtDir, log, art.Options{
		DryRun:       *dryRun,
		SaveToFolder: toFolder,
	})
	if err := scraper.Run(); err != nil {
		log.Error("scraping failed", "err", err)
		os.Exit(1)
	}
}
