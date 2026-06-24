package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const applicationLogName = "listen-party.log"

func setupApplicationLogging(stdout io.Writer) (*os.File, string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return nil, "", err
	}
	return setupApplicationLoggingIn(stdout, userConfigDir)
}

func setupApplicationLoggingIn(stdout io.Writer, userConfigDir string) (*os.File, string, error) {
	logDir := filepath.Join(userConfigDir, "listen-party", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(logDir, applicationLogName)
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}

	output := io.MultiWriter(stdout, logFile)
	slog.SetDefault(slog.New(slog.NewTextHandler(output, nil)))
	return logFile, logPath, nil
}
