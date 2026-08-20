package loadgen

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultsValidate(t *testing.T) {
	t.Parallel()
	require.NoError(t, Defaults().Validate())
}

func TestValidateRejectsEmptySchema(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Schema = ""
	cfg.Orders.Rows = 0
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrInvalid)
}

func TestLoadEnv(t *testing.T) {
	t.Setenv("PGAV_DSN", "postgres://lab")
	t.Setenv("PGAV_LAB_ORDERS_ROWS", "1000")
	t.Setenv("PGAV_LAB_SESSIONS_BATCH", "25")
	t.Setenv("PGAV_LAB_LONG_XACT", "false")
	t.Setenv("PGAV_LAB_ORDERS_PAUSE", "2s")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "postgres://lab", cfg.DSN)
	require.Equal(t, 1000, cfg.Orders.Rows)
	require.Equal(t, 25, cfg.SessionsBatch)
	require.False(t, cfg.LongXactEnabled)
	require.Equal(t, 2*time.Second, cfg.OrdersPause)
	require.NoError(t, cfg.Validate())
}

func TestLoadInvalidEnv(t *testing.T) {
	t.Setenv("PGAV_LAB_ORDERS_ROWS", "nope")
	_, err := Load()
	require.Error(t, err)
}
