// Package cli is the Cobra command tree for pgav.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/santiagolertora/pgav/internal/adapter/outbound/postgres"
	"github.com/santiagolertora/pgav/internal/app"
	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/observability"
)

// Options wires IO and catalog construction.
type Options struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
	Open    func(ctx context.Context, cfg config.Config) (app.Catalog, error)
}

type runtime struct {
	opts       Options
	configPath string
	dsn        string
	logLevel   string
}

// New builds the root command.
func New(opts Options) *cobra.Command {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.Open == nil {
		opts.Open = openPostgres
	}
	rt := &runtime{opts: opts}

	root := &cobra.Command{
		Use:           "pgav",
		Short:         "PostgreSQL autovacuum advisor and controller",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&rt.configPath, "config", "", "path to YAML config file")
	root.PersistentFlags().StringVar(&rt.dsn, "dsn", "", "PostgreSQL DSN (overrides PGAV_DSN)")
	root.PersistentFlags().StringVar(&rt.logLevel, "log-level", "", "log level: debug, info, warn, error")

	root.AddCommand(newVersionCmd(opts.Version, opts.Stdout))
	root.AddCommand(newStatusCmd(rt))
	root.AddCommand(newAnalyzeCmd(rt))
	root.AddCommand(newTuneCmd(rt))
	root.AddCommand(newDoctorCmd(rt))
	return root
}

func openPostgres(ctx context.Context, cfg config.Config) (app.Catalog, error) {
	cat, err := postgres.NewCatalog(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("cli open catalog: %w", err)
	}
	return cat, nil
}

func (rt *runtime) prepare(cmd *cobra.Command) (*app.Service, func(), error) {
	ctx := cmd.Context()
	cfg, err := config.Load(ctx, rt.configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("cli load config: %w", err)
	}
	if cmd.Flags().Changed("dsn") || rt.dsn != "" {
		cfg.Postgres.DSN = rt.dsn
	}
	if rt.logLevel != "" {
		cfg.Log.Level = rt.logLevel
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("cli validate config: %w", err)
	}
	logger := observability.NewLogger(rt.opts.Stderr, cfg.Log)
	cat, err := rt.opts.Open(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("cli open catalog: %w", err)
	}
	svc, err := app.New(cat, cfg, logger)
	if err != nil {
		_ = cat.Close(ctx)
		return nil, nil, fmt.Errorf("cli service: %w", err)
	}
	cleanup := func() {
		if cerr := cat.Close(ctx); cerr != nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "catalog close", slog.Any("err", cerr))
		}
	}
	return svc, cleanup, nil
}

func newVersionCmd(version string, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(out, version)
			if err != nil {
				return fmt.Errorf("cli version: %w", err)
			}
			return nil
		},
	}
}
