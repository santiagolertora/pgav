package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testDoctorParams() DoctorParams {
	return DoctorParams{
		MaxScore:          100,
		MinScore:          0,
		CriticalPenalty:   15,
		HighPenalty:       8,
		WarningPenalty:    3,
		LongXactPenalty:   5,
		WraparoundPenalty: 20,
	}
}

func TestDoctorHealthyCluster(t *testing.T) {
	t.Parallel()
	tables := []RaceAssessment{
		{Table: TableID{Name: "a"}, Risk: RiskOK, KeepingUp: true, Reasons: []string{"vacuum is keeping up"}},
		{Table: TableID{Name: "b"}, Risk: RiskOK, KeepingUp: true, Reasons: []string{"vacuum is keeping up"}},
	}
	cluster := ClusterSettings{AutovacuumEnabled: true}
	got := Doctor(cluster, tables, nil, nil, testDoctorParams())
	require.Equal(t, 100, got.Score)
	require.Equal(t, 100, got.MaxScore)
	require.Equal(t, 2, got.OKCount)
	require.Empty(t, got.Findings)
}

func TestDoctorPenalizesCriticalAndLongXact(t *testing.T) {
	t.Parallel()
	tables := []RaceAssessment{
		{
			Table:     TableID{Schema: "public", Name: "sessions"},
			Risk:      RiskCritical,
			KeepingUp: false,
			Reasons:   []string{"vacuum cannot keep up with the write workload"},
		},
		{
			Table:     TableID{Schema: "public", Name: "orders"},
			Risk:      RiskHigh,
			KeepingUp: true,
			Reasons:   []string{"scale factor is too large for the write rate"},
		},
	}
	xacts := []LongTransaction{{PID: 38122, State: "idle in transaction", Age: 7*time.Hour + 42*time.Minute}}
	cluster := ClusterSettings{AutovacuumEnabled: true}
	got := Doctor(cluster, tables, xacts, nil, testDoctorParams())
	require.Equal(t, 100-15-8-5, got.Score)
	require.Equal(t, 0, got.OKCount)
	require.Len(t, got.Findings, 3)
	require.Equal(t, FindingVacuumCannotKeepUp, got.Findings[0].Code)
	require.Equal(t, FindingScaleFactorTooLarge, got.Findings[1].Code)
	require.Equal(t, FindingLongTransaction, got.Findings[2].Code)
	require.Contains(t, got.Findings[2].Detail, "38122")
	require.Contains(t, got.Findings[2].Detail, "7h42m")
	require.Contains(t, got.Findings[2].Detail, "pg_terminate_backend(38122)")
	require.Contains(t, got.Findings[2].Summary, "xmin")
}

func TestDoctorWraparoundTakesPrecedence(t *testing.T) {
	t.Parallel()
	tables := []RaceAssessment{
		{
			Table:           TableID{Schema: "public", Name: "huge"},
			Risk:            RiskCritical,
			KeepingUp:       false,
			WraparoundRatio: 0.97,
			Reasons:         []string{"freeze age is approaching wraparound", "vacuum cannot keep up with the write workload"},
		},
	}
	got := Doctor(ClusterSettings{AutovacuumEnabled: true}, tables, nil, []VacuumProgress{
		{Table: TableID{Schema: "public", Name: "huge"}, Phase: "scanning heap", HeapBlksScanned: 10, HeapBlksTotal: 100},
	}, testDoctorParams())
	require.Equal(t, FindingWraparound, got.Findings[0].Code)
	require.Contains(t, got.Findings[0].Detail, "97%")
	require.Len(t, got.Progress, 1)
}

func TestDoctorAutovacuumDisabled(t *testing.T) {
	t.Parallel()
	got := Doctor(ClusterSettings{AutovacuumEnabled: false}, nil, nil, nil, testDoctorParams())
	require.Equal(t, 85, got.Score)
	require.Equal(t, FindingAutovacuumDisabled, got.Findings[0].Code)
}

func TestDoctorClampsMinScore(t *testing.T) {
	t.Parallel()
	tables := make([]RaceAssessment, 20)
	for i := range tables {
		tables[i] = RaceAssessment{
			Table:     TableID{Name: "t"},
			Risk:      RiskCritical,
			KeepingUp: false,
			Reasons:   []string{"vacuum cannot keep up with the write workload"},
		}
	}
	got := Doctor(ClusterSettings{AutovacuumEnabled: true}, tables, nil, nil, testDoctorParams())
	require.Equal(t, 0, got.Score)
}
