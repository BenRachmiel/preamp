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
  Subsonic API (30 endpoints)
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

Server listens on `:4533`. Point your Subsonic client at `http://localhost:4533` with the dev credentials above.

### Docker

```bash
# Create admin secret for management UI
echo "admin:yourpassword" > admin-secret

# Build and run
docker compose up --build
```

The container image builds `FROM scratch` — just the static binary, nothing else. No shell, no libc, no package manager.

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
| `PREAMP_ENCRYPTION_KEY` | *required* | 32-char hex key for AES-GCM credential encryption |
| `PREAMP_NO_AUTH` | — | Set to `1` to disable auth (dev only) |
| `PREAMP_DEV_USERNAME` | — | Seed a dev credential on startup |
| `PREAMP_DEV_PASSWORD` | — | Plaintext password for dev credential |

### Management UI

The management UI lives at `/manage/` and supports two auth backends (mutually exclusive):

| Variable | Description |
|----------|-------------|
| `PREAMP_ADMIN_SECRET_FILE` | Path to file containing `username:password` |
| `PREAMP_OIDC_ISSUER` | OIDC provider issuer URL |
| `PREAMP_OIDC_CLIENT_ID` | OIDC client ID |
| `PREAMP_OIDC_CLIENT_SECRET` | OIDC client secret |
| `PREAMP_OIDC_REDIRECT_URI` | Callback URL (e.g. `https://music.example.com/manage/callback`) |
| `PREAMP_CREDENTIAL_TTL` | Credential lifetime (default `168h` / 7 days) |

If neither is set, the management UI is disabled and credentials are managed via dev env vars only.

## What's Implemented

### Subsonic API — 30 endpoints

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

Credentials are minted via the management UI with configurable TTL, per-client labels, and instant revocation. API key auth is the default; legacy token/password auth is opt-in per credential.

### Management UI

Go templates + HTMX. Mint, renew, and revoke Subsonic credentials from a browser. Credentials are scoped per user — you can only manage your own.

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

179 tests across 5 packages:

```bash
go test ./... -count=1
```

## What's NOT in Scope

This is a focused Subsonic server, not a kitchen sink. Some things are explicitly out:

### Transcoding

No on-the-fly transcoding. Clients get the original file. If this becomes needed, the plan is to externalize `ffmpeg` as a sidecar container rather than bloating the server binary. The streaming endpoint already supports the `maxBitRate` parameter shape — it just needs the transcoding backend.

### Helm Chart

Coming. The container is ready, compose works, but the Helm chart with proper secret management, ingress, and health checks is next.

### OIDC (in production)

The OIDC auth backend is implemented and tested but hasn't been validated against a real IdP in production yet. File-secret auth works today. For OIDC, the `FROM scratch` image will need CA certificates copied from the builder stage (~200KB).

### Video, Podcasts, Chat, Radio, Jukebox

Not a media center. See the Subsonic endpoints explicitly excluded in the design docs.

### Multi-user

Designed as a single-user server. Multi-user would need per-user library scoping, which changes the data model fundamentally.

## License

GPLv3 — see [LICENSE](LICENSE).
