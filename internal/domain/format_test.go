package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompactAge(t *testing.T) {
	t.Parallel()
	require.Equal(t, "13s", CompactAge(13*time.Second))
	require.Equal(t, "47m", CompactAge(47*time.Minute))
	require.Equal(t, "7h42m", CompactAge(7*time.Hour+42*time.Minute))
}

func TestFormatXact(t *testing.T) {
	t.Parallel()
	got := FormatXact(LongTransaction{
		PID:             42,
		ApplicationName: "pgav-lab-blocker",
		State:           "idle in transaction",
		Age:             12 * time.Minute,
		Query:           "SELECT id FROM customers FOR UPDATE",
	})
	require.Contains(t, got, "pid 42")
	require.Contains(t, got, "pgav-lab-blocker")
	require.Contains(t, got, "12m")
	require.Contains(t, got, "SELECT id FROM customers FOR UPDATE")
}

func TestFormatProgress(t *testing.T) {
	t.Parallel()
	got := FormatProgress(VacuumProgress{
		Table:           TableID{Schema: "public", Name: "sessions"},
		Phase:           "scanning heap",
		HeapBlksScanned: 25,
		HeapBlksTotal:   100,
	})
	require.Contains(t, got, "public.sessions")
	require.Contains(t, got, "25%")
	require.Contains(t, got, "scanning heap")
}

func TestTerminateSQL(t *testing.T) {
	t.Parallel()
	require.Equal(t, "SELECT pg_terminate_backend(42);", TerminateSQL(42))
}
