package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/tommyo123/mtag"
	_ "modernc.org/sqlite"
)

type Track struct {
	ID         int64 `json:"id"`
	path       string
	Title      string    `json:"title"`
	Artist     string    `json:"artist"`
	Album      string    `json:"album"`
	TrackNo    int       `json:"track_no"`
	DurationMS int64     `json:"duration_ms"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	ContentKey string    `json:"content_key"`
	Lossless   bool      `json:"lossless"`
	Bitrate    int       `json:"bitrate"`
	Available  bool      `json:"available"`
}

type Playlist struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	OwnerID   string         `json:"owner_id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Items     []PlaylistItem `json:"items,omitempty"`
}

type PlaylistItem struct {
	ID         int64  `json:"id"`
	PlaylistID int64  `json:"playlist_id"`
	Position   int    `json:"position"`
	ContentKey string `json:"content_key"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
}

type FolderManifestFile struct {
	RelativePath   string `json:"relative_path"`
	Size           int64  `json:"size"`
	LastModifiedMS int64  `json:"last_modified_ms"`
}

type PlaylistFolderImport struct {
	Imported   int `json:"imported"`
	Duplicates int `json:"duplicates"`
	Unmatched  int `json:"unmatched"`
	Ambiguous  int `json:"ambiguous"`
}

type PlaylistKeyImport struct {
	Imported    int `json:"imported"`
	Duplicates  int `json:"duplicates"`
	Unavailable int `json:"unavailable"`
	Invalid     int `json:"invalid"`
}

// RoomPlaybackSnapshot is opaque application state associated with a room.
// It intentionally lives beside the library data so a single SQLite database
// remains the durable unit for a listen-party installation.
type RoomPlaybackSnapshot struct {
	RoomID   string
	Revision uint64
	State    []byte
}

type Media struct {
	Track Track
	file  *os.File
}

func (m *Media) Read(p []byte) (int, error) {
	return m.file.Read(p)
}

func (m *Media) Seek(offset int64, whence int) (int64, error) {
	return m.file.Seek(offset, whence)
}

func (m *Media) Close() error {
	return m.file.Close()
}

func (m *Media) Name() string {
	return filepath.Base(m.Track.path)
}

func (m *Media) ContentType() string {
	return audioMIME(m.Track.path)
}

func (m *Media) ModTime() time.Time {
	return m.Track.ModTime
}

type Library struct {
	mu              sync.RWMutex
	scanMu          sync.Mutex
	statusMu        sync.RWMutex
	durationLoading sync.Map
	db              *sql.DB
	dirs            []string
	workers         int
	status          ScanStatus
}

type ScanStatus struct {
	Scanning            bool      `json:"scanning"`
	Roots               []string  `json:"roots"`
	LastStarted         time.Time `json:"last_started"`
	LastCompleted       time.Time `json:"last_completed"`
	LastError           string    `json:"last_error"`
	DurationMS          int64     `json:"duration_ms"`
	AudioSeen           int       `json:"audio_seen"`
	Parsed              int64     `json:"parsed"`
	Indexed             int64     `json:"indexed"`
	Unchanged           int       `json:"unchanged"`
	IgnoredDirs         int       `json:"ignored_dirs"`
	Skipped             int64     `json:"skipped"`
	Removed             int       `json:"removed"`
	PendingPaths        int       `json:"pending_paths"`
	PendingWrites       int       `json:"pending_writes"`
	RemainingKnown      int       `json:"remaining_known"`
	RecentTracksPerSec  float64   `json:"recent_tracks_per_sec"`
	AverageTracksPerSec float64   `json:"average_tracks_per_sec"`
}

var (
	ErrTrackNotFound    = errors.New("track not found")
	ErrPlaylistNotFound = errors.New("playlist not found")
	ErrScanInProgress   = errors.New("library scan already in progress")
)

const (
	defaultTrackQueryLimit = 25
	maxTrackQueryLimit     = 100
	scanWriteBatchSize     = 1000
	scanWriteDrainMax      = 4000
	scanPathBufferSize     = 4096
	scanMaxWorkers         = 256
	scanProgressLogEvery   = 5 * time.Second
	librarySchemaVersion   = "4"
	trackPropertiesVersion = 1
)

const trackSelectColumns = `id, path, title, artist, album, track_no, duration_ms, size, mod_time, content_key, lossless, bitrate_bps, available`
const qualifiedTrackSelectColumns = `t.id, t.path, t.title, t.artist, t.album, t.track_no, t.duration_ms, t.size, t.mod_time, t.content_key, t.lossless, t.bitrate_bps, t.available`

func Open(ctx context.Context, path string, dirs []string, workers int) (*Library, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	lib := &Library{
		db:      db,
		dirs:    append([]string(nil), dirs...),
		workers: normalizeWorkers(workers),
	}
	if err := lib.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return lib, nil
}

func normalizeWorkers(workers int) int {
	if workers <= 0 {
		return 1
	}
	if workers > scanMaxWorkers {
		return scanMaxWorkers
	}
	return workers
}

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func (l *Library) Close() error {
	return l.db.Close()
}

func (l *Library) migrate(ctx context.Context) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS library_metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
)`); err != nil {
		return err
	}
	var version string
	err = tx.QueryRowContext(ctx, `SELECT value FROM library_metadata WHERE key = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version == librarySchemaVersion {
		return tx.Commit()
	}
	switch version {
	case "2", "3":
		if err := migrateLegacyToV4(ctx, tx, version); err != nil {
			return err
		}
	default:
		if _, err := tx.ExecContext(ctx, `
DROP TABLE IF EXISTS playlist_items;
DROP TABLE IF EXISTS tracks_fts;
DROP TABLE IF EXISTS tracks;
CREATE TABLE IF NOT EXISTS tracks (
	id INTEGER PRIMARY KEY,
	path TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	artist TEXT NOT NULL,
	album TEXT NOT NULL,
	track_no INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	size INTEGER NOT NULL,
	mod_time INTEGER NOT NULL,
	dedupe_key TEXT NOT NULL DEFAULT '',
	match_key TEXT NOT NULL DEFAULT '',
	content_key TEXT NOT NULL DEFAULT '',
	lossless INTEGER NOT NULL DEFAULT 0,
	bitrate_bps INTEGER NOT NULL DEFAULT 0,
	properties_version INTEGER NOT NULL DEFAULT 0,
	available INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS tracks_available_idx ON tracks(available);
CREATE VIRTUAL TABLE IF NOT EXISTS tracks_fts USING fts5(
	title,
	artist,
	album,
	content='tracks',
	content_rowid='id',
	prefix='2 3'
);
CREATE TRIGGER IF NOT EXISTS tracks_ai AFTER INSERT ON tracks BEGIN
	INSERT INTO tracks_fts(rowid, title, artist, album) VALUES (new.id, new.title, new.artist, new.album);
END;
CREATE TRIGGER IF NOT EXISTS tracks_ad AFTER DELETE ON tracks BEGIN
	INSERT INTO tracks_fts(tracks_fts, rowid, title, artist, album) VALUES ('delete', old.id, old.title, old.artist, old.album);
END;
CREATE TRIGGER IF NOT EXISTS tracks_au AFTER UPDATE ON tracks BEGIN
	INSERT INTO tracks_fts(tracks_fts, rowid, title, artist, album) VALUES ('delete', old.id, old.title, old.artist, old.album);
	INSERT INTO tracks_fts(rowid, title, artist, album) VALUES (new.id, new.title, new.artist, new.album);
END;
CREATE TABLE IF NOT EXISTS playlists (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	owner_id TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS playlist_items (
	id INTEGER PRIMARY KEY,
	playlist_id INTEGER NOT NULL,
	position INTEGER NOT NULL,
	dedupe_key TEXT NOT NULL DEFAULT '',
	match_key TEXT NOT NULL DEFAULT '',
	content_key TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	artist TEXT NOT NULL,
	album TEXT NOT NULL,
	FOREIGN KEY(playlist_id) REFERENCES playlists(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS playlist_items_playlist_idx ON playlist_items(playlist_id, position);
`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS playlist_items_playlist_content_idx ON playlist_items(playlist_id, content_key);
CREATE INDEX IF NOT EXISTS tracks_content_idx ON tracks(content_key, available);
DROP TABLE IF EXISTS room_playback_state;
CREATE TABLE room_playback_state (
	room_id TEXT PRIMARY KEY,
	revision INTEGER NOT NULL,
	state_json BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);
INSERT INTO library_metadata(key, value) VALUES ('schema_version', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;`, librarySchemaVersion); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if version != "2" && version != "3" {
		slog.Warn("reset library content state; retained playlists; reindex required", "schema_version", librarySchemaVersion)
	}
	return nil
}

func migrateLegacyToV4(ctx context.Context, tx *sql.Tx, version string) error {
	if version == "2" {
		if _, err := tx.ExecContext(ctx, `
ALTER TABLE tracks ADD COLUMN content_key TEXT NOT NULL DEFAULT '';
ALTER TABLE tracks ADD COLUMN lossless INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN bitrate_bps INTEGER NOT NULL DEFAULT 0;`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
ALTER TABLE tracks ADD COLUMN properties_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playlist_items ADD COLUMN content_key TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}

	type trackKey struct {
		id  int64
		key string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, title, artist, album, track_no FROM tracks`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tracks []trackKey
	for rows.Next() {
		var track Track
		if err := rows.Scan(&track.ID, &track.Title, &track.Artist, &track.Album, &track.TrackNo); err != nil {
			return err
		}
		tracks = append(tracks, trackKey{track.ID, contentKey(track.Artist, track.Title, track.Album, track.TrackNo)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	updateTrack, err := tx.PrepareContext(ctx, `UPDATE tracks SET content_key = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer updateTrack.Close()
	for _, track := range tracks {
		if _, err := updateTrack.ExecContext(ctx, track.key, track.id); err != nil {
			return err
		}
	}

	// Track identities must be populated before translating playlist references.
	_, err = tx.ExecContext(ctx, `
UPDATE playlist_items SET content_key = COALESCE((
	SELECT content_key FROM tracks
	WHERE tracks.dedupe_key = playlist_items.dedupe_key AND tracks.dedupe_key != ''
	ORDER BY path ASC LIMIT 1
), '');
DELETE FROM playlist_items WHERE content_key = '';`)
	return err
}

type knownTrack struct {
	modTime           int64
	propertiesVersion int
}

func (l *Library) loadKnownTracks(ctx context.Context, roots []string) (map[string]knownTrack, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT path, mod_time, properties_version FROM tracks WHERE available = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	known := make(map[string]knownTrack)
	for rows.Next() {
		var path string
		var knownFile knownTrack
		if err := rows.Scan(&path, &knownFile.modTime, &knownFile.propertiesVersion); err != nil {
			return nil, err
		}
		if len(roots) > 0 && !pathInRoots(path, roots) {
			continue
		}
		known[path] = knownFile
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return known, nil
}

func pathInRoots(path string, roots []string) bool {
	for _, root := range roots {
		if pathInRoot(path, root) {
			return true
		}
	}
	return false
}

func pathInRoot(path string, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

const upsertTrackSQL = `
INSERT INTO tracks(path, title, artist, album, track_no, duration_ms, size, mod_time, dedupe_key, match_key, content_key, lossless, bitrate_bps, properties_version, available)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, 1)
ON CONFLICT(path) DO UPDATE SET
	title = excluded.title,
	artist = excluded.artist,
	album = excluded.album,
	track_no = excluded.track_no,
	duration_ms = CASE WHEN tracks.mod_time = excluded.mod_time THEN tracks.duration_ms ELSE excluded.duration_ms END,
	size = excluded.size,
	mod_time = excluded.mod_time,
	content_key = excluded.content_key,
	lossless = excluded.lossless,
	bitrate_bps = excluded.bitrate_bps,
	properties_version = excluded.properties_version,
	available = 1
`

func (l *Library) flushTracks(ctx context.Context, tracks []Track) error {
	if len(tracks) == 0 {
		return nil
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, upsertTrackSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range tracks {
		normalizeTrackDisplay(&t)
		if t.Title == "" {
			t.Title = fallbackTitle(t.path)
		}
		t.ContentKey = contentKey(t.Artist, t.Title, t.Album, t.TrackNo)
		if _, err := stmt.ExecContext(ctx, t.path, t.Title, t.Artist, t.Album, t.TrackNo, t.DurationMS, t.Size, t.ModTime.Unix(), t.ContentKey, t.Lossless, t.Bitrate, trackPropertiesVersion); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (l *Library) writeScannedTracks(ctx context.Context, tracks <-chan Track, indexed *int64) error {
	batch := make([]Track, 0, scanWriteDrainMax)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n := len(batch)
		if err := l.flushTracks(ctx, batch); err != nil {
			return err
		}
		atomic.AddInt64(indexed, int64(n))
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case track, ok := <-tracks:
			if !ok {
				return flush()
			}
			batch = append(batch, track)
		}

		if len(batch) < scanWriteBatchSize {
			continue
		}
	drain:
		for len(batch) < scanWriteDrainMax {
			select {
			case track, ok := <-tracks:
				if !ok {
					return flush()
				}
				batch = append(batch, track)
			default:
				break drain
			}
		}
		if err := flush(); err != nil {
			return err
		}
	}
}

func (l *Library) deleteMissing(ctx context.Context, paths map[string]knownTrack) error {
	if len(paths) == 0 {
		return nil
	}
	batch := make([]string, 0, scanWriteBatchSize)
	for path := range paths {
		batch = append(batch, path)
		if len(batch) == scanWriteBatchSize {
			if err := l.deletePathBatch(ctx, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		return l.deletePathBatch(ctx, batch)
	}
	return nil
}

func (l *Library) deletePathBatch(ctx context.Context, paths []string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM tracks WHERE path = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, path := range paths {
		if _, err := stmt.ExecContext(ctx, path); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (l *Library) Search(ctx context.Context, q string) ([]Track, error) {
	return l.SearchField(ctx, q, "")
}

func (l *Library) SearchField(ctx context.Context, q string, field string) ([]Track, error) {
	limit := maxTrackQueryLimit
	if strings.TrimSpace(q) == "" {
		return l.recent(ctx, limit)
	}
	query := searchFTSQuery(q, field)
	if query == "" {
		return l.recent(ctx, limit)
	}
	rows, err := l.db.QueryContext(ctx, `
SELECT `+trackSelectColumns+`
FROM (
	SELECT `+qualifiedTrackSelectColumns+`,
		row_number() OVER (PARTITION BY t.content_key ORDER BY t.lossless DESC, t.bitrate_bps DESC, t.path ASC) AS rn
	FROM tracks t
	JOIN tracks_fts ON tracks_fts.rowid = t.id
	WHERE t.available = 1 AND tracks_fts MATCH ?
)
WHERE rn = 1
ORDER BY title COLLATE NOCASE ASC, artist COLLATE NOCASE ASC, album COLLATE NOCASE ASC, track_no ASC
LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

func searchFTSQuery(q string, field string) string {
	terms := strings.Fields(normalizeSearch(q))
	if len(terms) == 0 {
		return ""
	}
	for i, term := range terms {
		terms[i] = term + "*"
	}
	query := strings.Join(terms, " ")
	switch field {
	case "title":
		return "title: " + query
	case "artist":
		return "artist: " + query
	case "album":
		return "album: " + query
	default:
		return query
	}
}

func (l *Library) recent(ctx context.Context, limit int) ([]Track, error) {
	limit = clampTrackQueryLimit(limit)
	rows, err := l.db.QueryContext(ctx, `
SELECT `+trackSelectColumns+`
FROM (
	SELECT `+qualifiedTrackSelectColumns+`,
		row_number() OVER (PARTITION BY t.content_key ORDER BY t.lossless DESC, t.bitrate_bps DESC, t.path ASC) AS rn
	FROM tracks t
	WHERE t.available = 1
)
WHERE rn = 1
ORDER BY mod_time DESC, title
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

func clampTrackQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultTrackQueryLimit
	}
	if limit > maxTrackQueryLimit {
		return maxTrackQueryLimit
	}
	return limit
}

func (l *Library) Count(ctx context.Context) (int64, error) {
	var count int64
	err := l.db.QueryRowContext(ctx, `SELECT count(*) FROM tracks WHERE available = 1`).Scan(&count)
	return count, err
}

func (l *Library) LoadRoomPlaybackSnapshots(ctx context.Context) ([]RoomPlaybackSnapshot, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT room_id, revision, state_json FROM room_playback_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []RoomPlaybackSnapshot
	for rows.Next() {
		var snapshot RoomPlaybackSnapshot
		if err := rows.Scan(&snapshot.RoomID, &snapshot.Revision, &snapshot.State); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (l *Library) SaveRoomPlaybackSnapshot(ctx context.Context, snapshot RoomPlaybackSnapshot) error {
	_, err := l.db.ExecContext(ctx, `
INSERT INTO room_playback_state (room_id, revision, state_json, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(room_id) DO UPDATE SET
	revision = excluded.revision,
	state_json = excluded.state_json,
	updated_at = excluded.updated_at
WHERE excluded.revision >= room_playback_state.revision`,
		snapshot.RoomID, snapshot.Revision, snapshot.State, time.Now().UnixMilli())
	return err
}

func (l *Library) DeleteRoomPlaybackSnapshot(ctx context.Context, roomID string) error {
	_, err := l.db.ExecContext(ctx, `DELETE FROM room_playback_state WHERE room_id = ?`, roomID)
	return err
}

func (l *Library) Get(ctx context.Context, id int64) (Track, error) {
	return l.get(ctx, id, true)
}

func (l *Library) get(ctx context.Context, id int64, fillDuration bool) (Track, error) {
	row := l.db.QueryRowContext(ctx, `
SELECT `+trackSelectColumns+`
FROM tracks
WHERE id = ? AND available = 1`, id)
	track, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, ErrTrackNotFound
	}
	if err != nil {
		return Track{}, err
	}
	if fillDuration && track.DurationMS == 0 {
		track.DurationMS = audioDurationMS(track.path)
		if track.DurationMS > 0 {
			_, _ = l.db.ExecContext(ctx, `UPDATE tracks SET duration_ms = ? WHERE id = ?`, track.DurationMS, id)
		}
	}
	return track, nil
}

func audioDurationMS(path string) int64 {
	file, err := mtag.Open(path,
		mtag.WithReadOnly(),
		mtag.WithSkipPictures(),
		mtag.WithAudioPropertiesStyle(mtag.AudioPropertiesAccurate),
	)
	if err != nil {
		return 0
	}
	defer file.Close()
	return file.AudioProperties().Duration.Milliseconds()
}

func (l *Library) EnsureDuration(id int64) <-chan struct{} {
	done := make(chan struct{})
	if id <= 0 {
		close(done)
		return done
	}
	actual, loaded := l.durationLoading.LoadOrStore(id, done)
	if loaded {
		return actual.(chan struct{})
	}
	go func() {
		_, _ = l.Get(context.Background(), id)
		close(done)
		l.durationLoading.Delete(id)
	}()
	return done
}

func (l *Library) ListByContentKeys(ctx context.Context, keys []string) (map[string]Track, error) {
	out := make(map[string]Track, len(keys))
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	if len(unique) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for i, key := range unique {
		placeholders[i] = "?"
		args[i] = key
	}
	rows, err := l.db.QueryContext(ctx, `SELECT `+trackSelectColumns+` FROM tracks WHERE available = 1 AND content_key IN (`+strings.Join(placeholders, ",")+`) ORDER BY content_key ASC, lossless DESC, bitrate_bps DESC, path ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		track, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		if _, exists := out[track.ContentKey]; !exists {
			out[track.ContentKey] = track
		}
	}
	return out, rows.Err()
}

func (l *Library) OpenMedia(ctx context.Context, id int64) (*Media, error) {
	track, err := l.get(ctx, id, false)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(track.path)
	if err != nil {
		return nil, err
	}
	return &Media{Track: track, file: file}, nil
}

func (l *Library) Artwork(ctx context.Context, id int64) ([]byte, string, error) {
	track, err := l.get(ctx, id, false)
	if err != nil {
		return nil, "", err
	}
	file, err := mtag.Open(track.path, mtag.WithReadOnly())
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	var selected mtag.Picture
	for _, picture := range file.Images() {
		if len(picture.Data) == 0 {
			continue
		}
		if len(selected.Data) == 0 || picture.Type == mtag.PictureCoverFront {
			selected = picture
		}
		if picture.Type == mtag.PictureCoverFront {
			break
		}
	}
	if len(selected.Data) == 0 {
		return nil, "", ErrTrackNotFound
	}
	mimeType := selected.MIME
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return selected.Data, mimeType, nil
}

func (l *Library) CreatePlaylist(ctx context.Context, name, ownerID string) (Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Playlist{}, errors.New("playlist name is required")
	}
	now := time.Now()
	res, err := l.db.ExecContext(ctx, `INSERT INTO playlists(name, owner_id, created_at, updated_at) VALUES(?, ?, ?, ?)`, name, ownerID, now.Unix(), now.Unix())
	if err != nil {
		return Playlist{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Playlist{}, err
	}
	return Playlist{ID: id, Name: name, OwnerID: ownerID, CreatedAt: now, UpdatedAt: now}, nil
}

func (l *Library) ListPlaylists(ctx context.Context) ([]Playlist, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, name, owner_id, created_at, updated_at FROM playlists ORDER BY name COLLATE NOCASE ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var playlists []Playlist
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		playlists = append(playlists, p)
	}
	return playlists, rows.Err()
}

func (l *Library) GetPlaylist(ctx context.Context, id int64) (Playlist, error) {
	p, err := l.GetPlaylistMetadata(ctx, id)
	if err != nil {
		return Playlist{}, err
	}
	items, err := l.PlaylistItems(ctx, id)
	if err != nil {
		return Playlist{}, err
	}
	p.Items = items
	return p, nil
}

func (l *Library) GetPlaylistMetadata(ctx context.Context, id int64) (Playlist, error) {
	row := l.db.QueryRowContext(ctx, `SELECT id, name, owner_id, created_at, updated_at FROM playlists WHERE id = ?`, id)
	p, err := scanPlaylist(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrPlaylistNotFound
	}
	if err != nil {
		return Playlist{}, err
	}
	return p, nil
}

func (l *Library) PlaylistItems(ctx context.Context, playlistID int64) ([]PlaylistItem, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, playlist_id, position, content_key, title, artist, album FROM playlist_items WHERE playlist_id = ? ORDER BY position ASC, id ASC`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PlaylistItem
	for rows.Next() {
		item, err := scanPlaylistItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (l *Library) AddPlaylistTrack(ctx context.Context, playlistID int64, contentKey string) (PlaylistItem, error) {
	track, err := l.ResolveContentKey(ctx, contentKey)
	if err != nil {
		return PlaylistItem{}, err
	}
	var position int
	if err := l.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&position); err != nil {
		return PlaylistItem{}, err
	}
	res, err := l.db.ExecContext(ctx, `INSERT INTO playlist_items(playlist_id, position, dedupe_key, match_key, content_key, title, artist, album) VALUES(?, ?, '', '', ?, ?, ?, ?)`,
		playlistID, position, track.ContentKey, track.Title, track.Artist, track.Album)
	if err != nil {
		return PlaylistItem{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PlaylistItem{}, err
	}
	return PlaylistItem{ID: id, PlaylistID: playlistID, Position: position, ContentKey: track.ContentKey, Title: track.Title, Artist: track.Artist, Album: track.Album}, nil
}

func (l *Library) ImportPlaylistKeys(ctx context.Context, playlistID int64, keys []string) (PlaylistKeyImport, error) {
	result := PlaylistKeyImport{}
	tracks := make([]Track, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			result.Invalid++
			continue
		}
		if _, ok := seen[key]; ok {
			result.Duplicates++
			continue
		}
		seen[key] = struct{}{}
		track, err := l.ResolveContentKey(ctx, key)
		if errors.Is(err, ErrTrackNotFound) {
			result.Unavailable++
			continue
		}
		if err != nil {
			return result, err
		}
		tracks = append(tracks, track)
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM playlists WHERE id = ?`, playlistID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return result, ErrPlaylistNotFound
	} else if err != nil {
		return result, err
	}
	existing := make(map[string]struct{})
	rows, err := tx.QueryContext(ctx, `SELECT content_key FROM playlist_items WHERE playlist_id = ?`, playlistID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return result, err
		}
		existing[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&position); err != nil {
		return result, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO playlist_items(playlist_id, position, dedupe_key, match_key, content_key, title, artist, album) VALUES(?, ?, '', '', ?, ?, ?, ?)`)
	if err != nil {
		return result, err
	}
	defer stmt.Close()
	for _, track := range tracks {
		if _, ok := existing[track.ContentKey]; ok {
			result.Duplicates++
			continue
		}
		position++
		if _, err := stmt.ExecContext(ctx, playlistID, position, track.ContentKey, track.Title, track.Artist, track.Album); err != nil {
			return result, err
		}
		existing[track.ContentKey] = struct{}{}
		result.Imported++
	}
	if result.Imported > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at = ? WHERE id = ?`, time.Now().Unix(), playlistID); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (l *Library) ImportPlaylistFolder(ctx context.Context, playlistID int64, files []FolderManifestFile) (PlaylistFolderImport, error) {
	result := PlaylistFolderImport{}
	if len(files) == 0 {
		return result, errors.New("folder contains no supported audio files")
	}
	type indexedFile struct {
		path       string
		size       int64
		modTime    int64
		contentKey string
		title      string
		artist     string
		album      string
	}
	bySize := make(map[int64][]indexedFile)
	sizes := make([]int64, 0, len(files))
	seenSizes := make(map[int64]struct{}, len(files))
	for _, file := range files {
		if file.Size < 0 {
			continue
		}
		if _, ok := seenSizes[file.Size]; !ok {
			seenSizes[file.Size] = struct{}{}
			sizes = append(sizes, file.Size)
		}
	}
	const sizeQueryBatch = 500
	for start := 0; start < len(sizes); start += sizeQueryBatch {
		end := min(start+sizeQueryBatch, len(sizes))
		placeholders := make([]string, end-start)
		args := make([]any, end-start)
		for i, size := range sizes[start:end] {
			placeholders[i] = "?"
			args[i] = size
		}
		rows, err := l.db.QueryContext(ctx, `SELECT path, size, mod_time, content_key, title, artist, album FROM tracks WHERE available = 1 AND size IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var file indexedFile
			if err := rows.Scan(&file.path, &file.size, &file.modTime, &file.contentKey, &file.title, &file.artist, &file.album); err != nil {
				rows.Close()
				return result, err
			}
			bySize[file.size] = append(bySize[file.size], file)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return result, err
		}
		if err := rows.Close(); err != nil {
			return result, err
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].RelativePath) < strings.ToLower(files[j].RelativePath)
	})
	matched := make([]indexedFile, 0, len(files))
	for _, manifest := range files {
		relative, ok := cleanManifestPath(manifest.RelativePath)
		if !ok || !isSupportedAudio(relative) {
			result.Unmatched++
			continue
		}
		logical := make(map[string]indexedFile)
		exactTime := make(map[string]indexedFile)
		for _, candidate := range bySize[manifest.Size] {
			if !pathHasSuffix(candidate.path, relative) {
				continue
			}
			logical[candidate.contentKey] = candidate
			if manifest.LastModifiedMS > 0 && candidate.modTime == manifest.LastModifiedMS/1000 {
				exactTime[candidate.contentKey] = candidate
			}
		}
		if len(logical) == 1 {
			for _, candidate := range logical {
				matched = append(matched, candidate)
			}
			continue
		}
		if len(exactTime) == 1 {
			for _, candidate := range exactTime {
				matched = append(matched, candidate)
			}
			continue
		}
		if len(logical) == 0 {
			result.Unmatched++
		} else {
			result.Ambiguous++
		}
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM playlists WHERE id = ?`, playlistID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return result, ErrPlaylistNotFound
	} else if err != nil {
		return result, err
	}
	existing := make(map[string]struct{})
	existingRows, err := tx.QueryContext(ctx, `SELECT content_key FROM playlist_items WHERE playlist_id = ?`, playlistID)
	if err != nil {
		return result, err
	}
	for existingRows.Next() {
		var key string
		if err := existingRows.Scan(&key); err != nil {
			existingRows.Close()
			return result, err
		}
		existing[key] = struct{}{}
	}
	if err := existingRows.Err(); err != nil {
		existingRows.Close()
		return result, err
	}
	if err := existingRows.Close(); err != nil {
		return result, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&position); err != nil {
		return result, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO playlist_items(playlist_id, position, dedupe_key, match_key, content_key, title, artist, album) VALUES(?, ?, '', '', ?, ?, ?, ?)`)
	if err != nil {
		return result, err
	}
	defer stmt.Close()
	for _, file := range matched {
		if _, ok := existing[file.contentKey]; ok {
			result.Duplicates++
			continue
		}
		position++
		if _, err := stmt.ExecContext(ctx, playlistID, position, file.contentKey, file.title, file.artist, file.album); err != nil {
			return result, err
		}
		existing[file.contentKey] = struct{}{}
		result.Imported++
	}
	if result.Imported > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at = ? WHERE id = ?`, time.Now().Unix(), playlistID); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func cleanManifestPath(value string) (string, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	cleaned := pathpkg.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || pathpkg.IsAbs(cleaned) {
		return "", false
	}
	return cleaned, true
}

func pathHasSuffix(indexedPath, relative string) bool {
	indexed := strings.ReplaceAll(filepath.Clean(indexedPath), `\`, "/")
	relative = strings.TrimPrefix(relative, "/")
	return indexed == relative || strings.HasSuffix(indexed, "/"+relative)
}

func (l *Library) RemovePlaylistItem(ctx context.Context, playlistID, itemID int64) error {
	_, err := l.db.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id = ? AND id = ?`, playlistID, itemID)
	return err
}

func (l *Library) DeletePlaylist(ctx context.Context, playlistID int64) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id = ?`, playlistID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, playlistID)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrPlaylistNotFound
	}
	return tx.Commit()
}

func (l *Library) ResolveContentKey(ctx context.Context, key string) (Track, error) {
	row := l.db.QueryRowContext(ctx, `SELECT `+trackSelectColumns+` FROM tracks WHERE available = 1 AND content_key = ? ORDER BY lossless DESC, bitrate_bps DESC, path ASC LIMIT 1`, key)
	track, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, ErrTrackNotFound
	}
	return track, err
}

func (l *Library) ShuffleContentKeys(ctx context.Context) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT content_key FROM tracks WHERE available = 1 GROUP BY content_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (l *Library) PlaylistShuffleContentKeys(ctx context.Context, playlistID int64) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `
SELECT pi.content_key
FROM playlist_items pi
WHERE pi.playlist_id = ?
  AND EXISTS (SELECT 1 FROM tracks t WHERE t.content_key = pi.content_key AND t.available = 1)
GROUP BY pi.content_key`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func scanPlaylist(row rowScanner) (Playlist, error) {
	var p Playlist
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Name, &p.OwnerID, &created, &updated); err != nil {
		return Playlist{}, err
	}
	p.CreatedAt = time.Unix(created, 0)
	p.UpdatedAt = time.Unix(updated, 0)
	return p, nil
}

func scanPlaylistItem(row rowScanner) (PlaylistItem, error) {
	var item PlaylistItem
	if err := row.Scan(&item.ID, &item.PlaylistID, &item.Position, &item.ContentKey, &item.Title, &item.Artist, &item.Album); err != nil {
		return PlaylistItem{}, err
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrack(row rowScanner) (Track, error) {
	var t Track
	var unix int64
	var available int
	var lossless int
	if err := row.Scan(&t.ID, &t.path, &t.Title, &t.Artist, &t.Album, &t.TrackNo, &t.DurationMS, &t.Size, &unix, &t.ContentKey, &lossless, &t.Bitrate, &available); err != nil {
		return Track{}, err
	}
	t.ModTime = time.Unix(unix, 0)
	t.Lossless = lossless == 1
	t.Available = available == 1
	normalizeTrackDisplay(&t)
	return t, nil
}

func scanTracks(rows *sql.Rows) ([]Track, error) {
	var tracks []Track
	for rows.Next() {
		t, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tracks, nil
}

func (l *Library) UpdateScanConfig(dirs []string, workers int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dirs = append([]string(nil), dirs...)
	l.workers = normalizeWorkers(workers)
}

func (l *Library) ScanStatus() ScanStatus {
	l.statusMu.RLock()
	defer l.statusMu.RUnlock()
	return l.status
}

func shouldIgnoreDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "__") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "target", "bin", "obj",
		"system volume information", "$recycle.bin", "@eadir":
		return true
	default:
		return false
	}
}

type scanFile struct {
	path string
	info fs.FileInfo
}

func (l *Library) Scan(ctx context.Context) (err error) {
	l.mu.RLock()
	dirs := append([]string(nil), l.dirs...)
	workers := l.workers
	l.mu.RUnlock()
	return l.scanDirs(ctx, dirs, workers, nil)
}

func (l *Library) ScanDir(ctx context.Context, dir string) error {
	l.mu.RLock()
	workers := l.workers
	l.mu.RUnlock()
	dirs := []string{dir}
	return l.scanDirs(ctx, dirs, workers, dirs)
}

func (l *Library) scanDirs(ctx context.Context, dirs []string, workers int, deletionRoots []string) (err error) {
	if !l.scanMu.TryLock() {
		return ErrScanInProgress
	}
	defer l.scanMu.Unlock()

	started := time.Now()
	l.statusMu.Lock()
	l.status = ScanStatus{
		Scanning:    true,
		Roots:       append([]string(nil), dirs...),
		LastStarted: started,
	}
	l.statusMu.Unlock()
	defer func() {
		l.statusMu.Lock()
		l.status.Scanning = false
		l.status.LastCompleted = time.Now()
		l.status.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			l.status.LastError = err.Error()
		}
		l.statusMu.Unlock()
	}()

	var seen, unchanged, ignoredDirs int
	var parsed, indexed, skipped int64
	var walkFailed bool

	known, err := l.loadKnownTracks(ctx, deletionRoots)
	if err != nil {
		return err
	}
	slog.Info("library scan index loaded", "known_tracks", len(known), "music_dirs", len(dirs), "scan_workers", workers)
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	paths := make(chan scanFile, scanPathBufferSize)
	tracks := make(chan Track, scanWriteDrainMax)
	writerDone := make(chan error, 1)
	go func() {
		err := l.writeScannedTracks(scanCtx, tracks, &indexed)
		if err != nil {
			cancel()
		}
		writerDone <- err
	}()
	lastProgressAt := started
	lastProgressSeen := 0
	logProgress := func(force bool) {
		now := time.Now()
		if !force && now.Sub(lastProgressAt) < scanProgressLogEvery {
			return
		}
		elapsed := now.Sub(started)
		interval := now.Sub(lastProgressAt)
		var recentRate, averageRate float64
		if interval > 0 {
			recentRate = float64(seen-lastProgressSeen) / interval.Seconds()
		}
		if elapsed > 0 {
			averageRate = float64(seen) / elapsed.Seconds()
		}
		l.statusMu.Lock()
		l.status.DurationMS = elapsed.Milliseconds()
		l.status.AudioSeen = seen
		l.status.Parsed = atomic.LoadInt64(&parsed)
		l.status.Indexed = atomic.LoadInt64(&indexed)
		l.status.Unchanged = unchanged
		l.status.IgnoredDirs = ignoredDirs
		l.status.Skipped = atomic.LoadInt64(&skipped)
		l.status.PendingPaths = len(paths)
		l.status.PendingWrites = len(tracks)
		l.status.RemainingKnown = len(known)
		l.status.RecentTracksPerSec = recentRate
		l.status.AverageTracksPerSec = averageRate
		l.statusMu.Unlock()
		slog.Info("library scan progress",
			"duration", elapsed,
			"audio_seen", seen,
			"parsed", atomic.LoadInt64(&parsed),
			"indexed", atomic.LoadInt64(&indexed),
			"pending_paths", len(paths),
			"pending_writes", len(tracks),
			"unchanged", unchanged,
			"ignored_dirs", ignoredDirs,
			"skipped", atomic.LoadInt64(&skipped),
			"remaining_known", len(known),
			"recent_tracks_per_sec", recentRate,
			"average_tracks_per_sec", averageRate,
		)
		lastProgressAt = now
		lastProgressSeen = seen
	}
	sendPath := func(file scanFile) error {
		select {
		case paths <- file:
			return nil
		case <-scanCtx.Done():
			return scanCtx.Err()
		}
	}
	sendTrack := func(track Track) error {
		select {
		case tracks <- track:
			return nil
		case <-scanCtx.Done():
			return scanCtx.Err()
		}
	}

	var parserWG sync.WaitGroup
	parserErr := make(chan error, workers)
	for range workers {
		parserWG.Add(1)
		go func() {
			defer parserWG.Done()
			for {
				select {
				case <-scanCtx.Done():
					return
				case file, ok := <-paths:
					if !ok {
						return
					}
					track, err := readTrack(file.path, file.info)
					if err != nil {
						atomic.AddInt64(&skipped, 1)
						slog.Warn("skip unreadable audio", "path", file.path, "error", err)
						continue
					}
					atomic.AddInt64(&parsed, 1)
					if err := sendTrack(track); err != nil {
						select {
						case parserErr <- err:
						default:
						}
						cancel()
						return
					}
				}
			}
		}()
	}

	var walkErr error
	for _, root := range dirs {
		slog.Info("library scan walking directory", "path", root)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				walkFailed = true
				slog.Warn("skip path during scan", "path", path, "error", walkErr)
				return nil
			}
			if entry.IsDir() {
				if path != root && shouldIgnoreDir(entry.Name()) {
					ignoredDirs++
					return filepath.SkipDir
				}
				return nil
			}
			if !isSupportedAudio(path) {
				return nil
			}
			seen++
			info, err := entry.Info()
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				delete(known, path)
				slog.Warn("skip unreadable audio info", "path", path, "error", err)
				logProgress(false)
				return nil
			}
			modTime := info.ModTime().Unix()
			if knownFile, ok := known[path]; ok {
				delete(known, path)
				if knownFile.modTime == modTime && knownFile.propertiesVersion == trackPropertiesVersion {
					unchanged++
					logProgress(false)
					return nil
				}
			}
			if err := sendPath(scanFile{path: path, info: info}); err != nil {
				return err
			}
			logProgress(false)
			return nil
		})
		if err != nil {
			walkErr = err
			break
		}
	}
	close(paths)
	parserWG.Wait()
	select {
	case err := <-parserErr:
		if walkErr == nil {
			walkErr = err
		}
	default:
	}
	close(tracks)
	writerErr := <-writerDone
	if writerErr != nil {
		return writerErr
	}
	if walkErr != nil {
		return walkErr
	}
	logProgress(true)
	removed := 0
	if !walkFailed {
		removed = len(known)
		if removed > 0 {
			slog.Info("library scan deleting missing tracks", "tracks", removed)
		}
		if err := l.deleteMissing(ctx, known); err != nil {
			return err
		}
	}
	l.statusMu.Lock()
	l.status.Removed = removed
	l.statusMu.Unlock()
	slog.Info("library scan committed", "duration", time.Since(started), "music_dirs", len(dirs), "scan_workers", workers, "audio_seen", seen, "parsed", atomic.LoadInt64(&parsed), "indexed", atomic.LoadInt64(&indexed), "unchanged", unchanged, "ignored_dirs", ignoredDirs, "skipped", atomic.LoadInt64(&skipped), "removed", removed, "deletion_pass", !walkFailed)
	return nil
}

func readTrack(path string, info fs.FileInfo) (Track, error) {
	if info.IsDir() {
		return Track{}, errors.New("path is a directory")
	}

	title, artist := filenameFallback(path)
	t := Track{
		path:    path,
		Title:   title,
		Artist:  artist,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}

	f, err := mtag.Open(path,
		mtag.WithReadOnly(),
		mtag.WithSkipPictures(),
		mtag.WithAudioPropertiesStyle(mtag.AudioPropertiesFast),
	)
	if err != nil {
		return Track{}, err
	}
	defer f.Close()
	if value := f.Title(); value != "" {
		t.Title = value
	}
	if value := f.Artist(); value != "" {
		t.Artist = value
	}
	t.Album = f.Album()
	t.TrackNo = f.Track()
	props := f.AudioProperties()
	if props.Codec == "" {
		return Track{}, errors.New("unrecognized audio stream")
	}
	t.Lossless = losslessCodec(props.Codec)
	t.Bitrate = props.Bitrate
	normalizeTrackDisplay(&t)
	return t, nil
}

func normalizeSearch(s string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func contentKey(artist, title, album string, trackNo int) string {
	identity := strings.Join([]string{
		normalizeSearch(artist),
		normalizeSearch(title),
		normalizeSearch(album),
		strconv.Itoa(trackNo),
	}, "\x1f")
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("v1:%x", digest)
}

func losslessCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "flac", "alac", "pcm", "pcm-float", "pcm-ext":
		return true
	default:
		return false
	}
}

func fallbackTitle(path string) string {
	title, _ := filenameFallback(path)
	return title
}

func filenameFallback(path string) (title, artist string) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	name = trimTrailingBracketToken(name)
	if before, after, ok := strings.Cut(name, " - "); ok && strings.TrimSpace(after) != "" {
		artist = cleanFilenameText(before)
		title = cleanFilenameText(after)
	} else {
		title = cleanFilenameText(name)
	}
	if title == "" {
		title = fmt.Sprintf("track %s", base)
	}
	return title, artist
}

func trimTrailingBracketToken(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "]") {
		return s
	}
	open := strings.LastIndex(s, "[")
	if open <= 0 {
		return s
	}
	token := s[open+1 : len(s)-1]
	if len(token) < 6 || strings.ContainsAny(token, " \t") {
		return s
	}
	return strings.TrimSpace(s[:open])
}

func cleanFilenameText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func normalizeTrackDisplay(t *Track) {
	if t == nil || t.path == "" {
		return
	}
	title, artist := filenameFallback(t.path)
	oldTitle := fallbackTitleOld(t.path)
	if t.Title == "" || (t.Artist == "" && t.Title == oldTitle) {
		t.Title = title
		if t.Artist == "" {
			t.Artist = artist
		}
		return
	}
}

func fallbackTitleOld(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	title := strings.TrimSuffix(base, ext)
	title = strings.TrimSpace(strings.ReplaceAll(title, "_", " "))
	if title == "" {
		return fmt.Sprintf("track %s", base)
	}
	return title
}
