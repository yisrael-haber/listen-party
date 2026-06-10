package main

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
	"time"
	"unicode"

	"github.com/dhowden/tag"
	_ "modernc.org/sqlite"
)

type Track struct {
	ID         int64     `json:"id"`
	Path       string    `json:"-"`
	Title      string    `json:"title"`
	Artist     string    `json:"artist"`
	Album      string    `json:"album"`
	TrackNo    int       `json:"track_no"`
	DurationMS int64     `json:"duration_ms"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Available  bool      `json:"available"`
}

type Store struct {
	db *sql.DB
}

func OpenDB(path string) (*sql.DB, error) {
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

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
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

func (s *Store) BeginScan(ctx context.Context) (*ScanTx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tracks SET available = 0`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return &ScanTx{tx: tx}, nil
}

type ScanTx struct {
	tx *sql.Tx
}

func (s *ScanTx) Upsert(ctx context.Context, t Track) error {
	if t.Title == "" {
		t.Title = fallbackTitle(t.Path)
	}
	search := SearchText(t)
	_, err := s.tx.ExecContext(ctx, `
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
`, t.Path, t.Title, t.Artist, t.Album, t.TrackNo, t.DurationMS, t.Size, t.ModTime.Unix(), search)
	return err
}

func (s *ScanTx) Commit() error {
	return s.tx.Commit()
}

func (s *ScanTx) Rollback() error {
	return s.tx.Rollback()
}

func (s *Store) Search(ctx context.Context, q string, limit int) ([]Track, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if strings.TrimSpace(q) == "" {
		return s.Recent(ctx, limit)
	}
	needle := "%" + strings.ReplaceAll(NormalizeSearch(q), "%", `\%`) + "%"
	rows, err := s.db.QueryContext(ctx, `
SELECT id, path, title, artist, album, track_no, duration_ms, size, mod_time, available
FROM tracks
WHERE available = 1 AND search_text LIKE ? ESCAPE '\'
ORDER BY artist, album, track_no, title
LIMIT ?`, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTracks(rows)
}

func (s *Store) Recent(ctx context.Context, limit int) ([]Track, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, `
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

func (s *Store) Count(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM tracks WHERE available = 1`).Scan(&count)
	return count, err
}

func (s *Store) Get(ctx context.Context, id int64) (Track, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, path, title, artist, album, track_no, duration_ms, size, mod_time, available
FROM tracks
WHERE id = ? AND available = 1`, id)
	return scanTrack(row)
}

func (s *Store) ListByIDs(ctx context.Context, ids []int64) (map[int64]Track, error) {
	out := make(map[int64]Track, len(ids))
	for _, id := range ids {
		t, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out[id] = t
	}
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrack(row rowScanner) (Track, error) {
	var t Track
	var unix int64
	var available int
	if err := row.Scan(&t.ID, &t.Path, &t.Title, &t.Artist, &t.Album, &t.TrackNo, &t.DurationMS, &t.Size, &unix, &available); err != nil {
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

type Scanner struct {
	mu    sync.RWMutex
	store *Store
	dirs  []string
}

func NewScanner(store *Store, dirs []string) *Scanner {
	return &Scanner{store: store, dirs: append([]string(nil), dirs...)}
}

func (s *Scanner) UpdateDirs(dirs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs = append([]string(nil), dirs...)
}

func IsMP3(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".mp3")
}

func (s *Scanner) Scan(ctx context.Context) error {
	s.mu.RLock()
	dirs := append([]string(nil), s.dirs...)
	s.mu.RUnlock()

	tx, err := s.store.BeginScan(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, root := range dirs {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				slog.Warn("skip path during scan", "path", path, "error", walkErr)
				return nil
			}
			if entry.IsDir() || !IsMP3(path) {
				return nil
			}
			track, err := readTrack(path)
			if err != nil {
				slog.Warn("skip unreadable mp3", "path", path, "error", err)
				return nil
			}
			return tx.Upsert(ctx, track)
		})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readTrack(path string) (Track, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Track{}, err
	}
	if info.IsDir() {
		return Track{}, errors.New("path is a directory")
	}

	t := Track{
		Path:    path,
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

func NormalizeSearch(s string) string {
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

func SearchText(t Track) string {
	return NormalizeSearch(strings.Join([]string{t.Title, t.Artist, t.Album}, " "))
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
