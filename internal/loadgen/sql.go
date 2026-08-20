package loadgen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/santiagolertora/pgav/internal/domain"
)

func qualified(schema, name string) string {
	return domain.TableID{Schema: schema, Name: name}.Qualified()
}

func createTableSQL(schema, name string) string {
	q := qualified(schema, name)
	return `CREATE TABLE IF NOT EXISTS ` + q + ` (
    id integer PRIMARY KEY,
    payload text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
)`
}

func insertChunkSQL(schema, name string, from, to, payloadBytes int) string {
	q := qualified(schema, name)
	payload := strings.Repeat("x", payloadBytes)
	lit := "'" + strings.ReplaceAll(payload, "'", "''") + "'"
	return fmt.Sprintf(
		`INSERT INTO %s (id, payload)
SELECT g, %s
FROM generate_series(%d, %d) AS g
ON CONFLICT (id) DO NOTHING`,
		q, lit, from, to,
	)
}

func analyzeSQL(schema, name string) string {
	return `ANALYZE ` + qualified(schema, name)
}

func countSQL(schema, name string) string {
	return `SELECT count(*) FROM ` + qualified(schema, name)
}

func throttleSessionsSQL(schema, name string, scale float64, costLimit int, costDelayMs int64) string {
	return fmt.Sprintf(
		`ALTER TABLE %s SET (
    autovacuum_vacuum_scale_factor = %s,
    autovacuum_vacuum_cost_limit = %d,
    autovacuum_vacuum_cost_delay = %d
)`,
		qualified(schema, name),
		strings.TrimRight(strings.TrimRight(strconv.FormatFloat(scale, 'f', 6, 64), "0"), "."),
		costLimit,
		costDelayMs,
	)
}

func friendlyEventsSQL(schema, name string, scale float64, threshold int64) string {
	return fmt.Sprintf(
		`ALTER TABLE %s SET (
    autovacuum_vacuum_scale_factor = %s,
    autovacuum_vacuum_threshold = %d
)`,
		qualified(schema, name),
		strings.TrimRight(strings.TrimRight(strconv.FormatFloat(scale, 'f', 6, 64), "0"), "."),
		threshold,
	)
}

func batchUpdateSQL(schema, name string, batch int) string {
	q := qualified(schema, name)
	return fmt.Sprintf(
		`UPDATE %s
SET payload = payload,
    updated_at = now()
WHERE id IN (
    SELECT id FROM %s ORDER BY random() LIMIT %d
)`,
		q, q, batch,
	)
}

func lockOneSQL(schema, name string) string {
	return `SELECT id FROM ` + qualified(schema, name) + ` ORDER BY id LIMIT 1 FOR UPDATE`
}

func chunks(total, size int) [][2]int {
	if total <= 0 || size <= 0 {
		return nil
	}
	out := make([][2]int, 0, (total+size-1)/size)
	for start := 1; start <= total; start += size {
		end := start + size - 1
		if end > total {
			end = total
		}
		out = append(out, [2]int{start, end})
	}
	return out
}
