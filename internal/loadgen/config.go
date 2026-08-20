// Package loadgen seeds PostgreSQL tables and generates autovacuum stress traffic.
package loadgen

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// ErrInvalid is returned when Config.Validate fails.
var ErrInvalid = errors.New("loadgen: invalid")

// TableSpec describes one lab table.
type TableSpec struct {
	Name         string
	Rows         int
	PayloadBytes int
}

// Config is the traffic-generator configuration. Defaults live in Defaults().
type Config struct {
	DSN                 string
	ConnectTimeout      time.Duration
	RetryInterval       time.Duration
	ReadyWait           time.Duration
	Schema              string
	ChunkSize           int
	Orders              TableSpec
	Sessions            TableSpec
	Events              TableSpec
	Customers           TableSpec
	OrdersPause         time.Duration
	OrdersBatch         int
	SessionsPause       time.Duration
	SessionsBatch       int
	EventsPause         time.Duration
	EventsBatch         int
	SessionsScaleFactor float64
	SessionsCostLimit   int
	SessionsCostDelay   time.Duration
	EventsScaleFactor   float64
	EventsThreshold     int64
	LongXactEnabled     bool
	AppPrefix           string
}

// Defaults returns the lab baseline. This is the only place default values are defined.
func Defaults() Config {
	return Config{
		DSN:            "",
		ConnectTimeout: 5 * time.Second,
		RetryInterval:  500 * time.Millisecond,
		ReadyWait:      60 * time.Second,
		Schema:         "public",
		ChunkSize:      50_000,
		Orders: TableSpec{
			Name:         "orders",
			Rows:         400_000,
			PayloadBytes: 24,
		},
		Sessions: TableSpec{
			Name:         "sessions",
			Rows:         80_000,
			PayloadBytes: 64,
		},
		Events: TableSpec{
			Name:         "events",
			Rows:         20_000,
			PayloadBytes: 16,
		},
		Customers: TableSpec{
			Name:         "customers",
			Rows:         10_000,
			PayloadBytes: 16,
		},
		OrdersPause:         2 * time.Second,
		OrdersBatch:         8_000,
		SessionsPause:       80 * time.Millisecond,
		SessionsBatch:       400,
		EventsPause:         time.Hour,
		EventsBatch:         0,
		SessionsScaleFactor: 0.2,
		SessionsCostLimit:   2,
		SessionsCostDelay:   100 * time.Millisecond,
		EventsScaleFactor:   0.01,
		EventsThreshold:     1000,
		LongXactEnabled:     true,
		AppPrefix:           "pgav-lab",
	}
}

// Load merges defaults with environment variables.
func Load() (Config, error) {
	cfg := Defaults()
	if v := os.Getenv("PGAV_DSN"); v != "" {
		cfg.DSN = v
	}
	if err := applyDurationEnv(&cfg.ConnectTimeout, "PGAV_CONNECT_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv(&cfg.ReadyWait, "PGAV_LAB_READY_WAIT"); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv(&cfg.Orders.Rows, "PGAV_LAB_ORDERS_ROWS"); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv(&cfg.Sessions.Rows, "PGAV_LAB_SESSIONS_ROWS"); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv(&cfg.OrdersBatch, "PGAV_LAB_ORDERS_BATCH"); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv(&cfg.SessionsBatch, "PGAV_LAB_SESSIONS_BATCH"); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv(&cfg.OrdersPause, "PGAV_LAB_ORDERS_PAUSE"); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv(&cfg.SessionsPause, "PGAV_LAB_SESSIONS_PAUSE"); err != nil {
		return Config{}, err
	}
	if v := os.Getenv("PGAV_LAB_LONG_XACT"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("loadgen PGAV_LAB_LONG_XACT: %w", err)
		}
		cfg.LongXactEnabled = on
	}
	return cfg, nil
}

func applyDurationEnv(dst *time.Duration, key string) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("loadgen %s: %w", key, err)
	}
	*dst = d
	return nil
}

func applyIntEnv(dst *int, key string) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("loadgen %s: %w", key, err)
	}
	*dst = n
	return nil
}

// Validate returns aggregated configuration errors.
func (c Config) Validate() error {
	var errs []error
	if c.ConnectTimeout <= 0 {
		errs = append(errs, errors.New("connect_timeout must be > 0"))
	}
	if c.RetryInterval <= 0 {
		errs = append(errs, errors.New("retry_interval must be > 0"))
	}
	if c.ReadyWait <= 0 {
		errs = append(errs, errors.New("ready_wait must be > 0"))
	}
	if c.Schema == "" {
		errs = append(errs, errors.New("schema must not be empty"))
	}
	if c.ChunkSize <= 0 {
		errs = append(errs, errors.New("chunk_size must be > 0"))
	}
	for _, spec := range []TableSpec{c.Orders, c.Sessions, c.Events, c.Customers} {
		if spec.Name == "" {
			errs = append(errs, errors.New("table name must not be empty"))
		}
		if spec.Rows <= 0 {
			errs = append(errs, fmt.Errorf("table %s rows must be > 0", spec.Name))
		}
		if spec.PayloadBytes <= 0 {
			errs = append(errs, fmt.Errorf("table %s payload_bytes must be > 0", spec.Name))
		}
	}
	if c.AppPrefix == "" {
		errs = append(errs, errors.New("app_prefix must not be empty"))
	}
	if c.OrdersBatch <= 0 || c.SessionsBatch <= 0 {
		errs = append(errs, errors.New("orders/sessions update batches must be > 0"))
	}
	if c.EventsBatch < 0 {
		errs = append(errs, errors.New("events update batch must be >= 0"))
	}
	if c.OrdersPause <= 0 || c.SessionsPause <= 0 {
		errs = append(errs, errors.New("orders/sessions update pauses must be > 0"))
	}
	if c.EventsBatch > 0 && c.EventsPause <= 0 {
		errs = append(errs, errors.New("events pause must be > 0 when events traffic is enabled"))
	}
	if c.SessionsScaleFactor <= 0 {
		errs = append(errs, errors.New("sessions scale_factor must be > 0"))
	}
	if c.SessionsCostLimit <= 0 {
		errs = append(errs, errors.New("sessions cost_limit must be > 0"))
	}
	if c.SessionsCostDelay <= 0 {
		errs = append(errs, errors.New("sessions cost_delay must be > 0"))
	}
	if c.EventsScaleFactor <= 0 {
		errs = append(errs, errors.New("events scale_factor must be > 0"))
	}
	if c.EventsThreshold <= 0 {
		errs = append(errs, errors.New("events threshold must be > 0"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
}
