// Package observability constructs the process logger from configuration.
package observability

import (
	"io"
	"log/slog"
	"strings"

	"github.com/santiagolertora/pgav/internal/config"
)

// NewLogger builds a slog logger. Level and format come from cfg; invalid values are rejected by Config.Validate.
func NewLogger(w io.Writer, cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(cfg.Format, "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
