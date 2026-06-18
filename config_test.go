package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRequiresUsefulConfig(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateScanWorkers(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
	}
	cfg.ScanWorkers = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero scan_workers accepted")
	}
	cfg.ScanWorkers = maxScanWorkers + 1
	if err := cfg.Validate(); err == nil {
		t.Fatal("too many scan_workers accepted")
	}
}

func TestValidateBannedIPs(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
		BannedIPs:   []string{"192.168.1.50", "::1"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid banned_ips rejected: %v", err)
	}
	cfg.BannedIPs = []string{"not-an-ip"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid banned ip accepted")
	}
}

func TestValidateRooms(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
		Rooms: []Room{
			{ID: "public", Name: "Public Room", Public: true},
			{ID: "office", Name: "Office", AllowedGroups: []string{"staff"}, AllowedRoles: []string{"listener"}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid rooms rejected: %v", err)
	}

	cfg.Rooms[1].ID = "Bad Room"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid room id accepted")
	}
	cfg.Rooms[1].ID = "office"
	cfg.Rooms[1].AllowedRoles = []string{"owner"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid room role accepted")
	}
}

func TestApplyDefaultsNormalizesRooms(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
		Rooms: []Room{{
			ID:            " public ",
			Name:          " Public Room ",
			Public:        true,
			AllowedGroups: []string{" staff ", "staff", ""},
		}},
	}
	if err := cfg.ApplyDefaults(); err != nil {
		t.Fatal(err)
	}
	if cfg.Rooms[0].ID != "public" || cfg.Rooms[0].Name != "Public Room" {
		t.Fatalf("room = %#v, want trimmed id/name", cfg.Rooms[0])
	}
	if len(cfg.Rooms[0].AllowedGroups) != 1 || cfg.Rooms[0].AllowedGroups[0] != "staff" {
		t.Fatalf("allowed groups = %#v, want [staff]", cfg.Rooms[0].AllowedGroups)
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
	wantAuthDir := filepath.Join(configHome, "listen-party", "auth")
	if cfg.Auth.PocketBase.DataDir != wantAuthDir {
		t.Fatalf("auth data dir = %q, want %q", cfg.Auth.PocketBase.DataDir, wantAuthDir)
	}
	if cfg.ScanWorkers != defaultScanWorkers {
		t.Fatalf("ScanWorkers = %d, want %d", cfg.ScanWorkers, defaultScanWorkers)
	}
	if info, err := os.Stat(wantMusic); err != nil || !info.IsDir() {
		t.Fatalf("music dir was not created: info=%v err=%v", info, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default config was not written: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("default config is not valid JSON: %v", err)
	}
	if _, ok := raw["database_path"]; ok {
		t.Fatal("default config should not persist database_path")
	}
	auth, ok := raw["auth"].(map[string]any)
	if !ok {
		t.Fatal("default config missing auth")
	}
	pocketbase, ok := auth["pocketbase"].(map[string]any)
	if !ok {
		t.Fatal("default config missing auth.pocketbase")
	}
	for _, key := range []string{"data_dir", "bootstrap_admin_email"} {
		if _, ok := pocketbase[key]; ok {
			t.Fatalf("default config should not persist auth.pocketbase.%s", key)
		}
	}
}

func TestLoadConfigCreatesConfiguredMusicDirs(t *testing.T) {
	dir := t.TempDir()
	musicDir := filepath.Join(dir, "nested", "music")
	configPath := filepath.Join(dir, "config.json")
	data := []byte(`{
	  "addr": "127.0.0.1:9999",
	  "music_dirs": [` + strconvQuote(musicDir) + `]
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
		Addr:        "127.0.0.1:7777",
		MusicDirs:   []string{musicDir},
		ScanWorkers: 4,
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
	if loaded.Addr != cfg.Addr || loaded.DatabasePath != filepath.Join(dir, "listen-party.sqlite") {
		t.Fatalf("loaded config = %#v, want %#v", loaded, cfg)
	}
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
