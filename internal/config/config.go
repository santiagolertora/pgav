// Package config loads and validates process configuration from defaults, YAML, env, and flags.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrInvalid is returned when Validate fails.
var ErrInvalid = errors.New("config: invalid")

// Config is the process configuration. Defaults live in Defaults(); callers pass this value around.
type Config struct {
	Postgres PostgresConfig `yaml:"postgres"`
	Log      LogConfig      `yaml:"log"`
	Assess   AssessConfig   `yaml:"assess"`
	Tuner    TunerConfig    `yaml:"tuner"`
	Doctor   DoctorConfig   `yaml:"doctor"`
}

// PostgresConfig controls catalog access.
type PostgresConfig struct {
	DSN             string        `yaml:"dsn"`
	ApplicationName string        `yaml:"application_name"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout"`
	QueryTimeout    time.Duration `yaml:"query_timeout"`
}

// LogConfig controls slog.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// AssessConfig holds risk and capacity-model knobs.
type AssessConfig struct {
	HighDeadRatio             float64 `yaml:"high_dead_ratio"`
	CriticalDeadRatio         float64 `yaml:"critical_dead_ratio"`
	HighHoursToTrigger        float64 `yaml:"high_hours_to_trigger"`
	WraparoundWarningRatio    float64 `yaml:"wraparound_warning_ratio"`
	WraparoundCriticalRatio   float64 `yaml:"wraparound_critical_ratio"`
	AssumedCostPerPage        float64 `yaml:"assumed_cost_per_page"`
	AssumedTuplesPerPage      float64 `yaml:"assumed_tuples_per_page"`
	IOMultiplier              float64 `yaml:"io_multiplier"`
	MinTriggerForScaleWarning int64   `yaml:"min_trigger_for_scale_warning"`
}

// TunerConfig holds recommendation bounds.
type TunerConfig struct {
	MaxHoursBetweenVacuum float64 `yaml:"max_hours_between_vacuum"`
	MinScaleFactor        float64 `yaml:"min_scale_factor"`
	MaxScaleFactor        float64 `yaml:"max_scale_factor"`
	MinThreshold          int64   `yaml:"min_threshold"`
	MaxThreshold          int64   `yaml:"max_threshold"`
	CostLimitBump         int     `yaml:"cost_limit_bump"`
	MinCostLimit          int     `yaml:"min_cost_limit"`
	MaxCostLimit          int     `yaml:"max_cost_limit"`
	CostLimitHeadroom     float64 `yaml:"cost_limit_headroom"`
	ScaleDecimals         int     `yaml:"scale_decimals"`
}

// DoctorConfig holds health-score penalties and long-transaction detection.
type DoctorConfig struct {
	MaxScore          int           `yaml:"max_score"`
	MinScore          int           `yaml:"min_score"`
	CriticalPenalty   int           `yaml:"critical_penalty"`
	HighPenalty       int           `yaml:"high_penalty"`
	WarningPenalty    int           `yaml:"warning_penalty"`
	LongXactPenalty   int           `yaml:"long_xact_penalty"`
	WraparoundPenalty int           `yaml:"wraparound_penalty"`
	LongXactAfter     time.Duration `yaml:"long_xact_after"`
}

type fileConfig struct {
	Postgres postgresFile `yaml:"postgres"`
	Log      LogConfig    `yaml:"log"`
	Assess   AssessConfig `yaml:"assess"`
	Tuner    TunerConfig  `yaml:"tuner"`
	Doctor   doctorFile   `yaml:"doctor"`
}

type postgresFile struct {
	DSN             string `yaml:"dsn"`
	ApplicationName string `yaml:"application_name"`
	ConnectTimeout  string `yaml:"connect_timeout"`
	QueryTimeout    string `yaml:"query_timeout"`
}

type doctorFile struct {
	MaxScore          int    `yaml:"max_score"`
	MinScore          int    `yaml:"min_score"`
	CriticalPenalty   int    `yaml:"critical_penalty"`
	HighPenalty       int    `yaml:"high_penalty"`
	WarningPenalty    int    `yaml:"warning_penalty"`
	LongXactPenalty   int    `yaml:"long_xact_penalty"`
	WraparoundPenalty int    `yaml:"wraparound_penalty"`
	LongXactAfter     string `yaml:"long_xact_after"`
}

