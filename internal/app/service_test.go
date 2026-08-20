package app

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/pgav/internal/app/catalogfake"
	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/domain"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func healthySnap(name string) domain.TableSnapshot {
	return domain.TableSnapshot{
		ID:          domain.TableID{Schema: "public", Name: name},
		LiveTuples:  100_000,
		DeadTuples:  80,
		RelTuples:   100_000,
		TupUpdated:  100,
		StatsWindow: time.Hour,
		Settings:    domain.VacuumSettings{ScaleFactor: 0.02, Threshold: 10000, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
}

func problemSnap() domain.TableSnapshot {
	return domain.TableSnapshot{
		ID:          domain.TableID{Schema: "public", Name: "orders"},
		LiveTuples:  100_000_000,
		DeadTuples:  45_000_000,
		RelTuples:   100_000_000,
		TupUpdated:  1_800_000,
		StatsWindow: time.Hour,
		Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
	}
}

func TestStatusAndDoctor(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster:   domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000, MaxWorkers: 3},
		Snapshots: []domain.TableSnapshot{healthySnap("customers"), problemSnap()},
		Xacts:     []domain.LongTransaction{{PID: 1, State: "active", Age: 2 * time.Hour}},
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)

	status, err := svc.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, status, 2)

	doc, err := svc.Doctor(t.Context())
	require.NoError(t, err)
	require.Less(t, doc.Score, 100)
	require.NotEmpty(t, doc.Findings)
}

func TestAnalyzeAndTuneDryRun(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster:   domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{problemSnap()},
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)

	rep, err := svc.Analyze(t.Context(), "orders")
	require.NoError(t, err)
	require.Equal(t, "public.orders", rep.Snapshot.ID.String())
	require.NotEqual(t, domain.RiskOK, rep.Assessment.Risk)

	tune, err := svc.Tune(t.Context(), false)
	require.NoError(t, err)
	require.NotEmpty(t, tune.Statements)
	require.False(t, tune.Applied)
	require.Empty(t, cat.Applied)
	require.Empty(t, tune.Blockers)
}

func TestTuneReportsBlockers(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster:   domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{problemSnap()},
		Xacts:     []domain.LongTransaction{{PID: 92, ApplicationName: "pgav-lab-blocker", State: "idle in transaction", Age: time.Minute}},
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)
	tune, err := svc.Tune(t.Context(), false)
	require.NoError(t, err)
	require.False(t, tune.Applied)
	require.Len(t, tune.Blockers, 1)
	require.Equal(t, int32(92), tune.Blockers[0].PID)
}

func TestTuneApply(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster:   domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{problemSnap()},
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)
	tune, err := svc.Tune(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, tune.Statements, cat.Applied)
	require.True(t, tune.Applied)
}

func TestNewRejectsNil(t *testing.T) {
	t.Parallel()
	_, err := New(nil, config.Defaults(), testLogger())
	require.Error(t, err)
	_, err = New(&catalogfake.Catalog{}, config.Defaults(), nil)
	require.Error(t, err)
}

func TestAnalyzeUnknownTable(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)
	_, err = svc.Analyze(t.Context(), "missing")
	require.Error(t, err)
}

func TestTuneApplyError(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster:   domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{problemSnap()},
		FailApply: true,
	}
	svc, err := New(cat, config.Defaults(), testLogger())
	require.NoError(t, err)
	_, err = svc.Tune(t.Context(), true)
	require.Error(t, err)
}
