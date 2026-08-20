package domain

import (
	"fmt"
	"math"
	"strings"
)

// TunerParams are recommendation bounds supplied by config.
type TunerParams struct {
	MaxHoursBetweenVacuum float64
	MinScaleFactor        float64
	MaxScaleFactor        float64
	MinThreshold          int64
	MaxThreshold          int64
	CostLimitBump         int
	MinCostLimit          int
	MaxCostLimit          int
	CostLimitHeadroom     float64
	LargeTableRelTuples   float64
	ScaleDecimals         int
}

// ImpactEstimate is the predicted effect of applying a recommendation.
type ImpactEstimate struct {
	MaxDeadTuplesChangePct  float64
	VacuumFrequencyChange   float64
	AdditionalIOPct         float64
	CurrentTrigger          int64
	ProposedTrigger         int64
	CurrentCapacityPerHour  float64
	ProposedCapacityPerHour float64
	IOBudgetMultiple        float64
}

// Recommendation is a proposed settings change for one table.
type Recommendation struct {
	Table    TableID
	Current  VacuumSettings
	Proposed VacuumSettings
	Reasons  []string
	Impact   ImpactEstimate
}

// Changed reports whether any setting differs.
func (r Recommendation) Changed() bool {
	return r.Current.ScaleFactor != r.Proposed.ScaleFactor ||
		r.Current.Threshold != r.Proposed.Threshold ||
		r.Current.CostLimit != r.Proposed.CostLimit ||
		r.Current.CostDelay != r.Proposed.CostDelay
}

