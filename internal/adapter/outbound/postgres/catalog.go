// Package postgres reads autovacuum statistics from a PostgreSQL catalog.
package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/domain"
)

// Catalog is a live PostgreSQL catalog.
type Catalog struct {
	conn         *pgx.Conn
	queryTimeout time.Duration
}

// NewCatalog connects using cfg. Empty DSN falls through to libpq environment variables.
func NewCatalog(ctx context.Context, cfg config.PostgresConfig) (*Catalog, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	pgcfg, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres parse dsn: %w", err)
	}
	if pgcfg.RuntimeParams == nil {
		pgcfg.RuntimeParams = map[string]string{}
	}
	if cfg.ApplicationName != "" {
		pgcfg.RuntimeParams["application_name"] = cfg.ApplicationName
	}
	conn, err := pgx.ConnectConfig(dialCtx, pgcfg)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	return &Catalog{conn: conn, queryTimeout: cfg.QueryTimeout}, nil
}

// Close closes the connection.
func (c *Catalog) Close(ctx context.Context) error {
	if err := c.conn.Close(ctx); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}

func (c *Catalog) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.queryTimeout)
}

// ClusterSettings loads autovacuum GUCs.
func (c *Catalog) ClusterSettings(ctx context.Context) (domain.ClusterSettings, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	const q = `
SELECT
  current_setting('autovacuum'),
  current_setting('autovacuum_max_workers'),
  current_setting('autovacuum_naptime'),
  current_setting('autovacuum_freeze_max_age'),
  current_setting('autovacuum_vacuum_scale_factor'),
  current_setting('autovacuum_vacuum_threshold'),
  current_setting('autovacuum_vacuum_cost_limit'),
  current_setting('autovacuum_vacuum_cost_delay'),
  current_setting('vacuum_cost_limit'),
  current_setting('vacuum_cost_delay')`
	var (
		autovacuum, workers, nap, freeze, scale, threshold string
		avLimit, avDelay, vacLimit, vacDelay               string
	)
	err := c.conn.QueryRow(ctx, q).Scan(
		&autovacuum, &workers, &nap, &freeze, &scale, &threshold,
		&avLimit, &avDelay, &vacLimit, &vacDelay,
	)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres cluster settings: %w", err)
	}
	cs, err := parseClusterSettings(autovacuum, workers, nap, freeze, scale, threshold, avLimit, avDelay, vacLimit, vacDelay)
	if err != nil {
		return domain.ClusterSettings{}, err
	}
	return cs, nil
}

type tableRow struct {
	schema          string
	name            string
	live            int64
	dead            int64
	reltuples       float64
	upd             int64
	hot             int64
	del             int64
	lastVacuum      *time.Time
	lastAuto        *time.Time
	lastAnalyze     *time.Time
	lastAutoanalyze *time.Time
	modSince        int64
	statsReset      *time.Time
	sizeBytes       int64
	frozenAge       int64
	reloptions      []string
}

const tableSQL = `
SELECT
  n.nspname,
  c.relname,
  COALESCE(s.n_live_tup, 0),
  COALESCE(s.n_dead_tup, 0),
  COALESCE(c.reltuples, 0),
  COALESCE(s.n_tup_upd, 0),
  COALESCE(s.n_tup_hot_upd, 0),
  COALESCE(s.n_tup_del, 0),
  s.last_vacuum,
  s.last_autovacuum,
  s.last_analyze,
  s.last_autoanalyze,
  COALESCE(s.n_mod_since_analyze, 0),
  COALESCE(d.stats_reset, pg_catalog.pg_postmaster_start_time()),
  pg_catalog.pg_total_relation_size(c.oid),
  age(c.relfrozenxid),
  c.reloptions
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_stat_database d ON d.datname = current_database()
LEFT JOIN pg_catalog.pg_stat_user_tables s ON s.relid = c.oid
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
  AND n.nspname NOT LIKE 'pg_temp%'`

