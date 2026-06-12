package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthConfig struct {
	Listener Credentials `json:"listener"`
	Admin    Credentials `json:"admin"`
}

type Config struct {
	Addr         string     `json:"addr"`
	MusicDirs    []string   `json:"music_dirs"`
	DatabasePath string     `json:"database_path"`
	ScanWorkers  int        `json:"scan_workers"`
	Auth         AuthConfig `json:"auth"`
}

const (
	defaultScanWorkers = 16
	maxScanWorkers     = 256
)

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
	dbPath, err := DefaultDatabasePath()
	if err != nil {
		return Config{}, err
	}
	musicDir, err := DefaultMusicDir()
	if err != nil {
		return Config{}, err
	}
	defaultCreds := Credentials{Username: "default", Password: "default"}
	adminCreds := Credentials{Username: "admin", Password: "admin"}
	return Config{
		Addr:         "0.0.0.0:8080",
		MusicDirs:    []string{musicDir},
		DatabasePath: dbPath,
		ScanWorkers:  defaultScanWorkers,
		Auth: AuthConfig{
			Listener: defaultCreds,
			Admin:    adminCreds,
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
	if err := validateCreds("auth.listener", c.Auth.Listener); err != nil {
		return err
	}
	if err := validateCreds("auth.admin", c.Auth.Admin); err != nil {
		return err
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
	return nil
}

func (c Config) EnsureMusicDirs() error {
	for _, dir := range c.MusicDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create music dir %s: %w", dir, err)
		}
	}
	return nil
}

func validateCreds(name string, creds Credentials) error {
	if creds.Username == "" || creds.Password == "" {
		return fmt.Errorf("%s username and password are required", name)
	}
	return nil
}
