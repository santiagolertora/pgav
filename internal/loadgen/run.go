package loadgen

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

// Run waits for PostgreSQL, seeds lab tables, then generates traffic until ctx is cancelled.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	if logger == nil {
		return fmt.Errorf("loadgen: logger is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("loadgen: %w", err)
	}
	conn, err := waitForPostgres(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ConnectTimeout)
		defer cancel()
		if cerr := conn.Close(closeCtx); cerr != nil {
			logger.Warn("postgres close", "err", cerr)
		}
	}()

	if err := seed(ctx, conn, cfg, logger); err != nil {
		return err
	}
	logger.Info("lab ready; generating autovacuum traffic",
		"orders", cfg.Orders.Name,
		"sessions", cfg.Sessions.Name,
		"events", cfg.Events.Name,
		"customers", cfg.Customers.Name,
	)
	return generate(ctx, cfg, logger)
}

func waitForPostgres(ctx context.Context, cfg Config, logger *slog.Logger) (*pgx.Conn, error) {
	deadline := time.Now().Add(cfg.ReadyWait)
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("loadgen wait: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
		conn, err := connect(dialCtx, cfg, cfg.AppPrefix+"-seed")
		cancel()
		if err == nil {
			logger.Info("connected to postgres")
			return conn, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("loadgen wait: %w", last)
		}
		logger.Info("postgres not ready, retrying", "err", err)
		timer := time.NewTimer(cfg.RetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("loadgen wait: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func connect(ctx context.Context, cfg Config, appName string) (*pgx.Conn, error) {
	pgcfg, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("loadgen parse dsn: %w", err)
	}
	if pgcfg.RuntimeParams == nil {
		pgcfg.RuntimeParams = map[string]string{}
	}
	pgcfg.RuntimeParams["application_name"] = appName
	conn, err := pgx.ConnectConfig(ctx, pgcfg)
	if err != nil {
		return nil, fmt.Errorf("loadgen connect: %w", err)
	}
	return conn, nil
}

func seed(ctx context.Context, conn *pgx.Conn, cfg Config, logger *slog.Logger) error {
	tables := []TableSpec{cfg.Orders, cfg.Sessions, cfg.Events, cfg.Customers}
	for _, spec := range tables {
		if _, err := conn.Exec(ctx, createTableSQL(cfg.Schema, spec.Name)); err != nil {
			return fmt.Errorf("loadgen create %s: %w", spec.Name, err)
		}
		var existing int64
		if err := conn.QueryRow(ctx, countSQL(cfg.Schema, spec.Name)).Scan(&existing); err != nil {
			return fmt.Errorf("loadgen count %s: %w", spec.Name, err)
		}
		remaining := spec.Rows - int(existing)
		if remaining < 0 {
			remaining = 0
		}
		start := int(existing) + 1
		for _, chunk := range chunks(remaining, cfg.ChunkSize) {
			from := start + chunk[0] - 1
			to := start + chunk[1] - 1
			if _, err := conn.Exec(ctx, insertChunkSQL(cfg.Schema, spec.Name, from, to, spec.PayloadBytes)); err != nil {
				return fmt.Errorf("loadgen seed %s: %w", spec.Name, err)
			}
		}
		if _, err := conn.Exec(ctx, analyzeSQL(cfg.Schema, spec.Name)); err != nil {
			return fmt.Errorf("loadgen analyze %s: %w", spec.Name, err)
		}
		logger.Info("seeded table", "table", spec.Name, "rows", spec.Rows)
	}

	delayMs := cfg.SessionsCostDelay.Milliseconds()
	if _, err := conn.Exec(ctx, throttleSessionsSQL(cfg.Schema, cfg.Sessions.Name, cfg.SessionsScaleFactor, cfg.SessionsCostLimit, delayMs)); err != nil {
		return fmt.Errorf("loadgen throttle sessions: %w", err)
	}
	if _, err := conn.Exec(ctx, friendlyEventsSQL(cfg.Schema, cfg.Events.Name, cfg.EventsScaleFactor, cfg.EventsThreshold)); err != nil {
		return fmt.Errorf("loadgen tune events: %w", err)
	}
	return nil
}

func generate(ctx context.Context, cfg Config, logger *slog.Logger) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return updateLoop(ctx, cfg, cfg.Orders.Name, cfg.AppPrefix+"-orders", cfg.OrdersBatch, cfg.OrdersPause, logger)
	})
	g.Go(func() error {
		return updateLoop(ctx, cfg, cfg.Sessions.Name, cfg.AppPrefix+"-sessions", cfg.SessionsBatch, cfg.SessionsPause, logger)
	})
	if cfg.EventsPause > 0 && cfg.EventsBatch > 0 {
		g.Go(func() error {
			return updateLoop(ctx, cfg, cfg.Events.Name, cfg.AppPrefix+"-events", cfg.EventsBatch, cfg.EventsPause, logger)
		})
	}
	if cfg.LongXactEnabled {
		g.Go(func() error {
			return holdLongTransaction(ctx, cfg, logger)
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("loadgen traffic: %w", err)
	}
	return nil
}

func updateLoop(ctx context.Context, cfg Config, table, appName string, batch int, pause time.Duration, logger *slog.Logger) error {
	conn, err := connect(ctx, cfg, appName)
	if err != nil {
		return fmt.Errorf("loadgen connect %s: %w", table, err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ConnectTimeout)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	sql := batchUpdateSQL(cfg.Schema, table, batch)
	ticker := time.NewTicker(pause)
	defer ticker.Stop()
	for {
		if _, err := conn.Exec(ctx, sql); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("loadgen update %s: %w", table, err)
		}
		select {
		case <-ctx.Done():
			logger.Info("stopping updater", "table", table)
			return nil
		case <-ticker.C:
		}
	}
}

func holdLongTransaction(ctx context.Context, cfg Config, logger *slog.Logger) error {
	conn, err := connect(ctx, cfg, cfg.AppPrefix+"-blocker")
	if err != nil {
		return fmt.Errorf("loadgen long xact connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ConnectTimeout)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("loadgen long xact begin: %w", err)
	}
	if _, err := tx.Exec(ctx, lockOneSQL(cfg.Schema, cfg.Customers.Name)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("loadgen long xact lock: %w", err)
	}
	logger.Info("holding open transaction to block cleanup", "table", cfg.Customers.Name)
	<-ctx.Done()
	if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("loadgen long xact rollback: %w", err)
	}
	return nil
}
