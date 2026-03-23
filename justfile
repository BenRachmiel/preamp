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

# Generate test-music-lib with synthetic MP3s (requires ffmpeg)
gen-test-lib count="1500":
    #!/usr/bin/env bash
    set -euo pipefail
    dir="test-music-lib"
    if [ -d "$dir" ]; then
        echo "test-music-lib already exists ($(find "$dir" -name '*.mp3' | wc -l) MP3s). Delete it first to regenerate."
        exit 0
    fi

    # Generate a cover image (~50KB JPEG) and a template MP3 with embedded art.
    cover=$(mktemp --suffix=.jpg)
    template=$(mktemp --suffix=.mp3)
    trap 'rm -f "$template" "$cover"' EXIT
    # Random noise at 1500x1500 makes an incompressible JPEG ≈ 1.5-2MB (realistic embedded art).
    ffmpeg -y -f lavfi -i "nullsrc=s=1500x1500:d=0.04,geq=random(1)*255:128:128" -frames:v 1 -q:v 3 "$cover" 2>/dev/null
    ffmpeg -y -f lavfi -i "sine=frequency=440:duration=3" -i "$cover" \
        -codec:a libmp3lame -q:a 2 \
        -map 0:a -map 1:v -c:v mjpeg -disposition:v attached_pic \
        -metadata "title=Template" -metadata "artist=Test" -metadata "album=Test" \
        "$template" 2>/dev/null

    artists=("ABBA" "Weezer" "Metallica" "Radiohead" "Björk")
    albums=3
    total=0
    per_album=$(( {{count}} / (${#artists[@]} * albums) ))
    [ "$per_album" -lt 1 ] && per_album=1

    for artist in "${artists[@]}"; do
        for a in $(seq 1 $albums); do
            album="Album $a"
            adir="$dir/$artist/$album"
            mkdir -p "$adir"
            printf 'fake-cover-art-data' > "$adir/cover.jpg"

            for t in $(seq 1 $per_album); do
                [ "$total" -ge {{count}} ] && break 3
                cp "$template" "$adir/$(printf '%02d' $t) - Track $t.mp3"
                total=$((total + 1))
            done
        done
    done
    echo "done: $total tracks in $dir/"

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
