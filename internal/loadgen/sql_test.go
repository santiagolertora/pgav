package loadgen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLBuilders(t *testing.T) {
	t.Parallel()
	require.Contains(t, createTableSQL("public", "orders"), `"public"."orders"`)
	require.Contains(t, insertChunkSQL("public", "orders", 1, 100, 3), "generate_series(1, 100)")
	require.Contains(t, insertChunkSQL("public", "orders", 1, 100, 3), "'xxx'")
	require.Equal(t, `ANALYZE "public"."sessions"`, analyzeSQL("public", "sessions"))
	require.Contains(t, throttleSessionsSQL("public", "sessions", 0.2, 2, 100), "autovacuum_vacuum_cost_limit = 2")
	require.Contains(t, friendlyEventsSQL("public", "events", 0.01, 1000), "autovacuum_vacuum_scale_factor = 0.01")
	require.Contains(t, batchUpdateSQL("public", "orders", 500), "LIMIT 500")
	require.Contains(t, lockOneSQL("public", "customers"), "FOR UPDATE")
}

func TestChunks(t *testing.T) {
	t.Parallel()
	require.Nil(t, chunks(0, 10))
	require.Equal(t, [][2]int{{1, 10}, {11, 20}, {21, 25}}, chunks(25, 10))
	require.Equal(t, [][2]int{{1, 5}}, chunks(5, 10))
}
