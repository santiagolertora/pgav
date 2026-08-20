// Package main is the pgav CLI entrypoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/santiagolertora/pgav/internal/adapter/inbound/cli"
	"github.com/santiagolertora/pgav/internal/config"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := cli.New(cli.Options{
		Version: version,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "pgav: %v\n", err)
		if errors.Is(err, config.ErrInvalid) {
			return 2
		}
		return 1
	}
	return 0
}