// TableSnapshots lists user tables.
func (c *Catalog) TableSnapshots(ctx context.Context) ([]domain.TableSnapshot, error) {
	cluster, err := c.ClusterSettings(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	rows, err := c.conn.Query(ctx, tableSQL)
	if err != nil {
		return nil, fmt.Errorf("postgres table snapshots: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := make([]domain.TableSnapshot, 0)
	for rows.Next() {
		var r tableRow
		if err := rows.Scan(
			&r.schema, &r.name, &r.live, &r.dead, &r.reltuples,
			&r.upd, &r.hot, &r.del, &r.lastVacuum, &r.lastAuto,
			&r.lastAnalyze, &r.lastAutoanalyze, &r.modSince, &r.statsReset,
			&r.sizeBytes, &r.frozenAge, &r.reloptions,
		); err != nil {
			return nil, fmt.Errorf("postgres table scan: %w", err)
		}
		out = append(out, snapshotFromRow(r, cluster, now))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres table rows: %w", err)
	}
	return out, nil
}

// TableSnapshot loads one table.
func (c *Catalog) TableSnapshot(ctx context.Context, id domain.TableID) (domain.TableSnapshot, error) {
	snaps, err := c.TableSnapshots(ctx)
	if err != nil {
		return domain.TableSnapshot{}, err
	}
	for i := range snaps {
		if snaps[i].ID == id {
			return snaps[i], nil
		}
	}
	return domain.TableSnapshot{}, fmt.Errorf("postgres table snapshot: %s not found", id.String())
}

// LongTransactions lists backends with open transactions older than olderThan.
func (c *Catalog) LongTransactions(ctx context.Context, olderThan time.Duration) ([]domain.LongTransaction, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	const q = `
SELECT pid,
       COALESCE(usename, ''),
       COALESCE(application_name, ''),
       COALESCE(state, ''),
       now() - xact_start,
       COALESCE(query, '')
FROM pg_catalog.pg_stat_activity
WHERE xact_start IS NOT NULL
  AND pid <> pg_backend_pid()
  AND now() - xact_start > $1::interval
  AND state LIKE 'idle in transaction%'`
	rows, err := c.conn.Query(ctx, q, olderThan)
	if err != nil {
		return nil, fmt.Errorf("postgres long transactions: %w", err)
	}
	defer rows.Close()
	out := make([]domain.LongTransaction, 0)
	for rows.Next() {
		var x domain.LongTransaction
		if err := rows.Scan(&x.PID, &x.User, &x.ApplicationName, &x.State, &x.Age, &x.Query); err != nil {
			return nil, fmt.Errorf("postgres long transaction scan: %w", err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres long transaction rows: %w", err)
	}
	return out, nil
}

// VacuumProgress lists autovacuum / VACUUM work currently running.
func (c *Catalog) VacuumProgress(ctx context.Context) ([]domain.VacuumProgress, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	const q = `
SELECT COALESCE(n.nspname, ''),
       COALESCE(c.relname, ''),
       COALESCE(p.phase, ''),
       COALESCE(p.heap_blks_scanned, 0),
       COALESCE(p.heap_blks_total, 0)
FROM pg_catalog.pg_stat_progress_vacuum p
JOIN pg_catalog.pg_class c ON c.oid = p.relid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace`
	rows, err := c.conn.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres vacuum progress: %w", err)
	}
	defer rows.Close()
	out := make([]domain.VacuumProgress, 0)
	for rows.Next() {
		var p domain.VacuumProgress
		if err := rows.Scan(&p.Table.Schema, &p.Table.Name, &p.Phase, &p.HeapBlksScanned, &p.HeapBlksTotal); err != nil {
			return nil, fmt.Errorf("postgres vacuum progress scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres vacuum progress rows: %w", err)
	}
	return out, nil
}

// Apply runs ALTER TABLE statements in a single transaction.
func (c *Catalog) Apply(ctx context.Context, statements []string) error {
	for _, stmt := range statements {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(stmt)), "ALTER TABLE") {
			return fmt.Errorf("postgres apply: refusing non-alter statement")
		}
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	tx, err := c.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres apply begin: %w", err)
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres apply exec: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres apply commit: %w", err)
	}
	return nil
}

func snapshotFromRow(r tableRow, cluster domain.ClusterSettings, now time.Time) domain.TableSnapshot {
	settings := cluster.Defaults
	applyReloptions(&settings, r.reloptions)
	var lastVac, lastAuto, lastAn, lastAutoAn time.Time
	if r.lastVacuum != nil {
		lastVac = r.lastVacuum.UTC()
	}
	if r.lastAuto != nil {
		lastAuto = r.lastAuto.UTC()
	}
	if r.lastAnalyze != nil {
		lastAn = r.lastAnalyze.UTC()
	}
	if r.lastAutoanalyze != nil {
		lastAutoAn = r.lastAutoanalyze.UTC()
	}
	windowStart := now
	if r.statsReset != nil && r.statsReset.Before(windowStart) {
		windowStart = r.statsReset.UTC()
	}
	snap := domain.TableSnapshot{
		ID:              domain.TableID{Schema: r.schema, Name: r.name},
		LiveTuples:      r.live,
		DeadTuples:      r.dead,
		RelTuples:       r.reltuples,
		TupUpdated:      r.upd,
		TupHotUpdated:   r.hot,
		TupDeleted:      r.del,
		LastVacuum:      lastVac,
		LastAutovacuum:  lastAuto,
		LastAnalyze:     lastAn,
		LastAutoanalyze: lastAutoAn,
		ModSinceAnalyze: r.modSince,
		SizeBytes:       r.sizeBytes,
		FrozenXIDAge:    r.frozenAge,
		Settings:        settings,
	}
	if !windowStart.IsZero() && now.After(windowStart) {
		snap.StatsWindow = now.Sub(windowStart)
	}
	return snap
}

func applyReloptions(settings *domain.VacuumSettings, opts []string) {
	for _, raw := range opts {
		key, val, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		switch key {
		case "autovacuum_vacuum_scale_factor":
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				settings.ScaleFactor = f
			}
		case "autovacuum_vacuum_threshold":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				settings.Threshold = n
			}
		case "autovacuum_vacuum_cost_limit":
			if n, err := strconv.Atoi(val); err == nil {
				settings.CostLimit = n
			}
		case "autovacuum_vacuum_cost_delay":
			if d, err := parsePGInterval(val); err == nil {
				settings.CostDelay = d
			}
		}
	}
}

func parseClusterSettings(autovacuum, workers, nap, freeze, scale, threshold, avLimit, avDelay, vacLimit, vacDelay string) (domain.ClusterSettings, error) {
	enabled := autovacuum == "on" || autovacuum == "true"
	maxWorkers, err := strconv.Atoi(workers)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres autovacuum_max_workers: %w", err)
	}
	naptime, err := parsePGInterval(nap)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres autovacuum_naptime: %w", err)
	}
	freezeAge, err := strconv.ParseInt(freeze, 10, 64)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres autovacuum_freeze_max_age: %w", err)
	}
	sf, err := strconv.ParseFloat(scale, 64)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres autovacuum_vacuum_scale_factor: %w", err)
	}
	th, err := strconv.ParseInt(threshold, 10, 64)
	if err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("postgres autovacuum_vacuum_threshold: %w", err)
	}
	limit, err := parseCostLimit(avLimit, vacLimit)
	if err != nil {
		return domain.ClusterSettings{}, err
	}
	delay, err := parseCostDelay(avDelay, vacDelay)
	if err != nil {
		return domain.ClusterSettings{}, err
	}
	return domain.ClusterSettings{
		AutovacuumEnabled: enabled,
		MaxWorkers:        maxWorkers,
		Naptime:           naptime,
		FreezeMaxAge:      freezeAge,
		Defaults: domain.VacuumSettings{
			ScaleFactor: sf,
			Threshold:   th,
			CostLimit:   limit,
			CostDelay:   delay,
		},
	}, nil
}

