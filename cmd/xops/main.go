package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/app"
	"github.com/YuanJey/go-x-ai-operations/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("xops stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || os.Args[1] != "server" {
		return fmt.Errorf("usage: xops server [-config path]")
	}
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	configPath := flags.String("config", "./config/default.yaml", "path to runtime configuration")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	application, err := app.Build(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer application.Close()
	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           application.HTTP.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Server.Listen, err)
	}
	defer listener.Close()
	application.RunBackground(ctx)
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("server listening",
			"address", "http://"+cfg.Server.Listen,
			"dry_run", cfg.Runtime.DryRun,
		)
		errorValue := server.Serve(listener)
		if errorValue != nil && !errors.Is(errorValue, http.ErrServerClosed) {
			errorsChannel <- errorValue
		}
		close(errorsChannel)
	}()

	select {
	case <-ctx.Done():
	case serverErr := <-errorsChannel:
		if serverErr != nil {
			return serverErr
		}
	}
	shutdownContext, shutdownCancel := context.WithTimeout(
		context.Background(), cfg.Server.ShutdownTimeout.Value(),
	)
	defer shutdownCancel()
	return server.Shutdown(shutdownContext)
}
