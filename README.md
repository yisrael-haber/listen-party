# listen-party

`listen-party` is a self-hosted MP3 player for offices and trusted LANs. It
indexes local music, provides shared playback rooms, and keeps connected
browsers synchronized.

The server is one Go binary with an embedded web UI. It does not require a CDN,
external database, or runtime internet service. Metadata, playlists, and local
authentication are stored beside the configuration file.

## Features

- Recursive MP3 indexing and title, artist, or album search.
- Multiple rooms with independent playback, queue, and history state.
- Shared play/pause, seek, skip, previous, and play-now controls.
- Queue add, remove, clear, and drag-and-drop reordering.
- Optional per-room Auto-DJ playback when the queue is exhausted.
- Persistent user playlists with owner/admin editing.
- Native folder selection for importing indexed network-share tracks into playlists.
- Room permissions for everyone or selected authentication groups.
- Room administrators delegated through authentication groups.
- Local username/password authentication and optional Keycloak login.
- Admin UI for global configuration, rooms, room-admin assignment, bans, and rescans.
- Embedded frontend assets and SQLite storage.

## Quick Start

Go 1.25 or newer is required to build from source.

```sh
go run .
```

Open `http://localhost:8080`. On first run, the server creates its config,
storage directories, database, and authentication data.

The initial PocketBase superuser is:

```text
admin@listen-party.local / admin
```

Use these credentials only at `http://localhost:8080/authAdmin`, then change
the password. This superuser does not sign in to the music application.

Create application users in the `users` collection with:

- A username and password.
- `enabled` set to `true`.
- Optional groups for room permissions.
- Optional `app_role` set to `admin` for application administration.

Application users sign in at `http://localhost:8080/login`.

## Administration

| URL | Purpose |
| --- | --- |
| `/` | Music application |
| `/admin` | listen-party configuration and library rescans |
| `/authAdmin` | PocketBase users and authentication administration |
| `/healthz` | Unauthenticated health check |

Only application admins can access `/admin`. PocketBase superusers administer
`/authAdmin`; the two account types are separate.

The admin UI can update music directories, scan workers, banned IPs, rooms, and
room-administrator groups. Room access grants are managed only from the regular
application's room settings view. Room and ban changes apply immediately. Music
directory changes apply to subsequent scans. Address and authentication
provider changes require a restart.

## Configuration

The default config path is:

```text
${UserConfigDir}/listen-party/config.json
```

The SQLite database and PocketBase data directory are derived from the config
file's directory:

```text
<config-dir>/listen-party.sqlite
<config-dir>/auth
```

Use a different config location with:

```sh
./build/lp -config /path/to/config.json
```

A new configuration has this shape:

```json
{
  "version": 1,
  "revision": 1,
  "addr": "0.0.0.0:8080",
  "music_dirs": ["<config-dir>/music"],
  "scan_workers": 16,
  "banned_ips": [],
  "rooms": [
    {
      "id": "main",
      "name": "Public Room",
      "admin_groups": [],
      "grants": {
        "everyone": [
          "queue_add",
          "queue_manage",
          "playback_control"
        ]
      }
    }
  ],
  "auth": {
    "pocketbase": {
      "keycloak": {
        "enabled": false,
        "issuer_url": "",
        "client_id": "",
        "client_secret": "",
        "display_name": ""
      }
    }
  }
}
```

Configured music directories are created when missing. `scan_workers` must be
between 1 and 256. Room IDs must be unique, lowercase URL-safe values.

`banned_ips` contains exact client IP addresses. Proxy headers are not used, so
configure bans at the reverse proxy instead when one is present.

The config version is managed by the server. Version-zero pre-beta configs are
migrated and rewritten with an open first/default room.

## Rooms And Permissions

Every enabled user can see, enter, and listen to every room. Permissions only
control actions within a room.

Room grants are positive and additive. A user receives the union of grants for
their groups and the reserved `everyone` principal. There are no deny rules.
Application admins implicitly receive every room permission.

