package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultsValidate(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	require.NoError(t, cfg.Validate())
}

func TestValidateRejectsBadLevel(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Log.Level = "verbose"
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrInvalid)
}

func TestValidateRejectsBadFormatAndAssess(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Log.Format = "xml"
	cfg.Assess.HighDeadRatio = 2
	cfg.Assess.IOMultiplier = -1
	cfg.Tuner.MinScaleFactor = 0
	cfg.Doctor.LongXactAfter = 0
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "log.format")
	require.ErrorContains(t, err, "high_dead_ratio")
	require.ErrorContains(t, err, "io_multiplier")
	require.ErrorContains(t, err, "min_scale_factor")
	require.ErrorContains(t, err, "long_xact_after")
}

func TestLoadYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgav.yaml")
	raw := []byte(`
postgres:
  dsn: postgres://file
  connect_timeout: 3s
  query_timeout: 8s
log:
  level: debug
  format: json
assess:
  high_dead_ratio: 0.15
  critical_dead_ratio: 0.5
  high_hours_to_trigger: 6
  wraparound_warning_ratio: 0.7
  wraparound_critical_ratio: 0.9
  assumed_cost_per_page: 8
  assumed_tuples_per_page: 40
  io_multiplier: 2
  min_trigger_for_scale_warning: 500000
tuner:
  max_hours_between_vacuum: 3
  min_scale_factor: 0.002
  max_scale_factor: 0.1
  min_threshold: 5000
  max_threshold: 50000
  cost_limit_bump: 500
  max_cost_limit: 4000
  scale_decimals: 2
doctor:
  max_score: 90
  min_score: 1
  critical_penalty: 10
  high_penalty: 7
  warning_penalty: 2
  long_xact_penalty: 4
  wraparound_penalty: 12
  long_xact_after: 30m
`)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	t.Setenv("PGAV_DSN", "postgres://env")
	t.Setenv("PGAV_APPLICATION_NAME", "pgav-test")
	t.Setenv("PGAV_LOG_LEVEL", "warn")
	t.Setenv("PGAV_LOG_FORMAT", "text")
	t.Setenv("PGAV_CONNECT_TIMEOUT", "9s")
	t.Setenv("PGAV_QUERY_TIMEOUT", "11s")
	t.Setenv("PGAV_LONG_XACT_AFTER", "45m")
	t.Setenv("PGAV_MAX_HOURS_BETWEEN_VACUUM", "5")

	cfg, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "postgres://env", cfg.Postgres.DSN)
	require.Equal(t, "pgav-test", cfg.Postgres.ApplicationName)
	require.Equal(t, "warn", cfg.Log.Level)
	require.Equal(t, "text", cfg.Log.Format)
	require.Equal(t, 9*time.Second, cfg.Postgres.ConnectTimeout)
	require.Equal(t, 11*time.Second, cfg.Postgres.QueryTimeout)
	require.Equal(t, 45*time.Minute, cfg.Doctor.LongXactAfter)
	require.Equal(t, 5.0, cfg.Tuner.MaxHoursBetweenVacuum)
	require.Equal(t, 0.15, cfg.Assess.HighDeadRatio)
	require.Equal(t, 0.002, cfg.Tuner.MinScaleFactor)
	require.Equal(t, 10, cfg.Doctor.CriticalPenalty)
	require.NoError(t, cfg.Validate())
}

func TestLoadInvalidYAML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("postgres: ["), 0o600))
	_, err := Load(t.Context(), path)
	require.Error(t, err)
}

func TestLoadInvalidDurations(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("postgres:\n  connect_timeout: nope\n"), 0o600))
	_, err := Load(t.Context(), path)
	require.Error(t, err)

	path2 := filepath.Join(t.TempDir(), "bad2.yaml")
	require.NoError(t, os.WriteFile(path2, []byte("postgres:\n  query_timeout: nope\n"), 0o600))
	_, err = Load(t.Context(), path2)
	require.Error(t, err)

	path3 := filepath.Join(t.TempDir(), "bad3.yaml")
	require.NoError(t, os.WriteFile(path3, []byte("doctor:\n  long_xact_after: nope\n"), 0o600))
	_, err = Load(t.Context(), path3)
	require.Error(t, err)
}

func TestApplyEnvInvalid(t *testing.T) {
	t.Setenv("PGAV_CONNECT_TIMEOUT", "nope")
	_, err := Load(t.Context(), "")
	require.Error(t, err)

	t.Setenv("PGAV_CONNECT_TIMEOUT", "")
	t.Setenv("PGAV_QUERY_TIMEOUT", "nope")
	_, err = Load(t.Context(), "")
	require.Error(t, err)

	t.Setenv("PGAV_QUERY_TIMEOUT", "")
	t.Setenv("PGAV_LONG_XACT_AFTER", "nope")
	_, err = Load(t.Context(), "")
	require.Error(t, err)

	t.Setenv("PGAV_LONG_XACT_AFTER", "")
	t.Setenv("PGAV_MAX_HOURS_BETWEEN_VACUUM", "nope")
	_, err = Load(t.Context(), "")
	require.Error(t, err)
}

func TestLoadCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Load(ctx, "")
	require.Error(t, err)
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(t.Context(), filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
}

func TestValidateJoinMultiple(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Postgres.ConnectTimeout = 0
	cfg.Postgres.QueryTimeout = 0
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "connect_timeout")
	require.ErrorContains(t, err, "query_timeout")
}

func TestValidateCriticalDeadRatioAndWraparound(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Assess.CriticalDeadRatio = 0.1
	cfg.Assess.WraparoundCriticalRatio = 0.1
	cfg.Assess.AssumedCostPerPage = 0
	cfg.Assess.AssumedTuplesPerPage = 0
	cfg.Assess.MinTriggerForScaleWarning = 0
	cfg.Tuner.MaxHoursBetweenVacuum = 0
	cfg.Tuner.CostLimitBump = 0
	cfg.Tuner.MaxCostLimit = 0
	cfg.Tuner.ScaleDecimals = -1
	cfg.Doctor.MaxScore = 0
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrInvalid)
}
