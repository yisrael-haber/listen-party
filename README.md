# listen-party

`listen-party` is a small LAN music server for office / LAN-party environments.
It serves one embedded browser UI, indexes local MP3 files, and lets connected
browsers share one queue and one playback state.

The project is intentionally simple:

- Go single binary.
- Static embedded HTML/CSS/JS.
- SQLite for the local MP3 index.
- Basic Auth for the current POC.
- No internet services at runtime.

## Current State

Working:

- Recursive MP3 indexing from configured folders.
- Automatic first-run config creation.
- SQLite-backed metadata/search cache.
- Browser UI embedded in the binary.
- Shared queue.
- Add, remove, and clear queued tracks.
- Move queued tracks up, down, or directly to next.
- Explicit shared play/pause, seek, and skip controls.
- Play Now from search results or recently played tracks.
- Track-end auto advance.
- Server-owned playback state with client convergence for track, play/pause,
  seek position, and time drift.
- Connected listener count.
- Recently played history.
- Browser-local volume and mute controls.
- Search-as-you-type with recently added tracks as the empty search view.
- Library track count.
- SSE state updates and periodic state heartbeat.

Known limitation:

- Playback synchronization is much stronger than the original native-control
  model, but still depends on browser media behavior. Browser autoplay policy
  can stop a tab from making sound until that browser has been interacted with.
  That refusal is local and is not published back as shared state.
- Very small timing differences can still happen between browsers. Clients
  correct drift against server time when the local media element is more than
  0.1 seconds from the expected position.

Volume and mute are local to the tab session. They survive refresh in the same
tab, but are not synchronized with other tabs or stored on the server.

## Synchronization Model

The server is the durable source of truth for global playback:

- Current track.
- Queue.
- Queue order.
- Playing or paused.
- Shared seek position.
- Track start time.
- Recently played history.
- Connected listener count.

Clients receive state through SSE and periodic heartbeats. Each browser then
keeps its local audio element aligned to that state. Client audio events do not
become shared commands, except for `ended`, which advances the shared queue when
the current track finishes.

Global state changes come from explicit app controls:

- Play / pause.
- Seek.
- Skip.
- Queue add, remove, and clear.
- Queue move up, move down, and move to next.
- Play Now.

Volume and mute are intentionally outside the shared state. They are tab-local
preferences only.

## Config

Runtime config is JSON at:

```text
${UserConfigDir}/listen-party/config.json
```

The default SQLite database path is:

```text
${UserConfigDir}/listen-party/listen-party.sqlite
```

If the config file does not exist, the server creates it with these defaults:

```json
{
  "addr": "0.0.0.0:8080",
  "music_dirs": ["${UserConfigDir}/listen-party/music"],
  "database_path": "${UserConfigDir}/listen-party/listen-party.sqlite",
  "auth": {
    "listener": {"username": "default", "password": "default"},
    "admin": {"username": "admin", "password": "admin"},
    "rescan": {"username": "default", "password": "default"}
  }
}
```

Any configured `music_dirs` path that does not exist is created as an empty
directory at startup.

The server prints the resolved config directory at startup.

## Build And Run

Run from the repo root:

```sh
go run .
```

Build:

```sh
go build -o build/lp .
```

Run a built binary:

```sh
./build/lp
```

Use a custom config path:

```sh
./build/lp -config ./config.json
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

## Development Notes

Useful checks:

```sh
go test ./...
go build -o build/lp .
```

If port `8080` is stuck during local development:

```sh
fuser -k 8080/tcp
```

## Future Work

Highest priority:

- Add focused browser-level/manual test cases for two-tab synchronization.
- Keep hardening the current synchronization model without adding complex client
  state machines.

Next:

- Editable admin page for config and library management.
- Separate admin-only auth surface for administration.
- Scan stats: last scan time, indexed count, added/removed/skipped files.
- Future room model: dynamic rooms with join secrets.
- OAuth-capable auth abstraction for later deployment.
