package cli

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/pgav/internal/app"
	"github.com/santiagolertora/pgav/internal/app/catalogfake"
	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/domain"
)

func testOpen(cat app.Catalog) func(context.Context, config.Config) (app.Catalog, error) {
	return func(ctx context.Context, cfg config.Config) (app.Catalog, error) {
		_ = ctx
		_ = cfg
		return cat, nil
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	cmd := New(Options{Version: "testdev", Stdout: &out, Stderr: io.Discard})
	cmd.SetArgs([]string{"version"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Equal(t, "testdev\n", out.String())
}

func TestStatusCommand(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{
			{
				ID:          domain.TableID{Schema: "public", Name: "orders"},
				LiveTuples:  100_000_000,
				DeadTuples:  45_000_000,
				RelTuples:   100_000_000,
				TupUpdated:  1_800_000,
				StatsWindow: time.Hour,
				LastVacuum:  time.Now().Add(-47 * time.Minute),
				Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"status"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "public.orders")
	require.Contains(t, out.String(), "KEEP UP")
	require.Contains(t, out.String(), "FREEZE")
}

func TestAnalyzeAndDoctorCommands(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{
			{
				ID:          domain.TableID{Schema: "public", Name: "orders"},
				LiveTuples:  100_000_000,
				DeadTuples:  45_000_000,
				RelTuples:   100_000_000,
				TupUpdated:  1_800_000,
				StatsWindow: time.Hour,
				Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"analyze", "orders"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "autovacuum_vacuum_scale_factor")
	require.Contains(t, out.String(), "Race:")
	require.Contains(t, out.String(), "est. reclaim")
	require.Contains(t, out.String(), "freeze age")
	require.Contains(t, out.String(), "Recommendation (dry-run):")
	require.Contains(t, out.String(), "SQL (not executed):")

	out.Reset()
	cmd = New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"doctor"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "Autovacuum Health:")
	require.Contains(t, out.String(), "Next:")
	require.Contains(t, out.String(), "dry-run")
}

func TestTuneDryRun(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{
			{
				ID:          domain.TableID{Schema: "public", Name: "orders"},
				LiveTuples:  100_000_000,
				DeadTuples:  45_000_000,
				RelTuples:   100_000_000,
				TupUpdated:  1_800_000,
				StatsWindow: time.Hour,
				Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"tune"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "DRY-RUN")
	require.Contains(t, out.String(), "nothing applied")
	require.Contains(t, out.String(), "SQL (not executed)")
	require.Contains(t, out.String(), "scale_factor")
	require.Contains(t, out.String(), "pgav tune --apply")
	require.Empty(t, cat.Applied)
}

func TestTuneApplyCommand(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{
			{
				ID:          domain.TableID{Schema: "public", Name: "orders"},
				LiveTuples:  100_000_000,
				DeadTuples:  45_000_000,
				RelTuples:   100_000_000,
				TupUpdated:  1_800_000,
				StatsWindow: time.Hour,
				Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
			},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"tune", "--apply"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "APPLIED")
	require.Contains(t, out.String(), "SQL (executed)")
	require.NotEmpty(t, cat.Applied)
}

func TestTuneWarnsIdleTransaction(t *testing.T) {
	t.Parallel()
	cat := &catalogfake.Catalog{
		Cluster: domain.ClusterSettings{AutovacuumEnabled: true, FreezeMaxAge: 200_000_000},
		Snapshots: []domain.TableSnapshot{
			{
				ID:          domain.TableID{Schema: "public", Name: "orders"},
				LiveTuples:  100_000_000,
				DeadTuples:  45_000_000,
				RelTuples:   100_000_000,
				TupUpdated:  1_800_000,
				StatsWindow: time.Hour,
				Settings:    domain.VacuumSettings{ScaleFactor: 0.20, Threshold: 50, CostLimit: 2000, CostDelay: 2 * time.Millisecond},
			},
		},
		Xacts: []domain.LongTransaction{
			{PID: 92, ApplicationName: "pgav-lab-blocker", State: "idle in transaction", Age: time.Minute},
		},
	}
	var out bytes.Buffer
	cmd := New(Options{Version: "test", Stdout: &out, Stderr: io.Discard, Open: testOpen(cat)})
	cmd.SetArgs([]string{"tune"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	require.Contains(t, out.String(), "DRY-RUN")
	require.Contains(t, out.String(), "Blocked:")
	require.Contains(t, out.String(), "pgav-lab-blocker")
	require.Contains(t, out.String(), "pg_terminate_backend(92)")
	require.Empty(t, cat.Applied)
}

func TestFormatCount(t *testing.T) {
	t.Parallel()
	require.Equal(t, "18.2M", formatCount(18_200_000))
	require.Equal(t, "81K", formatCount(81_000))
	require.Equal(t, "50", formatCount(50))
}
