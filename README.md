# listen-party

`listen-party` is a small LAN music server for shared rooms, offices, and
LAN-party setups. It indexes local MP3 files, serves one embedded browser UI,
and keeps all connected browsers on one shared playback state.

The project is intentionally plain:

- One Go binary.
- Embedded HTML/CSS/JS.
- SQLite metadata/search index.
- PocketBase-backed local auth.
- No runtime internet services.

## Current State

Working:

- Recursive MP3 indexing from configured folders.
- Incremental rescans skip unchanged files by path and modification time.
- Scans prune ignored subdirectories such as dot-directories, double-underscore
  directories, dependency/build folders, and common system recycle/cache folders.
- Configurable parallel scan workers for metadata parsing.
- Automatic first-run config creation.
- SQLite-backed metadata and search.
- Embedded listener UI and admin config UI.
- Embedded PocketBase auth/admin dashboard mounted at `/authAdmin`.
- Dedicated listener/admin app login mounted at `/login`.
- Configurable rooms with one isolated shared playback state per room.
- Public room access for every authenticated app user, with private room access
  driven by PocketBase user room IDs, groups, and admin role.
- Shared current track, queue, queue order, play/pause, seek, skip, previous,
  and track-end advance.
- Queue add, remove, clear, move up/down, and move-to-next.
- Recently played history with clear support.
- Play immediately from search or history.
- Dynamic search with 300 ms debounce and server-side result limits.
- Search results sorted by title ascending.
- Server-sent events for shared state updates plus a periodic heartbeat.
- Connected listener count.
- Browser-local volume and mute.
- Structured backend logs for startup, scans, config changes, listener
  connections, playback commands, and important failures.

Known limitations:

- Browser autoplay policy still applies. A browser that has not been interacted
  with may refuse to make sound until the user clicks the page.
- Playback sync is practical, not sample-accurate. Clients correct drift against
  server time when they are more than 0.1 seconds away from the expected media
  position.

## Synchronization Model

The server is the source of truth for playback in each room:

- Current track.
- Queue and queue order.
- Recently played history.
- Playing or paused.
- Shared seek position.
- Track start time.
- Connected listener count.

Clients subscribe to `/rooms/{room}/events` with SSE. Every state update carries
a revision and server timestamp. The browser keeps its local `<audio>` element
aligned to that state and periodically re-checks drift. Client media events are
local except for `ended`, which asks the server to advance the shared playback
state. The server also checks indexed track duration during room state reads and
SSE heartbeats so playback advances even if a browser misses the `ended` event.

Removing a room from config closes existing SSE subscriptions for that room.

Volume and mute are intentionally not shared. They are tab-local session
preferences.

## UI Shape

The listener UI is a full-screen app:

- Left rail: library search.
- Main area: previously played on the left, upcoming queue on the right.
- Bottom player: previous, play/pause, next, seek, and local volume.

The queue drains from the top. The top queue item is always the next track.
Previously played is newest-first. Pressing previous pops the newest history
item into current playback and returns the current track to the front of the
upcoming queue.

## Config

Runtime config is JSON. By default it lives at:

```text
${UserConfigDir}/listen-party/config.json
```

Default database path:

```text
${UserConfigDir}/listen-party/listen-party.sqlite
```

Default config:

```json
{
  "addr": "0.0.0.0:8080",
  "music_dirs": ["${UserConfigDir}/listen-party/music"],
  "database_path": "${UserConfigDir}/listen-party/listen-party.sqlite",
  "scan_workers": 16,
  "rooms": [
    {
      "id": "public",
      "name": "Public Room",
      "public": true
    }
  ],
  "auth": {
    "pocketbase": {
      "data_dir": "${UserConfigDir}/listen-party/auth",
      "bootstrap_admin_email": "admin@listen-party.local",
      "keycloak": {
        "enabled": false,
        "issuer_url": "",
        "client_id": "",
        "client_secret": "",
        "display_name": "Keycloak"
      }
    }
  }
}
```

If the config file does not exist, the server creates it. Any configured
`music_dirs` that do not exist are created at startup.

Use a custom config path:

```sh
./build/lp -config ./config.json
```

The admin page can edit the config:

```text
http://localhost:8080/admin
```

The auth/admin dashboard is available at:

```text
http://localhost:8080/authAdmin
```

Regular music app users should not use `/authAdmin`. Unauthenticated requests to
the listener UI redirect to:

```text
http://localhost:8080/login
```

On first run the server bootstraps a PocketBase superuser for auth
administration:

```text
admin@listen-party.local / admin
```

The bootstrap superuser is only for `/authAdmin`; it is not accepted for app
routes. Regular app users are created in `/authAdmin` with a username,
password, `enabled=true`, and optional `app_role=admin`, then sign in through
`/login`.

