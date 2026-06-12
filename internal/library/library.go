package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/dhowden/tag"
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
	Available  bool      `json:"available"`
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
	return m.Track.Title + ".mp3"
}

func (m *Media) ModTime() time.Time {
	return m.Track.ModTime
}

type Library struct {
	mu       sync.RWMutex
	scanMu   sync.Mutex
	statusMu sync.RWMutex
	db       *sql.DB
	dirs     []string
	workers  int
	status   ScanStatus
}

type ScanStatus struct {
	Scanning            bool      `json:"scanning"`
	LastStarted         time.Time `json:"last_started"`
	LastCompleted       time.Time `json:"last_completed"`
	LastError           string    `json:"last_error"`
	DurationMS          int64     `json:"duration_ms"`
	MP3Seen             int       `json:"mp3_seen"`
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
	ErrTrackNotFound  = errors.New("track not found")
	ErrScanInProgress = errors.New("library scan already in progress")
)

const (
	defaultTrackQueryLimit = 25
	maxTrackQueryLimit     = 100
	scanWriteBatchSize     = 1000
	scanWriteDrainMax      = 4000
	scanPathBufferSize     = 4096
	scanMaxWorkers         = 256
	scanProgressLogEvery   = 5 * time.Second
)

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
	_, err := l.db.ExecContext(ctx, `
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
	search_text TEXT NOT NULL,
	available INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS tracks_search_idx ON tracks(search_text);
CREATE INDEX IF NOT EXISTS tracks_available_idx ON tracks(available);
`)
	return err
}

func (l *Library) loadKnownTracks(ctx context.Context) (map[string]int64, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT path, mod_time FROM tracks WHERE available = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	known := make(map[string]int64)
	for rows.Next() {
		var path string
		var modTime int64
		if err := rows.Scan(&path, &modTime); err != nil {
			return nil, err
		}
		known[path] = modTime
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return known, nil
}

const upsertTrackSQL = `
INSERT INTO tracks(path, title, artist, album, track_no, duration_ms, size, mod_time, search_text, available)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(path) DO UPDATE SET
	title = excluded.title,
	artist = excluded.artist,
	album = excluded.album,
	track_no = excluded.track_no,
	duration_ms = excluded.duration_ms,
	size = excluded.size,
	mod_time = excluded.mod_time,
	search_text = excluded.search_text,
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
		if t.Title == "" {
			t.Title = fallbackTitle(t.path)
		}
		search := searchText(t)
		if _, err := stmt.ExecContext(ctx, t.path, t.Title, t.Artist, t.Album, t.TrackNo, t.DurationMS, t.Size, t.ModTime.Unix(), search); err != nil {
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

func (l *Library) deleteMissing(ctx context.Context, paths map[string]int64) error {
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
	limit := maxTrackQueryLimit
	if strings.TrimSpace(q) == "" {
		return l.recent(ctx, limit)
	}
	needle := "%" + strings.ReplaceAll(normalizeSearch(q), "%", `\%`) + "%"
	rows, err := l.db.QueryContext(ctx, `
SELECT id, path, title, artist, album, track_no, duration_ms, size, mod_time, available
FROM tracks
WHERE available = 1 AND search_text LIKE ? ESCAPE '\'
ORDER BY title COLLATE NOCASE ASC, artist COLLATE NOCASE ASC, album COLLATE NOCASE ASC, track_no ASC
LIMIT ?`, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

func (l *Library) recent(ctx context.Context, limit int) ([]Track, error) {
	limit = clampTrackQueryLimit(limit)
	rows, err := l.db.QueryContext(ctx, `
SELECT id, path, title, artist, album, track_no, duration_ms, size, mod_time, available
FROM tracks
WHERE available = 1
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

func (l *Library) Get(ctx context.Context, id int64) (Track, error) {
	row := l.db.QueryRowContext(ctx, `
SELECT id, path, title, artist, album, track_no, duration_ms, size, mod_time, available
FROM tracks
WHERE id = ? AND available = 1`, id)
	track, err := scanTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, ErrTrackNotFound
	}
	return track, err
}

func (l *Library) ListByIDs(ctx context.Context, ids []int64) (map[int64]Track, error) {
	out := make(map[int64]Track, len(ids))
	for _, id := range ids {
		t, err := l.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrTrackNotFound) {
				continue
			}
			return nil, err
		}
		out[id] = t
	}
	return out, nil
}

func (l *Library) OpenMedia(ctx context.Context, id int64) (*Media, error) {
	track, err := l.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(track.path)
	if err != nil {
		return nil, err
	}
	return &Media{Track: track, file: file}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrack(row rowScanner) (Track, error) {
	var t Track
	var unix int64
	var available int
	if err := row.Scan(&t.ID, &t.path, &t.Title, &t.Artist, &t.Album, &t.TrackNo, &t.DurationMS, &t.Size, &unix, &available); err != nil {
		return Track{}, err
	}
	t.ModTime = time.Unix(unix, 0)
	t.Available = available == 1
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

func isMP3(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".mp3")
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
	if !l.scanMu.TryLock() {
		return ErrScanInProgress
	}
	defer l.scanMu.Unlock()

	l.mu.RLock()
	dirs := append([]string(nil), l.dirs...)
	workers := l.workers
	l.mu.RUnlock()
	started := time.Now()
	l.statusMu.Lock()
	l.status = ScanStatus{
		Scanning:    true,
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

	known, err := l.loadKnownTracks(ctx)
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
		l.status.MP3Seen = seen
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
			"mp3_seen", seen,
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
						slog.Warn("skip unreadable mp3", "path", file.path, "error", err)
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
			if !isMP3(path) {
				return nil
			}
			seen++
			info, err := entry.Info()
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				delete(known, path)
				slog.Warn("skip unreadable mp3 info", "path", path, "error", err)
				logProgress(false)
				return nil
			}
			modTime := info.ModTime().Unix()
			if knownModTime, ok := known[path]; ok {
				delete(known, path)
				if knownModTime == modTime {
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
	slog.Info("library scan committed", "duration", time.Since(started), "music_dirs", len(dirs), "scan_workers", workers, "mp3_seen", seen, "parsed", atomic.LoadInt64(&parsed), "indexed", atomic.LoadInt64(&indexed), "unchanged", unchanged, "ignored_dirs", ignoredDirs, "skipped", atomic.LoadInt64(&skipped), "removed", removed, "deletion_pass", !walkFailed)
	return nil
}

func readTrack(path string, info fs.FileInfo) (Track, error) {
	if info.IsDir() {
		return Track{}, errors.New("path is a directory")
	}

	t := Track{
		path:    path,
		Title:   fallbackTitle(path),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}

	f, err := os.Open(path)
	if err != nil {
		return Track{}, err
	}
	defer f.Close()

	meta, err := tag.ReadFrom(f)
	if err == nil {
		if meta.Title() != "" {
			t.Title = meta.Title()
		}
		t.Artist = meta.Artist()
		t.Album = meta.Album()
		t.TrackNo, _ = meta.Track()
	}
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

func searchText(t Track) string {
	return normalizeSearch(strings.Join([]string{t.Title, t.Artist, t.Album}, " "))
}

func fallbackTitle(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	title := strings.TrimSuffix(base, ext)
	title = strings.TrimSpace(strings.ReplaceAll(title, "_", " "))
	if title == "" {
		return fmt.Sprintf("track %s", base)
	}
	return title
}
