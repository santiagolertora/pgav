package observability

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/santiagolertora/pgav/internal/config"
)

func TestNewLoggerJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := NewLogger(&buf, config.LogConfig{Level: "debug", Format: "json"})
	log.Debug("booted", "component", "pgav")
	require.Contains(t, buf.String(), `"msg":"booted"`)
	require.Contains(t, buf.String(), `"component":"pgav"`)
}

func TestNewLoggerTextInfoDropsDebug(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := NewLogger(&buf, config.LogConfig{Level: "info", Format: "text"})
	log.Debug("hidden")
	log.Info("visible")
	require.NotContains(t, buf.String(), "hidden")
	require.Contains(t, buf.String(), "visible")
	require.True(t, log.Handler().Enabled(t.Context(), slog.LevelInfo))
	require.False(t, log.Handler().Enabled(t.Context(), slog.LevelDebug))
}
