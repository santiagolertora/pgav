// Package catalogfake is an in-memory Catalog used by tests.
package catalogfake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/santiagolertora/pgav/internal/domain"
)

// Catalog stores snapshots in memory and records applied DDL.
type Catalog struct {
	Cluster     domain.ClusterSettings
	Snapshots   []domain.TableSnapshot
	Xacts       []domain.LongTransaction
	Progress    []domain.VacuumProgress
	Applied     []string
	FailApply   bool
	FailCluster bool
}

// ClusterSettings returns cluster autovacuum settings.
func (c *Catalog) ClusterSettings(ctx context.Context) (domain.ClusterSettings, error) {
	if err := ctx.Err(); err != nil {
		return domain.ClusterSettings{}, fmt.Errorf("catalogfake cluster settings: %w", err)
	}
	if c.FailCluster {
		return domain.ClusterSettings{}, fmt.Errorf("catalogfake: cluster settings unavailable")
	}
	return c.Cluster, nil
}

// TableSnapshots returns all snapshots.
func (c *Catalog) TableSnapshots(ctx context.Context) ([]domain.TableSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("catalogfake table snapshots: %w", err)
	}
	out := make([]domain.TableSnapshot, len(c.Snapshots))
	copy(out, c.Snapshots)
	return out, nil
}

// TableSnapshot returns one table.
func (c *Catalog) TableSnapshot(ctx context.Context, id domain.TableID) (domain.TableSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.TableSnapshot{}, fmt.Errorf("catalogfake table snapshot: %w", err)
	}
	for i := range c.Snapshots {
		if c.Snapshots[i].ID == id {
			return c.Snapshots[i], nil
		}
	}
	return domain.TableSnapshot{}, fmt.Errorf("catalogfake: table %s not found", id.String())
}

// LongTransactions returns open transactions older than olderThan. The fake ignores the cutoff and returns stored rows.
func (c *Catalog) LongTransactions(ctx context.Context, olderThan time.Duration) ([]domain.LongTransaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("catalogfake long transactions: %w", err)
	}
	_ = olderThan
	out := make([]domain.LongTransaction, len(c.Xacts))
	copy(out, c.Xacts)
	return out, nil
}

// VacuumProgress returns running vacuums stored on the fake.
func (c *Catalog) VacuumProgress(ctx context.Context) ([]domain.VacuumProgress, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("catalogfake vacuum progress: %w", err)
	}
	out := make([]domain.VacuumProgress, len(c.Progress))
	copy(out, c.Progress)
	return out, nil
}

// Apply records statements. It rejects anything that is not ALTER TABLE.
func (c *Catalog) Apply(ctx context.Context, statements []string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("catalogfake apply: %w", err)
	}
	if c.FailApply {
		return fmt.Errorf("catalogfake: apply failed")
	}
	for _, stmt := range statements {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(stmt)), "ALTER TABLE") {
			return fmt.Errorf("catalogfake: refusing non-alter statement")
		}
		c.Applied = append(c.Applied, stmt)
	}
	return nil
}

// Close implements Catalog.
func (c *Catalog) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("catalogfake close: %w", err)
	}
	return nil
}
