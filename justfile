# preamp-server task runner

# List available recipes
default:
    @just --list

# Run dev server with test-music-lib, no auth
dev:
    PREAMP_MUSIC_DIR=./test-music-lib PREAMP_DATA_DIR=/tmp/preamp PREAMP_NO_AUTH=1 go run ./cmd/preamp/

# Build all packages
build:
    go build ./...

# Run all tests
test:
    go test ./... -count=1

# Test a specific package (e.g., just test-pkg scanner)
test-pkg pkg:
    go test ./internal/{{pkg}}/... -count=1

# Run scanner benchmarks
bench:
    go test ./internal/scanner/... -bench=. -benchmem -count=1

# Run tests with race detector
test-race:
    go test ./... -race -count=1

# Remove /tmp/preamp data dir
clean:
    rm -rf /tmp/preamp

# Docker compose up
up:
    docker compose up -d

# Docker compose down
down:
    docker compose down
