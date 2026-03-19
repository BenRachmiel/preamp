# preamp

A minimal, purpose-built Subsonic API server. Single static binary, 6MB container image, 43MB RAM at runtime. Replaces Navidrome for users who want native OIDC authentication and a server that does one thing well.

**14MB binary. No CGO. No ORM. No web framework. Pure Go.**

```
OIDC / Secret File
        |
        v
  Management UI ── Credential Minting (per-client, TTL-bound)
        |
        v
  Subsonic API (34 endpoints)
        |
    +---+---+
    v       v
  SQLite  Filesystem
  (WAL)   (read-only)
```

## Quick Start

```bash
git clone https://github.com/BenRachmiel/preamp.git
cd preamp

# Run locally
PREAMP_MUSIC_DIR=/path/to/music \
PREAMP_DATA_DIR=/tmp/preamp \
PREAMP_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef \
PREAMP_DEV_USERNAME=admin \
PREAMP_DEV_PASSWORD=admin \
go run ./cmd/preamp/
```

Server listens on `:4533` (Subsonic API) and `:4534` (admin API). Point your Subsonic client at `http://localhost:4533` with the dev credentials above.

### Docker

```bash
# Create admin secret for management UI
echo "admin:yourpassword" > admin-secret

# Build and run
docker compose up --build
```

The container image builds `FROM scratch` — just the static binary, nothing else. No shell, no libc, no package manager.

### Helm

A Helm chart is available in `chart/preamp/` with deployment, service, ingress, HTTPRoute, secrets, and PVCs.

| Metric | Value |
|--------|-------|
| Image size (compressed) | **6 MB** |
| Binary size | **14 MB** |
| RAM usage (1500 tracks) | **~43 MB** |
| Startup to serving | **< 1s** |
| Scan 1500 tracks | **< 1s** |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PREAMP_MUSIC_DIR` | *required* | Path to music library |
| `PREAMP_DATA_DIR` | `./data` | Database and cover art cache |
| `PREAMP_LISTEN` | `:4533` | HTTP listen address |
| `PREAMP_ADMIN_LISTEN` | `:4534` | Admin API listen address |
| `PREAMP_ENCRYPTION_KEY` | *required* | 32/64-char hex key for AES-256/128 credential encryption |
| `PREAMP_NO_AUTH` | — | Set to `1` to disable auth (dev only) |
| `PREAMP_DEV_USERNAME` | — | Seed a dev credential on startup |
| `PREAMP_DEV_PASSWORD` | — | Plaintext password for dev credential |

### Management UI

Two options for credential management:

**Go templates + HTMX** — built-in at `/manage/`, supports two auth backends (mutually exclusive):

| Variable | Description |
|----------|-------------|
| `PREAMP_ADMIN_SECRET_FILE` | Path to file containing `username:password` |
| `PREAMP_OIDC_ISSUER` | OIDC provider issuer URL |
| `PREAMP_OIDC_CLIENT_ID` | OIDC client ID |
| `PREAMP_OIDC_CLIENT_SECRET` | OIDC client secret |
| `PREAMP_OIDC_REDIRECT_URI` | Callback URL (e.g. `https://music.example.com/manage/callback`) |
| `PREAMP_CREDENTIAL_TTL` | Credential lifetime (default `168h` / 7 days) |

**Preact SPA** (`preamp-ui`) — separate container, communicates with the admin API on `:4534` via trusted-header auth (`Remote-User`). Use with oauth2-proxy for OIDC. See `docker-compose.yml`.

If neither is configured, credentials are managed via dev env vars only.

## What's Implemented

### Subsonic API — 34 endpoints

All P1 and P2 endpoints for full compatibility with Symfonium, Feishin, and Supersonic.

- **System** — ping, getLicense, getOpenSubsonicExtensions, getUser
- **Browsing** — getMusicFolders, getArtists, getArtist, getAlbum, getSong, getGenres
- **Search** — search3 (FTS5 full-text with unicode support)
- **Streaming** — stream (HTTP Range/206, zero-copy sendfile), download
- **Cover Art** — getCoverArt with lazy resize and disk cache
- **Lists** — getAlbumList2 (10 sort types), getRandomSongs, getStarred2, getSongsByGenre
- **Annotation** — star, unstar, scrobble (batch), setRating
- **Playlists** — full CRUD (create, read, update, delete)
- **Info** — getArtistInfo/2, getAlbumInfo/2, getSimilarSongs/2, getTopSongs
- **Scanning** — startScan, getScanStatus

### Auth

Three Subsonic auth methods supported, checked in order:

1. **API Key** (Open Subsonic) — `apiKey=<key>`, server stores bcrypt hash
2. **Token** — `t=md5(password+salt)&s=salt`, server stores AES-GCM encrypted password
3. **Legacy** — `p=<password>`, plaintext or hex-encoded

Credentials are minted via the management UI with configurable TTL, per-client labels, and instant revocation. API key auth is the default; legacy token/password auth is opt-in per credential. Rate limited: 10 failures per 5 minutes per IP.

### Management UI

Two options: built-in Go templates + HTMX at `/manage/`, or the Preact SPA (`preamp-ui`) as a separate container with oauth2-proxy. Both mint, renew, and revoke Subsonic credentials. Credentials are scoped per user.

### Admin API

JSON API on `:4534` — credential CRUD, library stats, scan control. Trusted-header auth (`Remote-User` / `X-Forwarded-User`). Used by the Preact SPA; also usable directly for automation.

### Scanner

- Reads ID3v1/v2, Vorbis, FLAC, MP4 tags via `dhowden/tag`
- Native MP3 duration parsing (Xing/VBRI VBR + CBR), native FLAC (STREAMINFO)
- Embedded cover art extraction + folder art detection
- FTS5 full-text index with `unicode61` tokenizer and diacritics removal
- Background scan on startup, API available immediately

## Client Compatibility

Tested with (pickiest first):

| Client | Status | Notes |
|--------|--------|-------|
| **Symfonium** (Android) | Working | Requires Content-Length on streams, tests auth aggressively |
| **Feishin** (Electron) | Working | Heavy getAlbumList2 usage, probes Open Subsonic extensions |
| **Supersonic** (Desktop) | Working | Conservative, core Subsonic only |

## Tech Stack

| Concern | Library |
|---------|---------|
| SQLite | `zombiezen.com/go/sqlite` (pure Go, no CGO) |
| Tag reading | `github.com/dhowden/tag` |
| OIDC | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2` |
| Image resize | `github.com/disintegration/imaging` |
| Encryption | `crypto/aes`, `crypto/cipher` (stdlib AES-GCM) |
| HTTP routing | `net/http` (Go 1.22+ method routing) |

No ORM, no web framework, no config library. Streaming uses `http.ServeContent` for native Range request and sendfile support.

## Testing

217 tests across 5 packages:

```bash
go test ./... -count=1
```

## What's NOT in Scope

This is a focused Subsonic server, not a kitchen sink. Some things are explicitly out:

### Transcoding

No on-the-fly transcoding. Clients get the original file. If this becomes needed, the plan is to externalize `ffmpeg` as a sidecar container rather than bloating the server binary. The streaming endpoint already supports the `maxBitRate` parameter shape — it just needs the transcoding backend.

### Video, Podcasts, Chat, Radio, Jukebox

Not a media center. See the Subsonic endpoints explicitly excluded in the design docs.

### Multi-user

Designed as a single-user server. Multi-user would need per-user library scoping, which changes the data model fundamentally.

## License

GPLv3 — see [LICENSE](LICENSE).
