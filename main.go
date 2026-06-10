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

	db, err := OpenDB(cfg.DatabasePath)
	if err != nil {
		slog.Error("open library database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	store := NewStore(db)
	if err := store.Migrate(context.Background()); err != nil {
		slog.Error("migrate library database", "error", err)
		os.Exit(1)
	}

	scanner := NewScanner(store, cfg.MusicDirs)
	if err := scanner.Scan(context.Background()); err != nil {
		slog.Warn("initial library scan failed", "error", err)
	}

	app := NewServer(ServerOptions{
		Auth:       NewBasicAuth(cfg.Auth),
		Library:    store,
		Player:     NewPlayback("default"),
		Scanner:    scanner,
		Config:     cfg,
		ConfigPath: resolvedConfigPath,
		RoomID:     "default",
		Logger:     slog.Default(),
	})

	serverCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()

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
