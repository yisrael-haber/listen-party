package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationLoggingWritesToStdoutAndFile(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	var stdout bytes.Buffer
	root := t.TempDir()
	file, path, err := setupApplicationLoggingIn(&stdout, root)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	slog.Info("test record", "value", 42)
	fileContents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"stdout": stdout.String(),
		"file":   string(fileContents),
	} {
		if !strings.Contains(contents, "msg=\"test record\"") || !strings.Contains(contents, "value=42") {
			t.Fatalf("%s output missing record: %q", name, contents)
		}
	}

	wantPath := filepath.Join(root, "listen-party", "logs", applicationLogName)
	if path != wantPath {
		t.Fatalf("log path = %q, want %q", path, wantPath)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("log permissions = %o, want 600", permissions)
	}
}

func TestApplicationLoggingAppendsToExistingFile(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	root := t.TempDir()
	path := filepath.Join(root, "listen-party", "logs", applicationLogName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("existing record\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	file, _, err := setupApplicationLoggingIn(&bytes.Buffer{}, root)
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("new record")
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(contents), "existing record\n") || !strings.Contains(string(contents), "msg=\"new record\"") {
		t.Fatalf("log was not appended: %q", contents)
	}
}
