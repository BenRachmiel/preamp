package scanner

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BenRachmiel/preamp/internal/db"
)

func setupBenchScanner(b *testing.B, musicDir string) (*Scanner, *db.DB) {
	b.Helper()
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.db")

	database, err := db.Open(dbPath)
	if err != nil {
		b.Fatalf("db.Open: %v", err)
	}
	b.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sc := New(database, musicDir, log)
	return sc, database
}

// BenchmarkScan benchmarks scanning test-music-lib/.
// Generate it with: just gen-test-lib
func BenchmarkScan(b *testing.B) {
	musicDir := filepath.Join("..", "..", "test-music-lib")
	if _, err := os.Stat(musicDir); err != nil {
		b.Skip("test-music-lib not found — run 'just gen-test-lib' to generate")
	}

	b.ReportAllocs()
	for b.Loop() {
		sc, _ := setupBenchScanner(b, musicDir)

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		if err := sc.Run(); err != nil {
			b.Fatalf("Run: %v", err)
		}

		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc), "heap-bytes")
		b.ReportMetric(float64(sc.Count()), "tracks")
	}
}