| Permission | Allows |
| --- | --- |
| `queue_add` | Add tracks; queue or shuffle playlists |
| `queue_manage` | Remove, reorder, or clear queued tracks; clear history; toggle Auto-DJ |
| `playback_control` | Play, pause, seek, skip, previous, and play-now |

Example restricted room:

```json
{
  "id": "office",
  "name": "Office",
  "admin_groups": ["office-admins"],
  "grants": {
    "staff": ["queue_add"],
    "facilities": [
      "queue_add",
      "queue_manage",
      "playback_control"
    ]
  }
}
```

Adding an `everyone` grant makes that permission available to every enabled
user. Removing it does not affect group grants.

Groups listed in `admin_groups` can edit that room's grants from the room
settings control in the regular application and implicitly receive every
permission in that room. Application admins administer every room and remain
responsible for assigning room administrator groups, creating rooms, and
changing global configuration.

Room administrators can also disconnect active listeners from the listener
menu. Disconnecting terminates every active tab for that listener, expires the
browser's application session, and requires a fresh sign-in before listening
can resume.

## Playlists

All enabled users can view playlists and create their own. Playlist owners and
application admins can add tracks, remove tracks, and delete the playlist.

Queueing or shuffling a playlist uses the room's `queue_add` permission.
Playlist viewing and ownership are independent of room permissions.

Playlist owners and application admins can use **Import from path...** to
append MP3s from a folder selected with the browser's native directory picker.
The browser sends only relative filenames, sizes, and modification times;
audio files are not uploaded. The server imports matches from its existing
index and reports unmatched or ambiguous files. The selected network share
must therefore be available to both the browser user's computer and the
server, though their mount paths may differ.

## Music Library

The server scans every configured music directory at startup. Use **Rescan** in
`/admin` for a full scan or the button beside a music directory for a targeted
scan. Incremental scans skip unchanged files and remove missing files from the
active index.

Only MP3 files are indexed. Playlists retain their stored entries when a file
is temporarily unavailable, but unavailable tracks cannot be played until the
library can resolve them again.

## Keycloak

Keycloak is optional. Configure a realm and confidential OIDC client, then set:

```json
"keycloak": {
  "enabled": true,
  "issuer_url": "https://keycloak.example/realms/listen-party",
  "client_id": "listen-party",
  "client_secret": "client-secret",
  "display_name": "Keycloak"
}
```

The Keycloak user must have a username. On first login, listen-party creates or
links the corresponding local application user.

To use Keycloak groups for room grants, include a `groups` claim in the OIDC
userinfo response. Group values are copied into the local user on login. If the
claim is absent, existing local groups are retained.

Restart the server after changing authentication settings.

## Build And Package

Build for the current platform:

```sh
go build -o build/lp .
```

Build Linux and Windows AMD64 binaries:

```sh
make compile
```

Create a deployment archive containing both binaries and the current config
directory:

```sh
make package
```

Packages are written under `publish/`. They include authentication data and the
library database; handle them as sensitive backups.

## Deployment Notes

- The default address, `0.0.0.0:8080`, listens on every network interface.
- This project targets trusted, non-internet-adjoined networks.
- No separate web server is required.
- When using a reverse proxy, preserve streaming responses for room updates.
- Logs are written to stdout/stderr.
- Room queues, current playback, history, and Auto-DJ state are held in memory
  and reset when the server restarts. Playlists and library metadata persist.

## Limitations

- Browser autoplay policy may require one user interaction before audio starts.
- Synchronization is intended for shared listening, not sample-accurate audio.
- Volume and mute are local to each browser.
- Search/view preferences and the last selected playlist are stored in browser
  local storage.

## Development

```sh
go test ./...
go test -race ./...
go build -o build/lp .
```

Third-party browser assets and licenses are stored under `frontend/vendor/` and
embedded into the binary. Runtime internet access is not required.

## License

See [LICENSE](LICENSE).
