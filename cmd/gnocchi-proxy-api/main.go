package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/catalog"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/keystone"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/prom"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	promClient, err := prom.New(cfg.Prometheus.BaseURL, cfg.Prometheus.QueryTimeout, cfg.Prometheus.Headers, cfg.Keystone.InsecureSkipVerify)
	if err != nil {
		logger.Error("failed to initialize prometheus client", "error", err)
		os.Exit(1)
	}

	authClient, err := keystone.New(cfg.Keystone)
	if err != nil {
		logger.Error("failed to initialize keystone client", "error", err)
		os.Exit(1)
	}

	catalogManager := catalog.NewManager(cfg, promClient)
	app := server.New(cfg, logger, authClient, promClient, catalogManager)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go catalogManager.Start(ctx)
	if err := catalogManager.Refresh(ctx); err != nil {
		logger.Warn("initial catalog refresh failed", "error", err)
	}

	httpServer := &http.Server{
		Addr:         cfg.Server.Address,
		Handler:      app.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		logger.Info("starting server", "address", cfg.Server.Address)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server exited", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}

	time.Sleep(100 * time.Millisecond)
}
