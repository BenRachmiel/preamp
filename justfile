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

# Pre-push check: vet, test, build, helm lint
push-check:
    #!/usr/bin/env bash
    set -euo pipefail
    failed=0
    run() {
        printf '\n\033[1;34m→ %s\033[0m\n' "$1"
        if eval "$2"; then
            printf '\033[1;32m  ✓ %s\033[0m\n' "$1"
        else
            printf '\033[1;31m  ✗ %s\033[0m\n' "$1"
            failed=1
        fi
    }
    run "go vet"       "go vet ./..."
    run "tests"        "go test ./... -count=1"
    run "build"        "CGO_ENABLED=0 go build -ldflags='-s -w' -o /dev/null ./cmd/preamp/"
    run "helm lint"    "helm lint chart/preamp/"
    echo
    if [ "$failed" -eq 0 ]; then
        printf '\033[1;32mAll checks passed.\033[0m\n'
    else
        printf '\033[1;31mSome checks failed.\033[0m\n'
        exit 1
    fi

# Docker compose up
up:
    docker compose up -d

# Docker compose down
down:
    docker compose down
