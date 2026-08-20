// Package main is the pgav-docker lab traffic generator.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/loadgen"
	"github.com/santiagolertora/pgav/internal/observability"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		if _, err := fmt.Fprintln(os.Stdout, version); err != nil {
			return 1
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadgen.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgav-docker: %v\n", err)
		return 2
	}
	if v := os.Getenv("PGAV_DSN"); v != "" {
		cfg.DSN = v
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "pgav-docker: %v\n", err)
		if errors.Is(err, loadgen.ErrInvalid) {
			return 2
		}
		return 1
	}

	logCfg := config.Defaults().Log
	logCfg.Level = "info"
	if v := os.Getenv("PGAV_LAB_LOG_LEVEL"); v != "" {
		logCfg.Level = v
	} else if v := os.Getenv("PGAV_LOG_LEVEL"); v != "" {
		logCfg.Level = v
	}
	logger := observability.NewLogger(os.Stderr, logCfg)
	logger.Info("pgav-docker starting", "version", version)

	if err := loadgen.Run(ctx, cfg, logger); err != nil {
		fmt.Fprintf(os.Stderr, "pgav-docker: %v\n", err)
		return 1
	}
	return 0
}