// Defaults returns the baseline configuration. This is the only place default values are defined.
func Defaults() Config {
	return Config{
		Postgres: PostgresConfig{
			DSN:             "",
			ApplicationName: "pgav",
			ConnectTimeout:  5 * time.Second,
			QueryTimeout:    15 * time.Second,
		},
		Log: LogConfig{
			Level:  "warn",
			Format: "text",
		},
		Assess: AssessConfig{
			HighDeadRatio:             0.20,
			CriticalDeadRatio:         0.40,
			HighHoursToTrigger:        8,
			WraparoundWarningRatio:    0.80,
			WraparoundCriticalRatio:   0.95,
			AssumedCostPerPage:        10,
			AssumedTuplesPerPage:      50,
			IOMultiplier:              1,
			MinTriggerForScaleWarning: 1_000_000,
		},
		Tuner: TunerConfig{
			MaxHoursBetweenVacuum: 4,
			MinScaleFactor:        0.001,
			MaxScaleFactor:        0.20,
			MinThreshold:          10000,
			MaxThreshold:          100000,
			CostLimitBump:         1800,
			MinCostLimit:          200,
			MaxCostLimit:          10000,
			CostLimitHeadroom:     1.25,
			ScaleDecimals:         3,
		},
		Doctor: DoctorConfig{
			MaxScore:          100,
			MinScore:          0,
			CriticalPenalty:   15,
			HighPenalty:       8,
			WarningPenalty:    3,
			LongXactPenalty:   5,
			WraparoundPenalty: 20,
			LongXactAfter:     time.Hour,
		},
	}
}

