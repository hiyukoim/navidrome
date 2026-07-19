# Last.fm artist alias proxy

A small HTTP proxy for [Navidrome](https://www.navidrome.org/) Last.fm scrobbling. Point Navidrome's `LastFM.BaseURL` at this service; for `track.updateNowPlaying` and `track.scrobble` it can rewrite artist fields, strip private `nd_*` helpers, and re-sign before forwarding to Last.fm.

All other Last.fm methods are forwarded unchanged. Rules reload automatically when the rules file changes (invalid files keep the previous rules).

## Quick start

```bash
export LASTFM_API_SECRET="your-lastfm-api-secret"
export RULES_FILE="./rules.example.yaml"
go run .
```

Or with Docker:

```bash
docker build -t lastfm-alias-proxy .
docker run --rm -p 8080:8080 \
  -e LASTFM_API_SECRET="your-lastfm-api-secret" \
  -v "$(pwd)/rules.example.yaml:/rules/rules.yaml:ro" \
  -e RULES_FILE=/rules/rules.yaml \
  lastfm-alias-proxy
```

Configure Navidrome:

```toml
[LastFM]
BaseURL = "http://localhost:8080/"
```

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LASTFM_API_SECRET` | For rewriting | — | Last.fm API secret used to re-sign after rewrite. If unset, requests pass through without rewriting. |
| `RULES_FILE` | No | — | Path to YAML or JSON rules. Watched for changes; bad files keep the previous config. |
| `ARTIST_ALIASES` | No | — | Legacy JSON object of artist aliases (merged with `RULES_FILE`). |
| `ARTIST_ALIASES_FILE` | No | — | Legacy JSON file of artist aliases. |
| `LISTEN_ADDR` | No | `:8080` | Address the HTTP server listens on. |

## Rules file (YAML)

See [`rules.example.yaml`](rules.example.yaml):

```yaml
artist_aliases:
  Example Artist: Example Alias

use_sort_artist:
  - Example Sort Display Artist

album_rules:
  - id: example-ost
    album: Example Soundtrack
    album_artist: Example Cast
```

Priority on scrobble / now playing:

1. `artist_aliases` — exact-match rewrite of `artist` / `albumArtist`.
2. `use_sort_artist` — if the (possibly still original) display name is listed and Navidrome sent `nd_sortArtist` / `nd_sortAlbumArtist`, use the sort value. Manual aliases therefore win over sort.
3. `album_rules` — set `albumArtist` only when `album` matches.
4. Strip all `nd_*` params, then re-sign.

Keep personal rule maps out of git (Coolify persistent volume / untracked `rules.yaml`).

## Signing

Re-signing follows the same rules as Navidrome's Last.fm client:

1. Collect all parameter keys except `format` and `callback`.
2. Sort keys alphabetically.
3. Concatenate each key with its first value (empty strings are included).
4. Append the API secret.
5. Set `api_sig` to the lowercase hex MD5 digest.

## Endpoints

The proxy accepts requests on `/` and `/2.0/` and forwards them to `https://ws.audioscrobbler.com/2.0/`. Health check: `/healthz`.

## Development

```bash
go test ./...
go run .
```

### Cross-compile for linux/amd64 (e.g. from Apple Silicon)

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o lastfm-alias-proxy .
```

QEMU/`docker buildx --platform linux/amd64` Go builds can segfault; prefer native Go cross-compile then a runtime-only image that `COPY`s the binary.

## Navidrome cutover

1. Run Navidrome built with configurable `LastFM.BaseURL` (and optional `nd_sortArtist` helpers).
2. Run this proxy on the same Docker network with `RULES_FILE` on persistent storage.
3. Set:

```toml
[LastFM]
BaseURL = "http://lastfm-alias-proxy:8080/"
```

Keep `AuthURL` on the official Last.fm auth page. Do not point browser OAuth at the proxy.
