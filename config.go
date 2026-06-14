package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	appauth "listen-party/internal/auth"
)

type AuthConfig struct {
	PocketBase appauth.Config `json:"pocketbase"`
}

type Config struct {
	Addr         string       `json:"addr"`
	MusicDirs    []string     `json:"music_dirs"`
	DatabasePath string       `json:"database_path"`
	ScanWorkers  int          `json:"scan_workers"`
	Rooms        []RoomConfig `json:"rooms"`
	Auth         AuthConfig   `json:"auth"`
}

const (
	defaultScanWorkers = 16
	maxScanWorkers     = 256
	defaultRoomID      = "public"
)

var roomIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type RoomConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Public        bool     `json:"public"`
	AllowedRoles  []string `json:"allowed_roles,omitempty"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
}

func DefaultConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "listen-party"), nil
}

func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func ResolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return DefaultConfigPath()
}

func DefaultDatabasePath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "listen-party.sqlite"), nil
}

func DefaultMusicDir() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "music"), nil
}

func LoadConfig(path string) (Config, error) {
	var err error
	path, err = ResolveConfigPath(path)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return createDefaultConfig(path)
		}
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.ApplyDefaults(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.EnsureMusicDirs(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	path, err := ResolveConfigPath(path)
	if err != nil {
		return err
	}
	if err := cfg.ApplyDefaults(); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.EnsureMusicDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func createDefaultConfig(path string) (Config, error) {
	cfg, err := NewDefaultConfig()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.EnsureMusicDirs(); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Config{}, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func NewDefaultConfig() (Config, error) {
	configDir, err := DefaultConfigDir()
	if err != nil {
		return Config{}, err
	}
	dbPath, err := DefaultDatabasePath()
	if err != nil {
		return Config{}, err
	}
	musicDir, err := DefaultMusicDir()
	if err != nil {
		return Config{}, err
	}
	return Config{
		Addr:         "0.0.0.0:8080",
		MusicDirs:    []string{musicDir},
		DatabasePath: dbPath,
		ScanWorkers:  defaultScanWorkers,
		Rooms:        []RoomConfig{{ID: defaultRoomID, Name: "Public Room", Public: true}},
		Auth: AuthConfig{
			PocketBase: appauth.DefaultConfig(configDir),
		},
	}, nil
}

func (c Config) Validate() error {
	if len(c.MusicDirs) == 0 {
		return errors.New("music_dirs must contain at least one directory")
	}
	if c.ScanWorkers <= 0 {
		return errors.New("scan_workers must be greater than zero")
	}
	if c.ScanWorkers > maxScanWorkers {
		return fmt.Errorf("scan_workers must be %d or less", maxScanWorkers)
	}
	for _, dir := range c.MusicDirs {
		if dir == "" {
			return errors.New("music_dirs must not contain empty paths")
		}
	}
	if c.Auth.PocketBase.DataDir == "" {
		return errors.New("auth.pocketbase.data_dir is required")
	}
	if len(c.Rooms) > 0 {
		if err := validateRooms(c.Rooms); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) ApplyDefaults() error {
	if c.Addr == "" {
		c.Addr = "0.0.0.0:8080"
	}
	if c.DatabasePath == "" {
		dbPath, err := DefaultDatabasePath()
		if err != nil {
			return err
		}
		c.DatabasePath = dbPath
	}
	if c.ScanWorkers == 0 {
		c.ScanWorkers = defaultScanWorkers
	}
	if len(c.Rooms) == 0 {
		c.Rooms = []RoomConfig{{ID: defaultRoomID, Name: "Public Room", Public: true}}
	}
	for i := range c.Rooms {
		c.Rooms[i].ID = strings.TrimSpace(c.Rooms[i].ID)
		c.Rooms[i].Name = strings.TrimSpace(c.Rooms[i].Name)
		if c.Rooms[i].ID == "" && i == 0 {
			c.Rooms[i].ID = defaultRoomID
		}
		if c.Rooms[i].Name == "" {
			c.Rooms[i].Name = c.Rooms[i].ID
		}
		c.Rooms[i].AllowedRoles = normalizeConfigList(c.Rooms[i].AllowedRoles)
		c.Rooms[i].AllowedGroups = normalizeConfigList(c.Rooms[i].AllowedGroups)
	}
	if c.Auth.PocketBase.DataDir == "" {
		configDir, err := DefaultConfigDir()
		if err != nil {
			return err
		}
		c.Auth.PocketBase = appauth.DefaultConfig(configDir)
	}
	return nil
}

func validateRooms(rooms []RoomConfig) error {
	if len(rooms) == 0 {
		return errors.New("rooms must contain at least one room")
	}
	seen := make(map[string]struct{}, len(rooms))
	hasPublic := false
	reserved := []string{"admin", "api", "assets", "authAdmin", "events", "healthz", "login", "logout", "media", "rooms"}
	for _, room := range rooms {
		if !roomIDPattern.MatchString(room.ID) {
			return fmt.Errorf("room id %q must be lowercase URL-safe text", room.ID)
		}
		if slices.Contains(reserved, room.ID) {
			return fmt.Errorf("room id %q is reserved", room.ID)
		}
		if _, ok := seen[room.ID]; ok {
			return fmt.Errorf("duplicate room id %q", room.ID)
		}
		seen[room.ID] = struct{}{}
		if room.Name == "" {
			return fmt.Errorf("room %q name is required", room.ID)
		}
		if room.Public {
			hasPublic = true
		}
		for _, role := range room.AllowedRoles {
			if role != string(appauth.RoleListener) && role != string(appauth.RoleAdmin) {
				return fmt.Errorf("room %q allowed role %q must be listener or admin", room.ID, role)
			}
		}
		for _, group := range room.AllowedGroups {
			if group == "" {
				return fmt.Errorf("room %q allowed_groups must not contain empty values", room.ID)
			}
		}
	}
	if !hasPublic {
		return errors.New("rooms must contain at least one public room")
	}
	return nil
}

func normalizeConfigList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func (c Config) EnsureMusicDirs() error {
	for _, dir := range c.MusicDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create music dir %s: %w", dir, err)
		}
	}
	return nil
}
