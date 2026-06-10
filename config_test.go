package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRequiresUsefulConfig(t *testing.T) {
	cfg := Config{
		MusicDirs: []string{"/music"},
		Auth: AuthConfig{
			Listener: Credentials{Username: "listener", Password: "listen"},
			Admin:    Credentials{Username: "admin", Password: "admin"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cfg.Auth.Admin.Password = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("missing admin password accepted")
	}
}

func TestLoadConfigCreatesDefaultConfigAndMusicDir(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path := filepath.Join(configHome, "listen-party", "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	wantMusic := filepath.Join(configHome, "listen-party", "music")
	if cfg.Addr != "0.0.0.0:8080" {
		t.Fatalf("Addr = %q, want 0.0.0.0:8080", cfg.Addr)
	}
	if len(cfg.MusicDirs) != 1 || cfg.MusicDirs[0] != wantMusic {
		t.Fatalf("MusicDirs = %#v, want [%q]", cfg.MusicDirs, wantMusic)
	}
	if cfg.Auth.Listener != (Credentials{Username: "default", Password: "default"}) {
		t.Fatalf("Listener creds = %#v, want default/default", cfg.Auth.Listener)
	}
	if cfg.Auth.Admin != (Credentials{Username: "admin", Password: "admin"}) {
		t.Fatalf("Admin creds = %#v, want admin/admin", cfg.Auth.Admin)
	}
	if info, err := os.Stat(wantMusic); err != nil || !info.IsDir() {
		t.Fatalf("music dir was not created: info=%v err=%v", info, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default config was not written: %v", err)
	}
	var fromDisk Config
	if err := json.Unmarshal(data, &fromDisk); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}
	if fromDisk.DatabasePath == "" {
		t.Fatal("default config missing database_path")
	}
}

func TestLoadConfigCreatesConfiguredMusicDirs(t *testing.T) {
	dir := t.TempDir()
	musicDir := filepath.Join(dir, "nested", "music")
	configPath := filepath.Join(dir, "config.json")
	data := []byte(`{
  "addr": "127.0.0.1:9999",
  "music_dirs": [` + strconvQuote(musicDir) + `],
  "auth": {
    "listener": {"username": "a", "password": "b"},
    "admin": {"username": "c", "password": "d"}
  }
}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(configPath); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if info, err := os.Stat(musicDir); err != nil || !info.IsDir() {
		t.Fatalf("configured music dir was not created: info=%v err=%v", info, err)
	}
}

func TestSaveConfigWritesConfigAndCreatesMusicDirs(t *testing.T) {
	dir := t.TempDir()
	musicDir := filepath.Join(dir, "music")
	configPath := filepath.Join(dir, "config.json")
	cfg := Config{
		Addr:         "127.0.0.1:7777",
		MusicDirs:    []string{musicDir},
		DatabasePath: filepath.Join(dir, "db.sqlite"),
		Auth: AuthConfig{
			Listener: Credentials{Username: "listener", Password: "listen"},
			Admin:    Credentials{Username: "admin", Password: "admin"},
		},
	}

	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if info, err := os.Stat(musicDir); err != nil || !info.IsDir() {
		t.Fatalf("music dir was not created: info=%v err=%v", info, err)
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.Addr != cfg.Addr || loaded.DatabasePath != cfg.DatabasePath {
		t.Fatalf("loaded config = %#v, want %#v", loaded, cfg)
	}
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