// Load merges defaults, an optional YAML file, and environment variables.
func Load(ctx context.Context, configPath string) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("config load: %w", err)
	}
	cfg := Defaults()
	if configPath != "" {
		raw, err := os.ReadFile(configPath) //nolint:gosec // path is an explicit operator-supplied flag
		if err != nil {
			return Config{}, fmt.Errorf("config read: %w", err)
		}
		var parsed fileConfig
		if err := yaml.Unmarshal(raw, &parsed); err != nil {
			return Config{}, fmt.Errorf("config parse: %w", err)
		}
		if err := mergeFile(&cfg, parsed); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func mergeFile(cfg *Config, parsed fileConfig) error {
	if parsed.Postgres.DSN != "" {
		cfg.Postgres.DSN = parsed.Postgres.DSN
	}
	if parsed.Postgres.ApplicationName != "" {
		cfg.Postgres.ApplicationName = parsed.Postgres.ApplicationName
	}
	if parsed.Postgres.ConnectTimeout != "" {
		d, err := time.ParseDuration(parsed.Postgres.ConnectTimeout)
		if err != nil {
			return fmt.Errorf("config postgres.connect_timeout: %w", err)
		}
		cfg.Postgres.ConnectTimeout = d
	}
	if parsed.Postgres.QueryTimeout != "" {
		d, err := time.ParseDuration(parsed.Postgres.QueryTimeout)
		if err != nil {
			return fmt.Errorf("config postgres.query_timeout: %w", err)
		}
		cfg.Postgres.QueryTimeout = d
	}
	if parsed.Log.Level != "" {
		cfg.Log.Level = parsed.Log.Level
	}
	if parsed.Log.Format != "" {
		cfg.Log.Format = parsed.Log.Format
	}
	mergeAssess(&cfg.Assess, parsed.Assess)
	mergeTuner(&cfg.Tuner, parsed.Tuner)
	return mergeDoctor(&cfg.Doctor, parsed.Doctor)
}

func mergeAssess(dst *AssessConfig, src AssessConfig) {
	if src.HighDeadRatio != 0 {
		dst.HighDeadRatio = src.HighDeadRatio
	}
	if src.CriticalDeadRatio != 0 {
		dst.CriticalDeadRatio = src.CriticalDeadRatio
	}
	if src.HighHoursToTrigger != 0 {
		dst.HighHoursToTrigger = src.HighHoursToTrigger
	}
	if src.WraparoundWarningRatio != 0 {
		dst.WraparoundWarningRatio = src.WraparoundWarningRatio
	}
	if src.WraparoundCriticalRatio != 0 {
		dst.WraparoundCriticalRatio = src.WraparoundCriticalRatio
	}
	if src.AssumedCostPerPage != 0 {
		dst.AssumedCostPerPage = src.AssumedCostPerPage
	}
	if src.AssumedTuplesPerPage != 0 {
		dst.AssumedTuplesPerPage = src.AssumedTuplesPerPage
	}
	if src.IOMultiplier != 0 {
		dst.IOMultiplier = src.IOMultiplier
	}
	if src.MinTriggerForScaleWarning != 0 {
		dst.MinTriggerForScaleWarning = src.MinTriggerForScaleWarning
	}
}

func mergeTuner(dst *TunerConfig, src TunerConfig) {
	if src.MaxHoursBetweenVacuum != 0 {
		dst.MaxHoursBetweenVacuum = src.MaxHoursBetweenVacuum
	}
	if src.MinScaleFactor != 0 {
		dst.MinScaleFactor = src.MinScaleFactor
	}
	if src.MaxScaleFactor != 0 {
		dst.MaxScaleFactor = src.MaxScaleFactor
	}
	if src.MinThreshold != 0 {
		dst.MinThreshold = src.MinThreshold
	}
	if src.MaxThreshold != 0 {
		dst.MaxThreshold = src.MaxThreshold
	}
	if src.CostLimitBump != 0 {
		dst.CostLimitBump = src.CostLimitBump
	}
	if src.MinCostLimit != 0 {
		dst.MinCostLimit = src.MinCostLimit
	}
	if src.MaxCostLimit != 0 {
		dst.MaxCostLimit = src.MaxCostLimit
	}
	if src.CostLimitHeadroom != 0 {
		dst.CostLimitHeadroom = src.CostLimitHeadroom
	}
	if src.ScaleDecimals != 0 {
		dst.ScaleDecimals = src.ScaleDecimals
	}
}

func mergeDoctor(dst *DoctorConfig, src doctorFile) error {
	if src.MaxScore != 0 {
		dst.MaxScore = src.MaxScore
	}
	if src.MinScore != 0 {
		dst.MinScore = src.MinScore
	}
	if src.CriticalPenalty != 0 {
		dst.CriticalPenalty = src.CriticalPenalty
	}
	if src.HighPenalty != 0 {
		dst.HighPenalty = src.HighPenalty
	}
	if src.WarningPenalty != 0 {
		dst.WarningPenalty = src.WarningPenalty
	}
	if src.LongXactPenalty != 0 {
		dst.LongXactPenalty = src.LongXactPenalty
	}
	if src.WraparoundPenalty != 0 {
		dst.WraparoundPenalty = src.WraparoundPenalty
	}
	if src.LongXactAfter != "" {
		d, err := time.ParseDuration(src.LongXactAfter)
		if err != nil {
			return fmt.Errorf("config doctor.long_xact_after: %w", err)
		}
		dst.LongXactAfter = d
	}
	return nil
}

func applyEnv(cfg *Config) error {
	if v := os.Getenv("PGAV_DSN"); v != "" {
		cfg.Postgres.DSN = v
	}
	if v := os.Getenv("PGAV_APPLICATION_NAME"); v != "" {
		cfg.Postgres.ApplicationName = v
	}
	if v := os.Getenv("PGAV_CONNECT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config PGAV_CONNECT_TIMEOUT: %w", err)
		}
		cfg.Postgres.ConnectTimeout = d
	}
	if v := os.Getenv("PGAV_QUERY_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config PGAV_QUERY_TIMEOUT: %w", err)
		}
		cfg.Postgres.QueryTimeout = d
	}
	if v := os.Getenv("PGAV_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("PGAV_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("PGAV_LONG_XACT_AFTER"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("config PGAV_LONG_XACT_AFTER: %w", err)
		}
		cfg.Doctor.LongXactAfter = d
	}
	if v := os.Getenv("PGAV_MAX_HOURS_BETWEEN_VACUUM"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("config PGAV_MAX_HOURS_BETWEEN_VACUUM: %w", err)
		}
		cfg.Tuner.MaxHoursBetweenVacuum = f
	}
	return nil
}

