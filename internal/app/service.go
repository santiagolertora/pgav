// Package app runs status, analyze, tune, and doctor.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/santiagolertora/pgav/internal/config"
	"github.com/santiagolertora/pgav/internal/domain"
)

// Catalog reads PostgreSQL stats and applies ALTER TABLE.
type Catalog interface {
	ClusterSettings(ctx context.Context) (domain.ClusterSettings, error)
	TableSnapshots(ctx context.Context) ([]domain.TableSnapshot, error)
	TableSnapshot(ctx context.Context, id domain.TableID) (domain.TableSnapshot, error)
	LongTransactions(ctx context.Context, olderThan time.Duration) ([]domain.LongTransaction, error)
	VacuumProgress(ctx context.Context) ([]domain.VacuumProgress, error)
	Apply(ctx context.Context, statements []string) error
	Close(ctx context.Context) error
}

// Service runs status, analyze, tune, and doctor.
type Service struct {
	catalog Catalog
	cfg     config.Config
	logger  *slog.Logger
}

// New constructs a Service.
func New(catalog Catalog, cfg config.Config, logger *slog.Logger) (*Service, error) {
	if catalog == nil {
		return nil, fmt.Errorf("app: catalog is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("app: logger is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	return &Service{catalog: catalog, cfg: cfg, logger: logger}, nil
}

// TableReport is a snapshot plus its race assessment.
type TableReport struct {
	Snapshot   domain.TableSnapshot
	Assessment domain.RaceAssessment
}

// AnalyzeReport is one table plus a recommendation.
type AnalyzeReport struct {
	TableReport
	Recommendation domain.Recommendation
	Progress       []domain.VacuumProgress
}

// TuneReport is a cluster-wide recommendation set.
type TuneReport struct {
	Recommendations []domain.Recommendation
	Statements      []string
	Applied         bool
	Blockers        []domain.LongTransaction
}

func (s *Service) assessParams() domain.AssessParams {
	return domain.AssessParams{
		HighDeadRatio:             s.cfg.Assess.HighDeadRatio,
		CriticalDeadRatio:         s.cfg.Assess.CriticalDeadRatio,
		HighHoursToTrigger:        s.cfg.Assess.HighHoursToTrigger,
		WraparoundWarningRatio:    s.cfg.Assess.WraparoundWarningRatio,
		WraparoundCriticalRatio:   s.cfg.Assess.WraparoundCriticalRatio,
		AssumedCostPerPage:        s.cfg.Assess.AssumedCostPerPage,
		AssumedTuplesPerPage:      s.cfg.Assess.AssumedTuplesPerPage,
		IOMultiplier:              s.cfg.Assess.IOMultiplier,
		MinTriggerForScaleWarning: s.cfg.Assess.MinTriggerForScaleWarning,
	}
}

func (s *Service) tunerParams() domain.TunerParams {
	return domain.TunerParams{
		MaxHoursBetweenVacuum: s.cfg.Tuner.MaxHoursBetweenVacuum,
		MinScaleFactor:        s.cfg.Tuner.MinScaleFactor,
		MaxScaleFactor:        s.cfg.Tuner.MaxScaleFactor,
		MinThreshold:          s.cfg.Tuner.MinThreshold,
		MaxThreshold:          s.cfg.Tuner.MaxThreshold,
		CostLimitBump:         s.cfg.Tuner.CostLimitBump,
		MinCostLimit:          s.cfg.Tuner.MinCostLimit,
		MaxCostLimit:          s.cfg.Tuner.MaxCostLimit,
		CostLimitHeadroom:     s.cfg.Tuner.CostLimitHeadroom,
		LargeTableRelTuples:   float64(s.cfg.Assess.MinTriggerForScaleWarning),
		ScaleDecimals:         s.cfg.Tuner.ScaleDecimals,
	}
}

func (s *Service) doctorParams() domain.DoctorParams {
	return domain.DoctorParams{
		MaxScore:          s.cfg.Doctor.MaxScore,
		MinScore:          s.cfg.Doctor.MinScore,
		CriticalPenalty:   s.cfg.Doctor.CriticalPenalty,
		HighPenalty:       s.cfg.Doctor.HighPenalty,
		WarningPenalty:    s.cfg.Doctor.WarningPenalty,
		LongXactPenalty:   s.cfg.Doctor.LongXactPenalty,
		WraparoundPenalty: s.cfg.Doctor.WraparoundPenalty,
	}
}

func (s *Service) reportAll(ctx context.Context) (domain.ClusterSettings, []TableReport, error) {
	cluster, err := s.catalog.ClusterSettings(ctx)
	if err != nil {
		return domain.ClusterSettings{}, nil, fmt.Errorf("cluster settings: %w", err)
	}
	snaps, err := s.catalog.TableSnapshots(ctx)
	if err != nil {
		return domain.ClusterSettings{}, nil, fmt.Errorf("table snapshots: %w", err)
	}
	reports := make([]TableReport, 0, len(snaps))
	params := s.assessParams()
	for i := range snaps {
		reports = append(reports, TableReport{
			Snapshot:   snaps[i],
			Assessment: domain.Assess(snaps[i], cluster, params),
		})
	}
	return cluster, reports, nil
}

// Status assesses every user table.
func (s *Service) Status(ctx context.Context) ([]TableReport, error) {
	_, reports, err := s.reportAll(ctx)
	if err != nil {
		return nil, err
	}
	s.logger.Info("status complete", "tables", len(reports))
	return reports, nil
}

// Analyze assesses one table and proposes a change.
func (s *Service) Analyze(ctx context.Context, rawName string) (AnalyzeReport, error) {
	id, err := domain.ParseTableID(rawName)
	if err != nil {
		return AnalyzeReport{}, fmt.Errorf("analyze table: %w", err)
	}
	cluster, err := s.catalog.ClusterSettings(ctx)
	if err != nil {
		return AnalyzeReport{}, fmt.Errorf("cluster settings: %w", err)
	}
	snap, err := s.catalog.TableSnapshot(ctx, id)
	if err != nil {
		return AnalyzeReport{}, fmt.Errorf("table snapshot: %w", err)
	}
	assessment := domain.Assess(snap, cluster, s.assessParams())
	rec := domain.Recommend(snap, assessment, s.tunerParams())
	progress, err := s.catalog.VacuumProgress(ctx)
	if err != nil {
		return AnalyzeReport{}, fmt.Errorf("vacuum progress: %w", err)
	}
	mine := make([]domain.VacuumProgress, 0)
	for i := range progress {
		if progress[i].Table == id {
			mine = append(mine, progress[i])
		}
	}
	s.logger.Info("analyze complete", "table", id.String(), "risk", assessment.Risk.String())
	return AnalyzeReport{
		TableReport:    TableReport{Snapshot: snap, Assessment: assessment},
		Recommendation: rec,
		Progress:       mine,
	}, nil
}

// Tune computes recommendations. When apply is true, DDL is executed.
func (s *Service) Tune(ctx context.Context, apply bool) (TuneReport, error) {
	_, reports, err := s.reportAll(ctx)
	if err != nil {
		return TuneReport{}, err
	}
	recs := make([]domain.Recommendation, 0, len(reports))
	stmts := make([]string, 0)
	for i := range reports {
		rec := domain.Recommend(reports[i].Snapshot, reports[i].Assessment, s.tunerParams())
		recs = append(recs, rec)
		if rec.Changed() {
			stmts = append(stmts, rec.AlterSQL())
		}
	}
	xacts, err := s.catalog.LongTransactions(ctx, s.cfg.Doctor.LongXactAfter)
	if err != nil {
		return TuneReport{}, fmt.Errorf("long transactions: %w", err)
	}
	applied := false
	if apply && len(stmts) > 0 {
		if err := s.catalog.Apply(ctx, stmts); err != nil {
			return TuneReport{}, fmt.Errorf("apply: %w", err)
		}
		applied = true
		s.logger.Info("tune applied", "statements", len(stmts))
	} else {
		s.logger.Info("tune dry-run", "changes", len(stmts))
	}
	return TuneReport{
		Recommendations: recs,
		Statements:      stmts,
		Applied:         applied,
		Blockers:        xacts,
	}, nil
}

// Doctor scores cluster autovacuum health.
func (s *Service) Doctor(ctx context.Context) (domain.HealthReport, error) {
	cluster, reports, err := s.reportAll(ctx)
	if err != nil {
		return domain.HealthReport{}, err
	}
	xacts, err := s.catalog.LongTransactions(ctx, s.cfg.Doctor.LongXactAfter)
	if err != nil {
		return domain.HealthReport{}, fmt.Errorf("long transactions: %w", err)
	}
	progress, err := s.catalog.VacuumProgress(ctx)
	if err != nil {
		return domain.HealthReport{}, fmt.Errorf("vacuum progress: %w", err)
	}
	assessments := make([]domain.RaceAssessment, 0, len(reports))
	for i := range reports {
		assessments = append(assessments, reports[i].Assessment)
	}
	report := domain.Doctor(cluster, assessments, xacts, progress, s.doctorParams())
	s.logger.Info("doctor complete", "score", report.Score, "findings", len(report.Findings))
	return report, nil
}
