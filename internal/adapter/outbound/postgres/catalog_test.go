package postgres

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/pgav/internal/domain"
)

func TestParsePGInterval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want time.Duration
	}{
		{in: "2ms", want: 2 * time.Millisecond},
		{in: "100", want: 100 * time.Millisecond},
		{in: "1min", want: time.Minute},
		{in: "00:00:00.002", want: 2 * time.Millisecond},
		{in: "1s", want: time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parsePGInterval(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseClusterSettingsUsesVacuumFallback(t *testing.T) {
	t.Parallel()
	got, err := parseClusterSettings("on", "3", "1min", "200000000", "0.2", "50", "-1", "-1", "200", "20ms")
	require.NoError(t, err)
	require.True(t, got.AutovacuumEnabled)
	require.Equal(t, 3, got.MaxWorkers)
	require.Equal(t, time.Minute, got.Naptime)
	require.Equal(t, 200, got.Defaults.CostLimit)
	require.Equal(t, 20*time.Millisecond, got.Defaults.CostDelay)
}

func TestApplyReloptions(t *testing.T) {
	t.Parallel()
	s := domain.VacuumSettings{ScaleFactor: 0.2, Threshold: 50, CostLimit: 200, CostDelay: 2 * time.Millisecond}
	applyReloptions(&s, []string{
		"autovacuum_vacuum_scale_factor=0.02",
		"autovacuum_vacuum_threshold=10000",
		"autovacuum_vacuum_cost_delay=100",
		"fillfactor=90",
	})
	require.Equal(t, 0.02, s.ScaleFactor)
	require.Equal(t, int64(10000), s.Threshold)
	require.Equal(t, 200, s.CostLimit)
	require.Equal(t, 100*time.Millisecond, s.CostDelay)
}

func TestSnapshotFromRowWindow(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	now := reset.Add(2 * time.Hour)
	last := reset.Add(time.Hour)
	row := tableRow{
		schema:     "public",
		name:       "orders",
		live:       10,
		dead:       1,
		reltuples:  10,
		statsReset: &reset,
		lastAuto:   &last,
	}
	cluster := domain.ClusterSettings{Defaults: domain.VacuumSettings{ScaleFactor: 0.2, Threshold: 50}}
	snap := snapshotFromRow(row, cluster, now)
	require.Equal(t, "public.orders", snap.ID.String())
	require.Equal(t, 2*time.Hour, snap.StatsWindow)
	require.Equal(t, last, snap.LastAutovacuum)
}
