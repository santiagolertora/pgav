// Package domain contains autovacuum race-model types and pure calculations.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// RiskLevel is the attention level for a table or cluster finding.
type RiskLevel int

const (
	// RiskOK means vacuum is keeping up.
	RiskOK RiskLevel = iota
	// RiskWarning means a condition that can become a problem.
	RiskWarning
	// RiskHigh means settings or lag need attention soon.
	RiskHigh
	// RiskCritical means vacuum is losing or wraparound is near.
	RiskCritical
)

func (r RiskLevel) String() string {
	switch r {
	case RiskOK:
		return "OK"
	case RiskWarning:
		return "WARNING"
	case RiskHigh:
		return "HIGH"
	case RiskCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// TableID is a schema-qualified table name.
type TableID struct {
	Schema string
	Name   string
}

func (id TableID) String() string {
	if id.Schema == "" {
		return id.Name
	}
	return id.Schema + "." + id.Name
}

// Qualified returns a quoted schema.table identifier.
func (id TableID) Qualified() string {
	return quoteIdent(id.Schema) + "." + quoteIdent(id.Name)
}

func quoteIdent(s string) string {
	return QuoteIdent(s)
}

// QuoteIdent returns a PostgreSQL quoted identifier.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ParseTableID parses "name" or "schema.name".
func ParseTableID(raw string) (TableID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TableID{}, fmt.Errorf("domain: empty table name")
	}
	parts := strings.Split(raw, ".")
	switch len(parts) {
	case 1:
		return TableID{Schema: "public", Name: parts[0]}, nil
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return TableID{}, fmt.Errorf("domain: invalid table name %q", raw)
		}
		return TableID{Schema: parts[0], Name: parts[1]}, nil
	default:
		return TableID{}, fmt.Errorf("domain: invalid table name %q", raw)
	}
}

// VacuumSettings are the autovacuum knobs that decide when and how hard vacuum runs.
type VacuumSettings struct {
	ScaleFactor float64
	Threshold   int64
	CostLimit   int
	CostDelay   time.Duration
}

// TriggerDeadTuples is threshold + scale_factor * reltuples.
func (s VacuumSettings) TriggerDeadTuples(relTuples float64) int64 {
	if relTuples < 0 {
		relTuples = 0
	}
	v := float64(s.Threshold) + s.ScaleFactor*relTuples
	if v < 0 {
		return 0
	}
	return int64(v)
}

// ClusterSettings are instance-level autovacuum parameters.
type ClusterSettings struct {
	AutovacuumEnabled bool
	MaxWorkers        int
	Naptime           time.Duration
	FreezeMaxAge      int64
	Defaults          VacuumSettings
}

// TableSnapshot is a point-in-time view of one table's vacuum-relevant stats.
type TableSnapshot struct {
	ID              TableID
	LiveTuples      int64
	DeadTuples      int64
	RelTuples       float64
	TupUpdated      int64
	TupHotUpdated   int64
	TupDeleted      int64
	LastVacuum      time.Time
	LastAutovacuum  time.Time
	LastAnalyze     time.Time
	LastAutoanalyze time.Time
	ModSinceAnalyze int64
	StatsWindow     time.Duration
	SizeBytes       int64
	FrozenXIDAge    int64
	Settings        VacuumSettings
}

// DeadGenerated approximates non-HOT dead-tuple production in the stats window.
func (s TableSnapshot) DeadGenerated() int64 {
	hot := s.TupHotUpdated
	if hot > s.TupUpdated {
		hot = s.TupUpdated
	}
	n := s.TupUpdated - hot + s.TupDeleted
	if n < 0 {
		return 0
	}
	return n
}

// LastCleanup is the most recent vacuum or autovacuum, or zero.
func (s TableSnapshot) LastCleanup() time.Time {
	if s.LastAutovacuum.After(s.LastVacuum) {
		return s.LastAutovacuum
	}
	return s.LastVacuum
}

// LastStats is the most recent ANALYZE or autoanalyze, or zero.
func (s TableSnapshot) LastStats() time.Time {
	if s.LastAutoanalyze.After(s.LastAnalyze) {
		return s.LastAutoanalyze
	}
	return s.LastAnalyze
}

// LongTransaction is a backend holding an open transaction that blocks cleanup.
type LongTransaction struct {
	PID             int32
	User            string
	ApplicationName string
	State           string
	Age             time.Duration
	Query           string
}

// VacuumProgress is a row from pg_stat_progress_vacuum.
type VacuumProgress struct {
	Table           TableID
	Phase           string
	HeapBlksScanned int64
	HeapBlksTotal   int64
}

// FindingCode identifies a doctor finding.
type FindingCode string

const (
	// FindingVacuumCannotKeepUp means dead_rate > vacuum capacity.
	FindingVacuumCannotKeepUp FindingCode = "vacuum_cannot_keep_up"
	// FindingScaleFactorTooLarge means the table waits too long to hit the trigger.
	FindingScaleFactorTooLarge FindingCode = "scale_factor_too_large"
	// FindingLongTransaction means an open xact is blocking cleanup.
	FindingLongTransaction FindingCode = "long_transaction"
	// FindingCostThrottling means cost delay is limiting reclaim rate.
	FindingCostThrottling FindingCode = "cost_throttling"
	// FindingWraparound means freeze age is approaching freeze_max_age.
	FindingWraparound FindingCode = "wraparound"
	// FindingAutovacuumDisabled means autovacuum is off at the cluster.
	FindingAutovacuumDisabled FindingCode = "autovacuum_disabled"
	// FindingHighDeadRatio means dead tuples are a large fraction of the table.
	FindingHighDeadRatio FindingCode = "high_dead_ratio"
)

// Finding is one doctor diagnosis.
type Finding struct {
	Severity RiskLevel
	Table    *TableID
	Code     FindingCode
	Summary  string
	Detail   string
}
