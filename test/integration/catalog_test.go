//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgcatalog "github.com/santiagolertora/pgav/internal/adapter/outbound/postgres"
	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/domain"
)

func TestCatalogAgainstPostgres(t *testing.T) {
	ctx := t.Context()
	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("pgav"),
		postgres.WithUsername("pgav"),
		postgres.WithPassword("pgav"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, container.Terminate(context.Background()))
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `CREATE TABLE orders (id int primary key, payload text)`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO orders SELECT g, 'x' FROM generate_series(1, 1000) g`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `UPDATE orders SET payload = 'y'`)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	cfg := config.Defaults().Postgres
	cfg.DSN = dsn
	cat, err := pgcatalog.NewCatalog(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cat.Close(context.Background()))
	})

	cluster, err := cat.ClusterSettings(ctx)
	require.NoError(t, err)
	require.True(t, cluster.AutovacuumEnabled)
	require.Greater(t, cluster.MaxWorkers, 0)
	require.Greater(t, cluster.Defaults.Threshold, int64(0))

	snaps, err := cat.TableSnapshots(ctx)
	require.NoError(t, err)
	var orders domain.TableSnapshot
	found := false
	for _, s := range snaps {
		if s.ID.Name == "orders" {
			orders = s
			found = true
			break
		}
	}
	require.True(t, found)
	require.Greater(t, orders.DeadTuples, int64(0))

	one, err := cat.TableSnapshot(ctx, domain.TableID{Schema: "public", Name: "orders"})
	require.NoError(t, err)
	require.Equal(t, orders.ID, one.ID)

	prog, err := cat.VacuumProgress(ctx)
	require.NoError(t, err)
	require.NotNil(t, prog)

	stmt := `ALTER TABLE "public"."orders" SET (autovacuum_vacuum_scale_factor = 0.02)`
	require.NoError(t, cat.Apply(ctx, []string{stmt}))
	require.Error(t, cat.Apply(ctx, []string{"DROP TABLE orders"}))
}
