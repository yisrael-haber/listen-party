package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
			{ID: "main", Name: "Main Room"},
			{ID: "office", Name: "Office", Grants: map[string][]RoomPermission{
				"staff": {PermissionQueueManage},
			}},
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
	cfg.Rooms[1].Grants["staff"] = []RoomPermission{"unknown"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid room permission accepted")
	}
	cfg.Rooms[1].Grants["staff"] = []RoomPermission{PermissionQueueManage}
	cfg.Rooms[1].UserOverrides = map[string][]RoomPermission{"user-1": {"unknown"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid user override permission accepted")
	}
	cfg.Rooms[1].UserOverrides = map[string][]RoomPermission{"user-1": {}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty user override rejected: %v", err)
	}
}

func TestApplyDefaultsNormalizesRooms(t *testing.T) {
	cfg := Config{
		MusicDirs:   []string{"/music"},
		ScanWorkers: defaultScanWorkers,
		Rooms: []Room{{
			ID:          " main ",
			Name:        " Main Room ",
			AdminGroups: []string{" room-admins ", "room-admins"},
			Grants: map[string][]RoomPermission{
				" staff ": {PermissionQueueManage, PermissionQueueManage},
			},
		}},
	}
	if err := cfg.ApplyDefaults(); err != nil {
		t.Fatal(err)
	}
	if cfg.Rooms[0].ID != "main" || cfg.Rooms[0].Name != "Main Room" {
		t.Fatalf("room = %#v, want trimmed id/name", cfg.Rooms[0])
	}
	if !slices.Equal(cfg.Rooms[0].AdminGroups, []string{"room-admins"}) {
		t.Fatalf("administrator groups = %#v", cfg.Rooms[0].AdminGroups)
	}
	permissions := cfg.Rooms[0].Grants["staff"]
	if len(permissions) != 1 || permissions[0] != PermissionQueueManage {
		t.Fatalf("staff permissions = %#v, want [queue_manage]", permissions)
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
	if len(cfg.Rooms) != 1 || cfg.Rooms[0].Name != "Public Room" {
		t.Fatalf("default rooms = %#v, want Public Room", cfg.Rooms)
	}
	if got := cfg.Rooms[0].Grants[EveryoneRoomGrant]; !slices.Equal(got, roomPermissions) {
		t.Fatalf("default everyone permissions = %#v, want %#v", got, roomPermissions)
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

func TestLoadConfigMigratesDefaultRoomToEveryoneGrant(t *testing.T) {
	dir := t.TempDir()
	musicDir := filepath.Join(dir, "music")
	configPath := filepath.Join(dir, "config.json")
	data := []byte(`{
  "music_dirs": [` + strconvQuote(musicDir) + `],
  "rooms": [
    {"id": "main", "name": "Public Room", "grants": {"staff": ["queue_add"]}},
    {"id": "quiet", "name": "Quiet Room", "grants": {"staff": ["playback_control"]}}
  ]
}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != currentConfigVersion {
		t.Fatalf("config version = %d, want %d", cfg.Version, currentConfigVersion)
	}
	if got := cfg.Rooms[0].Grants[EveryoneRoomGrant]; !slices.Equal(got, roomPermissions) {
		t.Fatalf("default room everyone permissions = %#v, want %#v", got, roomPermissions)
	}
	if _, ok := cfg.Rooms[1].Grants[EveryoneRoomGrant]; ok {
		t.Fatal("restricted room received everyone grant")
	}
	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"version": 1`) || !strings.Contains(string(persisted), `"everyone"`) {
		t.Fatalf("migration was not persisted: %s", persisted)
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
