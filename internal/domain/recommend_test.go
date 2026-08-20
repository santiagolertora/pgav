package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testTunerParams() TunerParams {
	return TunerParams{
		MaxHoursBetweenVacuum: 4,
		MinScaleFactor:        0.001,
		MaxScaleFactor:        0.20,
		MinThreshold:          10000,
		MaxThreshold:          100000,
		CostLimitBump:         1800,
		MinCostLimit:          200,
		MaxCostLimit:          10000,
		CostLimitHeadroom:     1.25,
		LargeTableRelTuples:   1_000_000,
		ScaleDecimals:         3,
	}
}

func TestRecommendLowersScaleFactor(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:          TableID{Schema: "public", Name: "orders"},
		LiveTuples:  100_000_000,
		DeadTuples:  1_000_000,
		RelTuples:   100_000_000,
		TupUpdated:  1_800_000,
		StatsWindow: time.Hour,
		Settings:    VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	p := testAssessParams()
	p.AssumedCostPerPage = 1
	p.AssumedTuplesPerPage = 200
	p.IOMultiplier = 0
	assessment := Assess(snap, cluster, p)
	rec := Recommend(snap, assessment, testTunerParams())
	require.True(t, rec.Changed())
	require.Less(t, rec.Proposed.ScaleFactor, rec.Current.ScaleFactor)
	require.Equal(t, int64(50), rec.Proposed.Threshold)
	require.Contains(t, rec.AlterSQL(), "ALTER TABLE")
	require.Contains(t, rec.AlterSQL(), "autovacuum_vacuum_scale_factor")
}

func TestRecommendBumpsCostLimitWhenLosing(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:          TableID{Schema: "public", Name: "sessions"},
		LiveTuples:  1_000_000,
		DeadTuples:  500_000,
		RelTuples:   1_000_000,
		TupUpdated:  10_000_000,
		StatsWindow: time.Hour,
		Settings:    VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 200, CostDelay: 20 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	p := testAssessParams()
	p.AssumedCostPerPage = 20
	p.AssumedTuplesPerPage = 5
	p.IOMultiplier = 9
	assessment := Assess(snap, cluster, p)
	require.False(t, assessment.KeepingUp)
	rec := Recommend(snap, assessment, testTunerParams())
	require.Greater(t, rec.Proposed.CostLimit, rec.Current.CostLimit)
	require.Equal(t, int64(50), rec.Proposed.Threshold)
	require.Equal(t, 0, rec.Proposed.CostLimit%200)
	require.Contains(t, rec.AlterSQL(), "autovacuum_vacuum_cost_limit")
	require.Greater(t, rec.Impact.ProposedCapacityPerHour, rec.Impact.CurrentCapacityPerHour)
}

func TestRecommendNoChangeWhenHealthy(t *testing.T) {
	t.Parallel()
	snap := TableSnapshot{
		ID:          TableID{Schema: "public", Name: "events"},
		LiveTuples:  10_000,
		DeadTuples:  10,
		RelTuples:   10_000,
		TupUpdated:  10,
		StatsWindow: time.Hour,
		Settings:    VacuumSettings{ScaleFactor: 0.02, Threshold: 10000, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000}
	assessment := Assess(snap, cluster, testAssessParams())
	rec := Recommend(snap, assessment, testTunerParams())
	require.False(t, rec.Changed())
	require.Empty(t, rec.AlterSQL())
	require.Equal(t, []string{"no change"}, rec.Reasons)
}