To enable a local Keycloak bridge, create a Keycloak realm and confidential
client, then set:

```json
"keycloak": {
  "enabled": true,
  "issuer_url": "http://127.0.0.1:10000/realms/listen-party",
  "client_id": "listen-party",
  "client_secret": "copy-from-keycloak-client-credentials",
  "display_name": "Keycloak"
}
```

In Keycloak, create users with a username; that username becomes the
listen-party username on first Keycloak login. Set a password under the user's
Credentials tab and turn off "Temporary" if you do not want Keycloak to force a
password change on first login.

To sync Keycloak groups into listen-party room access, configure the Keycloak
client to include a `groups` claim in the OIDC userinfo response. On each
Keycloak login, listen-party copies that claim into the PocketBase
`users.groups` field. Missing `groups` claims leave existing PocketBase groups
unchanged.

Changing `addr`, `database_path`, or auth provider settings requires a restart.
Updating music directories or scan worker count applies immediately; use the
admin rescan button to refresh the library after changing music folders.
Updating rooms also applies immediately for new room enumeration and API
requests. `scan_workers` must be between 1 and 256.

Room IDs must be lowercase URL-safe text. A public room is visible to every
authenticated app user. Admin users can access every room. For private room
access, edit users in `/authAdmin` and set `room_ids` and/or `groups` as
comma-separated values. Room `allowed_groups` are configured in `config.json`.
Room config updates preserve playback state for unchanged room IDs and close
listeners for removed room IDs.

Example private room:

```json
{
  "id": "staff",
  "name": "Staff Room",
  "public": false,
  "allowed_groups": ["staff"]
}
```

## Build And Run

Run from the repo root:

```sh
go run .
```

Build for the current platform:

```sh
go build -o build/lp .
```

Build using the Makefile:

```sh
make compile
```

Create a portable LAN package with both built executables and the current
config directory:

```sh
make package
```

Packages are written to `publish/listen-party-YYYYMMDD-HHMMSS.tar.gz`. The
archive contains `bin/lp`, `bin/lp.exe`, and `config/listen-party/`. The config
copy includes PocketBase auth data, room config, and the SQLite library DB, so
treat the archive as sensitive.

Run:

```sh
./build/lp
```

Open:

```text
http://localhost:8080
```

Default auth admin login is `admin@listen-party.local / admin`; use it only at
`/authAdmin`, then change the password. Create separate username/password app
users in the `users` collection for the music UI.

## Deployment

For a simple LAN deployment:

1. Build the binary on the target machine or cross-compile for it.
2. Create a config file with the listen address, music folders, database path,
   and credentials.
3. Run the binary with `-config /path/to/config.json`.
4. Create app users in `/authAdmin`.
5. Put MP3 files under one of the configured `music_dirs`.
6. Open the listener UI from LAN clients.

The binary serves its own static UI and media endpoints. No separate web server
is required for basic LAN use. If putting it behind a reverse proxy, preserve
SSE streaming for `/rooms/{room}/events`.

Logs are written to stdout/stderr through Go `slog`. They are intentionally
event-focused: startup, scan summaries, admin config changes, listener
connect/disconnect, playback and queue commands, and warnings for failed scans,
missing tracks, media file errors, and SSE write failures.

## Development

Useful checks:

```sh
go test ./...
go build -o build/lp .
```

If port `8080` is stuck during local development:

```sh
fuser -k 8080/tcp
```

The main files are:

- `main.go`: startup, config loading, scan, HTTP server.
- `config.go`: config defaults, validation, and persistence.
- `internal/auth/auth.go`: PocketBase auth setup, bootstrap, and token checks.
- `internal/library/library.go`: SQLite index ownership, scan support, search.
- `playback.go`: shared playback state machine.
- `rooms.go`: configured room catalog and room access checks.
- `server.go`: HTTP API, SSE, media serving, view shaping.
- `frontend/index.html`, `frontend/style.css`, `frontend/app.js`: listener UI.
- `frontend/admin.html`, `frontend/admin.js`: admin config UI.
- `web.go`: embedded filesystem.

## Future Direction

Highest priority:

- Browser-level/manual test coverage for multi-tab synchronization.
- Keep the playback model simple while hardening edge cases.
Next:

- Evaluate SQLite WAL mode for better read/write overlap during scans. Prefer
  enabling it only after confirming the database lives on local storage, not a
  network/synced path.
- Consider storing modification times with nanosecond precision if same-second
  file edits become a real concern.
- Add configurable ignored directory names if the built-in scanner pruning is
  not enough for real library layouts.
- Consider SQLite FTS if search becomes slow around very large libraries.
- Better admin-only surface and clearer listener/admin separation.
- Optional room join secrets.
- Authentication abstraction for non-LAN deployments.