func parseCostLimit(av, fallback string) (int, error) {
	n, err := strconv.Atoi(av)
	if err != nil {
		return 0, fmt.Errorf("postgres autovacuum_vacuum_cost_limit: %w", err)
	}
	if n < 0 {
		n, err = strconv.Atoi(fallback)
		if err != nil {
			return 0, fmt.Errorf("postgres vacuum_cost_limit: %w", err)
		}
	}
	return n, nil
}

func parseCostDelay(av, fallback string) (time.Duration, error) {
	if av == "-1" {
		d, err := parsePGInterval(fallback)
		if err != nil {
			return 0, fmt.Errorf("postgres vacuum_cost_delay: %w", err)
		}
		return d, nil
	}
	d, err := parsePGInterval(av)
	if err != nil {
		return 0, fmt.Errorf("postgres autovacuum_vacuum_cost_delay: %w", err)
	}
	return d, nil
}

func parsePGInterval(val string) (time.Duration, error) {
	raw := strings.TrimSpace(strings.ToLower(val))
	raw = strings.ReplaceAll(raw, " ", "")
	if raw == "" {
		return 0, fmt.Errorf("parse interval: empty")
	}
	// Reloptions store cost_delay as a bare millisecond count (e.g. "100").
	if isBareNumber(raw) {
		ms, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("parse interval: %w", err)
		}
		return time.Duration(ms * float64(time.Millisecond)), nil
	}
	if strings.Contains(raw, ":") {
		d, err := parseClockInterval(raw)
		if err != nil {
			return 0, fmt.Errorf("parse interval: %w", err)
		}
		return d, nil
	}
	repl := []struct{ old, new string }{
		{"milliseconds", "ms"},
		{"millisecond", "ms"},
		{"minutes", "m"},
		{"minute", "m"},
		{"mins", "m"},
		{"min", "m"},
		{"seconds", "s"},
		{"second", "s"},
		{"hours", "h"},
		{"hour", "h"},
	}
	for _, r := range repl {
		raw = strings.ReplaceAll(raw, r.old, r.new)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse interval: %w", err)
	}
	return d, nil
}

func isBareNumber(s string) bool {
	if s == "" {
		return false
	}
	dot := 0
	for i, r := range s {
		if r == '-' && i == 0 {
			continue
		}
		if r == '.' {
			dot++
			if dot > 1 {
				return false
			}
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != "-" && s != "." && s != "-."
}

func parseClockInterval(raw string) (time.Duration, error) {
	neg := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("clock %q", raw)
	}
	h, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse interval hours: %w", err)
	}
	m, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("parse interval minutes: %w", err)
	}
	s, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("parse interval seconds: %w", err)
	}
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s*float64(time.Second))
	if neg {
		return -d, nil
	}
	return d, nil
}
