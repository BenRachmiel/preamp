# preamp

A minimal Subsonic API server that replaces Navidrome. Single static binary, scratch container, native OIDC authentication. Does one thing well.

```
                         14MB binary | 6MB image | 43MB RAM | <1s startup
```

## Why

Navidrome is great, but:

- No OIDC/OAuth2 support -- Subsonic's MD5 token auth makes native SSO impossible without a bridge
- The built-in web UI and auth system go unused when you only care about the API
- A purpose-built server can be leaner and tailored to the exact feature set needed

preamp solves this with a credential minting model: authenticate via OIDC, mint short-lived Subsonic credentials, hand them to your client. Real SSO without breaking any Subsonic client.

```
OIDC Provider (Keycloak / Dex / Authelia)
        |
        v
  Management UI --- mint/renew/revoke credentials (per-client, TTL-bound)
        |
        v
  Subsonic API (33 endpoints)
        |
    +---+---+
    v       v
  SQLite  Music Dir
  (WAL)   (read-only)
```

## Quick Start

```bash
# Run locally with test data
just dev

# Or manually
PREAMP_MUSIC_DIR=/path/to/music \
PREAMP_DATA_DIR=/tmp/preamp \
PREAMP_NO_AUTH=1 \
go run ./cmd/preamp/
```

Subsonic API on `:4533`, admin API on `:4534`. Point your client at `http://localhost:4533`.

### Docker

```bash
echo "admin:yourpassword" > admin-secret
docker compose up --build
```

### Helm

```bash
helm install preamp chart/preamp/ -f values-pedals.yaml -n pedals
```

Chart includes deployment, service, ingress, HTTPRoute (Gateway API), secrets, PVCs, and optional oauth2-proxy sidecar.

## Metrics

| Metric | Value |
|--------|-------|
| Image size (compressed) | 6 MB |
| Binary size | 14 MB |
| RAM usage (1500 tracks) | ~43 MB |
| Startup to serving | < 1s |
| Scan 1500 tracks | < 1s |
| Dependencies | 6 (all pure Go) |
| Lines of Go | ~4500 |

## Client Compatibility

Tested with (pickiest first):

| Client | Platform | Status | Notes |
|--------|----------|--------|-------|
| Symfonium | Android | Working | Fast sync via search3, Content-Length on streams |
| Feishin | Electron | Working | Heavy getAlbumList2 usage, Open Subsonic probing |
| Supersonic | Desktop | Working | Conservative, core Subsonic only |

## Subsonic API

33 unique endpoints (36 registrations including v1 compatibility aliases).

| Category | Endpoints |
|----------|-----------|
| System | ping, getLicense, getOpenSubsonicExtensions, getUser, getMusicFolders |
| Browsing | getArtists, getArtist, getAlbum, getSong, getGenres |
| Search | search3 -- FTS5 full-text, unicode61 tokenizer, diacritics removal, empty-query bulk fetch with offset pagination |
| Media | stream (Range/206, zero-copy sendfile), download, getCoverArt (lazy resize + disk cache) |
| Lists | getAlbumList2 (9 sort types), getRandomSongs, getStarred2, getSongsByGenre |
| Annotation | star, unstar, scrobble (batch), setRating |
| Playlists | full CRUD |
| Info | getArtistInfo/2, getAlbumInfo/2, getSimilarSongs/2, getTopSongs |
| Scanning | startScan, getScanStatus |

## Auth

Three Subsonic auth methods, checked in order:

1. **API Key** (Open Subsonic) -- `apiKey=<key>`, bcrypt-hashed server-side
2. **Token** -- `t=md5(password+salt)&s=salt`, AES-GCM encrypted password
3. **Legacy** -- `p=<password>`, plaintext or hex-encoded

Credentials are minted via the management UI with configurable TTL (default 7 days), per-client labels, and instant revocation. Rate limited at 10 failures per 5 minutes per IP.

## Management UI

Two options for credential lifecycle management:

| Mode | Stack | Auth |
|------|-------|------|
| Built-in | Go templates + HTMX at `/manage/` | OIDC or file-secret |
| SPA | Preact container + oauth2-proxy sidecar | OIDC via oauth2-proxy |

Both mint, renew, and revoke credentials scoped per authenticated user.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PREAMP_MUSIC_DIR` | *required* | Path to music library |
| `PREAMP_DATA_DIR` | `./data` | Database and cover art cache |
| `PREAMP_LISTEN` | `:4533` | Subsonic API listen address |
| `PREAMP_ADMIN_LISTEN` | `:4534` | Admin API listen address |
| `PREAMP_ENCRYPTION_KEY` | *required* | 32/64-char hex key for credential encryption |
| `PREAMP_NO_AUTH` | -- | Disable auth (dev only) |
| `PREAMP_DEV_USERNAME` | -- | Seed a dev credential on startup |
| `PREAMP_DEV_PASSWORD` | -- | Password for dev credential |
| `PREAMP_CREDENTIAL_TTL` | `168h` | Credential lifetime |
| `PREAMP_ADMIN_SECRET_FILE` | -- | File-secret auth (`username:password`) |
| `PREAMP_OIDC_ISSUER` | -- | OIDC provider URL |
| `PREAMP_OIDC_CLIENT_ID` | -- | OIDC client ID |
| `PREAMP_OIDC_CLIENT_SECRET` | -- | OIDC client secret |
| `PREAMP_OIDC_REDIRECT_URI` | -- | OIDC callback URL |

## Development

```bash
just              # list all recipes
just dev          # run with test data, no auth
just test         # run all tests
just test-pkg api # test a single package
just test-race    # tests with race detector
just bench        # scanner benchmarks
just push-check   # vet + test + build + helm lint
just up / down    # docker compose
just clean        # remove /tmp/preamp
```

### Testing

218 tests across 5 packages:

| Package | Tests | Covers |
|---------|-------|--------|
| `internal/api` | 151 | All endpoints, auth, admin API |
| `internal/manage` | 23 | Sessions, OIDC, secret auth, handlers |
| `internal/scanner` | 11 | Scan, duration parsing, error recovery |
| `internal/config` | 9 | Env parsing, validation |
| `internal/db` | 9 | Schema, connection pools, constraints |

## Tech Stack

| Concern | Choice |
|---------|--------|
| Language | Go (single static binary, `CGO_ENABLED=0`) |
| Database | SQLite with WAL mode, FTS5 for search |
| SQLite driver | `zombiezen.com/go/sqlite` (pure Go) |
| Tag reading | `github.com/dhowden/tag` |
| OIDC | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2` |
| Image resize | `github.com/disintegration/imaging` |
| HTTP routing | `net/http` stdlib (Go 1.22+) |
| Encryption | `crypto/aes` stdlib (AES-GCM) |

No ORM. No web framework. No config library. Streaming uses `http.ServeContent` for native Range request and sendfile(2) support.

## Not in Scope

- **Transcoding** -- clients get original files. If needed, ffmpeg as a sidecar, not in the binary.
- **Video, podcasts, chat, radio, jukebox** -- not a media center.
- **Multi-user library scoping** -- single-user server by design.

## License

GPLv3 -- see [LICENSE](LICENSE).
