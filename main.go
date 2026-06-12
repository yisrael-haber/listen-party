package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	musiclib "listen-party/internal/library"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to JSON config file")
	flag.Parse()

	resolvedConfigPath, err := ResolveConfigPath(configPath)
	if err != nil {
		slog.Error("resolve config path", "error", err)
		os.Exit(1)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	configDir, err := DefaultConfigDir()
	if err != nil {
		slog.Error("resolve config directory", "error", err)
		os.Exit(1)
	}
	slog.Info("listen-party config directory", "path", configDir)
	slog.Info("listen-party config loaded",
		"path", resolvedConfigPath,
		"addr", cfg.Addr,
		"database_path", cfg.DatabasePath,
		"music_dirs", len(cfg.MusicDirs),
	)

	lib, err := musiclib.Open(context.Background(), cfg.DatabasePath, cfg.MusicDirs, cfg.ScanWorkers)
	if err != nil {
		slog.Error("open library database", "error", err)
		os.Exit(1)
	}
	defer lib.Close()

	app := NewServer(ServerOptions{
		Auth:       NewBasicAuth(cfg.Auth),
		Library:    lib,
		Player:     NewPlayback("default"),
		Config:     cfg,
		ConfigPath: resolvedConfigPath,
		RoomID:     "default",
		Logger:     slog.Default(),
	})

	serverCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()

	go func() {
		scanStarted := time.Now()
		slog.Info("initial library scan started", "music_dirs", len(cfg.MusicDirs), "scan_workers", cfg.ScanWorkers)
		if err := lib.Scan(serverCtx); err != nil {
			if err == context.Canceled {
				slog.Info("initial library scan canceled", "duration", time.Since(scanStarted))
				return
			}
			slog.Warn("initial library scan failed", "duration", time.Since(scanStarted), "error", err)
			return
		}
		count, err := lib.Count(context.Background())
		if err != nil {
			slog.Warn("count library after initial scan", "duration", time.Since(scanStarted), "error", err)
			return
		}
		slog.Info("initial library scan completed", "duration", time.Since(scanStarted), "tracks", count)
	}()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return serverCtx
		},
	}

	errs := make(chan error, 1)
	go func() {
		listener, err := listenWithReuse(context.Background(), cfg.Addr)
		if err != nil {
			errs <- err
			return
		}
		slog.Info("listen-party serving", "addr", cfg.Addr)
		errs <- httpServer.Serve(listener)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("http server failed", "error", err)
			os.Exit(1)
		}
	case <-sig:
		slog.Info("shutting down")
		stopServer()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			slog.Warn("graceful shutdown timed out; closing active connections", "error", err)
			if err := httpServer.Close(); err != nil {
				slog.Error("close http server", "error", err)
				os.Exit(1)
			}
		}
	}
}

func listenWithReuse(ctx context.Context, addr string) (net.Listener, error) {
	config := net.ListenConfig{Control: setReusePort}
	return config.Listen(ctx, "tcp", addr)
}
