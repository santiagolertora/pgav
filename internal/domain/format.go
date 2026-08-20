package domain

import (
	"fmt"
	"strings"
	"time"
)

// CompactAge formats a duration as 13s, 47m, or 1h18m.
func CompactAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// FormatXact renders a vacuum-blocking backend for operator output.
func FormatXact(x LongTransaction) string {
	app := x.ApplicationName
	if app == "" {
		app = "-"
	}
	q := strings.TrimSpace(strings.ReplaceAll(x.Query, "\n", " "))
	if len(q) > 80 {
		q = q[:80] + "…"
	}
	line := fmt.Sprintf("pid %d  app=%s  %s  %s", x.PID, app, x.State, CompactAge(x.Age))
	if q != "" {
		line += "\n  " + q
	}
	return line
}

// FormatProgress renders a running VACUUM for operator output.
func FormatProgress(p VacuumProgress) string {
	pct := 0
	if p.HeapBlksTotal > 0 {
		pct = int((100 * p.HeapBlksScanned) / p.HeapBlksTotal)
	}
	name := p.Table.String()
	if name == "." || name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s  %s  %d%% (%d/%d heap blocks)", name, p.Phase, pct, p.HeapBlksScanned, p.HeapBlksTotal)
}

// TerminateSQL is the operator command that releases a blocking backend.
func TerminateSQL(pid int32) string {
	return fmt.Sprintf("SELECT pg_terminate_backend(%d);", pid)
}