// Validate returns aggregated configuration errors.
func (c Config) Validate() error {
	var errs []error
	if c.Postgres.ConnectTimeout <= 0 {
		errs = append(errs, errors.New("postgres.connect_timeout must be > 0"))
	}
	if c.Postgres.QueryTimeout <= 0 {
		errs = append(errs, errors.New("postgres.query_timeout must be > 0"))
	}
	if c.Postgres.ApplicationName == "" {
		errs = append(errs, errors.New("postgres.application_name must not be empty"))
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q is invalid", c.Log.Level))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format %q is invalid", c.Log.Format))
	}
	if c.Assess.HighDeadRatio <= 0 || c.Assess.HighDeadRatio >= 1 {
		errs = append(errs, errors.New("assess.high_dead_ratio must be in (0,1)"))
	}
	if c.Assess.CriticalDeadRatio <= c.Assess.HighDeadRatio || c.Assess.CriticalDeadRatio >= 1 {
		errs = append(errs, errors.New("assess.critical_dead_ratio must be in (high_dead_ratio,1)"))
	}
	if c.Assess.HighHoursToTrigger <= 0 {
		errs = append(errs, errors.New("assess.high_hours_to_trigger must be > 0"))
	}
	if c.Assess.WraparoundWarningRatio <= 0 || c.Assess.WraparoundWarningRatio >= 1 {
		errs = append(errs, errors.New("assess.wraparound_warning_ratio must be in (0,1)"))
	}
	if c.Assess.WraparoundCriticalRatio <= c.Assess.WraparoundWarningRatio || c.Assess.WraparoundCriticalRatio > 1 {
		errs = append(errs, errors.New("assess.wraparound_critical_ratio must be in (wraparound_warning_ratio,1]"))
	}
	if c.Assess.AssumedCostPerPage <= 0 {
		errs = append(errs, errors.New("assess.assumed_cost_per_page must be > 0"))
	}
	if c.Assess.AssumedTuplesPerPage <= 0 {
		errs = append(errs, errors.New("assess.assumed_tuples_per_page must be > 0"))
	}
	if c.Assess.IOMultiplier < 0 {
		errs = append(errs, errors.New("assess.io_multiplier must be >= 0"))
	}
	if c.Assess.MinTriggerForScaleWarning <= 0 {
		errs = append(errs, errors.New("assess.min_trigger_for_scale_warning must be > 0"))
	}
	if c.Tuner.MaxHoursBetweenVacuum <= 0 {
		errs = append(errs, errors.New("tuner.max_hours_between_vacuum must be > 0"))
	}
	if c.Tuner.MinScaleFactor <= 0 || c.Tuner.MinScaleFactor > c.Tuner.MaxScaleFactor {
		errs = append(errs, errors.New("tuner.min_scale_factor must be > 0 and <= max_scale_factor"))
	}
	if c.Tuner.MinThreshold <= 0 || c.Tuner.MinThreshold > c.Tuner.MaxThreshold {
		errs = append(errs, errors.New("tuner.min_threshold must be > 0 and <= max_threshold"))
	}
	if c.Tuner.CostLimitBump <= 0 {
		errs = append(errs, errors.New("tuner.cost_limit_bump must be > 0"))
	}
	if c.Tuner.MinCostLimit <= 0 || c.Tuner.MinCostLimit > c.Tuner.MaxCostLimit {
		errs = append(errs, errors.New("tuner.min_cost_limit must be > 0 and <= max_cost_limit"))
	}
	if c.Tuner.MaxCostLimit <= 0 {
		errs = append(errs, errors.New("tuner.max_cost_limit must be > 0"))
	}
	if c.Tuner.CostLimitHeadroom < 1 {
		errs = append(errs, errors.New("tuner.cost_limit_headroom must be >= 1"))
	}
	if c.Tuner.ScaleDecimals < 0 {
		errs = append(errs, errors.New("tuner.scale_decimals must be >= 0"))
	}
	if c.Doctor.MaxScore <= c.Doctor.MinScore {
		errs = append(errs, errors.New("doctor.max_score must be > min_score"))
	}
	if c.Doctor.CriticalPenalty < 0 || c.Doctor.HighPenalty < 0 || c.Doctor.WarningPenalty < 0 {
		errs = append(errs, errors.New("doctor penalties must be >= 0"))
	}
	if c.Doctor.LongXactPenalty < 0 || c.Doctor.WraparoundPenalty < 0 {
		errs = append(errs, errors.New("doctor penalties must be >= 0"))
	}
	if c.Doctor.LongXactAfter <= 0 {
		errs = append(errs, errors.New("doctor.long_xact_after must be > 0"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
}
