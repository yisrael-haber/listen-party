# listen-party

`listen-party` is a small LAN music server for shared rooms, offices, and
LAN-party setups. It indexes local MP3 files, serves one embedded browser UI,
and keeps all connected browsers on one shared playback state.

The project is intentionally plain:

- One Go binary.
- Embedded HTML/CSS/JS.
- SQLite metadata/search index.
- Basic Auth.
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
- There is one room today. The code keeps a room ID internally so a future room
  model can be added without changing the core playback object.

## Synchronization Model

The server is the source of truth for global playback:

- Current track.
- Queue and queue order.
- Recently played history.
- Playing or paused.
- Shared seek position.
- Track start time.
- Connected listener count.

Clients subscribe to `/events` with SSE. Every state update carries a revision
and server timestamp. The browser keeps its local `<audio>` element aligned to
that state and periodically re-checks drift. Client media events are local
except for `ended`, which asks the server to advance the shared playback state.

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
  "auth": {
    "listener": {"username": "default", "password": "default"},
    "admin": {"username": "admin", "password": "admin"}
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

Changing `addr` or `database_path` requires a restart. Updating auth
credentials, music directories, or scan worker count applies immediately; use
the admin rescan button to refresh the library after changing music folders.
`scan_workers` must be between 1 and 256.

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

Run:

```sh
./build/lp
```

Open:

```text
http://localhost:8080
```

Default listener login:

```text
default / default
```

Default admin login:

```text
admin / admin
```

## Deployment

For a simple LAN deployment:

1. Build the binary on the target machine or cross-compile for it.
2. Create a config file with the listen address, music folders, database path,
   and credentials.
3. Run the binary with `-config /path/to/config.json`.
4. Put MP3 files under one of the configured `music_dirs`.
5. Open the listener UI from LAN clients.

The binary serves its own static UI and media endpoints. No separate web server
is required for basic LAN use. If putting it behind a reverse proxy, preserve
SSE streaming for `/events`.

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
- `internal/library/library.go`: SQLite index ownership, scan support, search.
- `playback.go`: shared playback state machine.
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
- Optional room model with join secrets.
- Authentication abstraction for non-LAN deployments.