// AlterSQL renders a single ALTER TABLE ... SET (...) statement.
func (r Recommendation) AlterSQL() string {
	parts := make([]string, 0, 3)
	if r.Current.ScaleFactor != r.Proposed.ScaleFactor {
		parts = append(parts, fmt.Sprintf("autovacuum_vacuum_scale_factor = %s", formatScale(r.Proposed.ScaleFactor)))
	}
	if r.Current.Threshold != r.Proposed.Threshold {
		parts = append(parts, fmt.Sprintf("autovacuum_vacuum_threshold = %d", r.Proposed.Threshold))
	}
	if r.Current.CostLimit != r.Proposed.CostLimit {
		parts = append(parts, fmt.Sprintf("autovacuum_vacuum_cost_limit = %d", r.Proposed.CostLimit))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("ALTER TABLE %s SET (\n    %s\n);", r.Table.Qualified(), strings.Join(parts, ",\n    "))
}

func formatScale(v float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// Recommend computes settings so the trigger fires within MaxHoursBetweenVacuum
// and, if vacuum is losing, raises the I/O budget to match the write rate.
func Recommend(snap TableSnapshot, assessment RaceAssessment, p TunerParams) Recommendation {
	current := snap.Settings
	if assessment.Risk == RiskOK {
		return Recommendation{
			Table:    snap.ID,
			Current:  current,
			Proposed: current,
			Reasons:  []string{"no change"},
		}
	}
	proposed := current
	reasons := make([]string, 0, 3)

	reltuples := snap.RelTuples
	if reltuples <= 0 {
		reltuples = float64(snap.LiveTuples)
	}

	if !assessment.KeepingUp {
		need := neededCostLimit(current.CostLimit, assessment.VacuumCapacityPerHour, assessment.DeadRatePerHour, p)
		if need > proposed.CostLimit {
			proposed.CostLimit = need
			reasons = append(reasons, "vacuum cannot keep up with the write rate; raise cost_limit so reclaim can match writes")
		}
	}

	if reltuples > 0 && assessment.DeadRatePerHour > 0 {
		targetTrigger := assessment.DeadRatePerHour * p.MaxHoursBetweenVacuum
		if targetTrigger < 1 {
			targetTrigger = 1
		}
		sf := (targetTrigger - float64(current.Threshold)) / reltuples
		sf = clamp(sf, p.MinScaleFactor, p.MaxScaleFactor)
		sf = roundScale(sf, p.ScaleDecimals)
		late := assessment.HoursToTrigger > p.MaxHoursBetweenVacuum ||
			(assessment.HoursToTrigger == 0 && assessment.DeadRatio > 0)
		if late && sf != roundScale(current.ScaleFactor, p.ScaleDecimals) {
			if p.LargeTableRelTuples > 0 && reltuples >= p.LargeTableRelTuples && sf <= p.MinScaleFactor {
				th := int64(math.Round(targetTrigger))
				if th < p.MinThreshold {
					th = p.MinThreshold
				}
				if th > p.MaxThreshold {
					th = p.MaxThreshold
				}
				proposed.ScaleFactor = 0
				proposed.Threshold = th
				reasons = append(reasons, "large table: use an absolute vacuum threshold instead of a percentage scale factor")
			} else {
				proposed.ScaleFactor = sf
				reasons = append(reasons, "scale factor lets dead tuples pile up longer than the target vacuum interval")
			}
		}
	}

	rec := Recommendation{
		Table:    snap.ID,
		Current:  current,
		Proposed: proposed,
		Reasons:  reasons,
		Impact:   estimateImpact(current, proposed, reltuples, assessment),
	}
	if !rec.Changed() {
		rec.Reasons = []string{"no change"}
	}
	return rec
}

func neededCostLimit(current int, cap, rate float64, p TunerParams) int {
	headroom := p.CostLimitHeadroom
	if headroom < 1 {
		headroom = 1
	}
	raw := 0.0
	if current > 0 && cap > 0 && rate > 0 {
		per := cap / float64(current)
		if per > 0 {
			raw = rate * headroom / per
		}
	}
	if raw <= 0 {
		raw = float64(current + p.CostLimitBump)
	}
	n := int(math.Ceil(raw))
	min := p.MinCostLimit
	if min <= 0 {
		min = 1
	}
	if n < min {
		n = min
	}
	n = roundUpTo(n, min)
	if p.MaxCostLimit > 0 && n > p.MaxCostLimit {
		n = p.MaxCostLimit
	}
	if n <= current {
		n = roundUpTo(current+min, min)
		if p.MaxCostLimit > 0 && n > p.MaxCostLimit {
			n = p.MaxCostLimit
		}
	}
	return n
}

func roundUpTo(n, quantum int) int {
	if quantum <= 0 {
		return n
	}
	if n%quantum == 0 {
		return n
	}
	return ((n / quantum) + 1) * quantum
}

func estimateImpact(current, proposed VacuumSettings, reltuples float64, a RaceAssessment) ImpactEstimate {
	oldTrigger := float64(current.TriggerDeadTuples(reltuples))
	newTrigger := float64(proposed.TriggerDeadTuples(reltuples))
	deadChange := 0.0
	if oldTrigger > 0 {
		deadChange = (newTrigger - oldTrigger) / oldTrigger * 100
	}
	freq := 0.0
	if newTrigger > 0 && oldTrigger > 0 {
		freq = oldTrigger / newTrigger
	}
	io := 0.0
	if current.CostLimit > 0 {
		io = float64(proposed.CostLimit-current.CostLimit) / float64(current.CostLimit) * 100
	}
	proposedCap := a.VacuumCapacityPerHour
	if current.CostLimit > 0 && proposed.CostLimit != current.CostLimit {
		proposedCap = a.VacuumCapacityPerHour * float64(proposed.CostLimit) / float64(current.CostLimit)
	}
	mult := 0.0
	if current.CostLimit > 0 {
		mult = float64(proposed.CostLimit) / float64(current.CostLimit)
	}
	return ImpactEstimate{
		MaxDeadTuplesChangePct:  deadChange,
		VacuumFrequencyChange:   freq,
		AdditionalIOPct:         io,
		CurrentTrigger:          int64(oldTrigger),
		ProposedTrigger:         int64(newTrigger),
		CurrentCapacityPerHour:  a.VacuumCapacityPerHour,
		ProposedCapacityPerHour: proposedCap,
		IOBudgetMultiple:        mult,
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func roundScale(v float64, decimals int) float64 {
	if decimals < 0 {
		return v
	}
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}
