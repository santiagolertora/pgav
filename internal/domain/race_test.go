package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testAssessParams() AssessParams {
	return AssessParams{
		HighDeadRatio:             0.20,
		CriticalDeadRatio:         0.40,
		HighHoursToTrigger:        8,
		WraparoundWarningRatio:    0.80,
		WraparoundCriticalRatio:   0.95,
		AssumedCostPerPage:        10,
		AssumedTuplesPerPage:      50,
		IOMultiplier:              1,
		MinTriggerForScaleWarning: 1_000_000,
	}
}

func TestParseTableID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    TableID
		wantErr bool
	}{
		{name: "bare name defaults to public", input: "orders", want: TableID{Schema: "public", Name: "orders"}},
		{name: "qualified", input: "sales.orders", want: TableID{Schema: "sales", Name: "orders"}},
		{name: "empty", input: "  ", wantErr: true},
		{name: "too many dots", input: "a.b.c", wantErr: true},
		{name: "missing name", input: "sales.", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTableID(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestTriggerDeadTuples(t *testing.T) {
	t.Parallel()
	s := VacuumSettings{ScaleFactor: 0.20, Threshold: 50}
	require.Equal(t, int64(50), s.TriggerDeadTuples(0))
	require.Equal(t, int64(20000050), s.TriggerDeadTuples(100_000_000))
}

func TestDeadRatio(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0.0, DeadRatio(0, 0))
	require.InDelta(t, 0.5, DeadRatio(50, 50), 1e-9)
}

func TestAssessVacuumCannotKeepUp(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:           TableID{Schema: "public", Name: "sessions"},
		LiveTuples:   1_000_000,
		DeadTuples:   500_000,
		RelTuples:    1_000_000,
		TupUpdated:   10_000_000,
		TupDeleted:   0,
		StatsWindow:  time.Hour,
		FrozenXIDAge: 1000,
		Settings:     VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 200, CostDelay: 20 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000, MaxWorkers: 3}
	p := testAssessParams()
	p.AssumedCostPerPage = 20
	p.AssumedTuplesPerPage = 5
	p.IOMultiplier = 9
	got := Assess(snap, cluster, p)
	require.False(t, got.KeepingUp)
	require.Equal(t, RiskCritical, got.Risk)
	require.Contains(t, got.Reasons, "vacuum cannot keep up with the write workload")
}

func TestAssessScaleFactorTooLarge(t *testing.T) {
	t.Parallel()
	// High capacity, slow accumulation, huge trigger → hours to trigger is large.
	snap := TableSnapshot{
		ID:          TableID{Schema: "public", Name: "orders"},
		LiveTuples:  100_000_000,
		DeadTuples:  1_000_000,
		RelTuples:   100_000_000,
		TupUpdated:  1_800_000,
		StatsWindow: time.Hour,
		Settings:    VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 10000, CostDelay: time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	p := testAssessParams()
	p.AssumedCostPerPage = 1
	p.AssumedTuplesPerPage = 200
	p.IOMultiplier = 0
	got := Assess(snap, cluster, p)
	require.True(t, got.KeepingUp)
	require.Equal(t, RiskHigh, got.Risk)
	require.Contains(t, got.Reasons, "scale factor is too large for the write rate")
}

func TestAssessOK(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:          TableID{Schema: "public", Name: "customers"},
		LiveTuples:  100_000,
		DeadTuples:  80,
		RelTuples:   100_000,
		TupUpdated:  100,
		StatsWindow: time.Hour,
		Settings:    VacuumSettings{ScaleFactor: 0.02, Threshold: 10000, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	got := Assess(snap, cluster, testAssessParams())
	require.True(t, got.KeepingUp)
	require.Equal(t, RiskOK, got.Risk)
}

func TestHoursToTrigger(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0.0, HoursToTrigger(100, 50, 10))
	require.Equal(t, -1.0, HoursToTrigger(10, 50, 0))
	require.InDelta(t, 4.0, HoursToTrigger(10, 50, 10), 1e-9)
}

func TestWraparoundCritical(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:           TableID{Schema: "public", Name: "legacy"},
		LiveTuples:   1000,
		DeadTuples:   1,
		RelTuples:    1000,
		StatsWindow:  time.Hour,
		FrozenXIDAge: 190_000_000,
		Settings:     VacuumSettings{ScaleFactor: 0.02, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	got := Assess(snap, cluster, testAssessParams())
	require.Equal(t, RiskCritical, got.Risk)
	require.Contains(t, got.Reasons, "freeze age is approaching wraparound")
}

func TestRiskLevelString(t *testing.T) {
	t.Parallel()
	require.Equal(t, "OK", RiskOK.String())
	require.Equal(t, "CRITICAL", RiskCritical.String())
	require.Equal(t, "UNKNOWN", RiskLevel(99).String())
}

func TestTableIDQualified(t *testing.T) {
	t.Parallel()
	id := TableID{Schema: `public`, Name: `or"ders`}
	require.Equal(t, `"public"."or""ders"`, id.Qualified())
}
