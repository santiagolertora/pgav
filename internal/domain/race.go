package domain

import "time"

// AssessParams are risk and capacity knobs supplied by config. No defaults live here.
type AssessParams struct {
	HighDeadRatio             float64
	CriticalDeadRatio         float64
	HighHoursToTrigger        float64
	WraparoundWarningRatio    float64
	WraparoundCriticalRatio   float64
	AssumedCostPerPage        float64
	AssumedTuplesPerPage      float64
	IOMultiplier              float64
	MinTriggerForScaleWarning int64
}

// RaceAssessment is the per-table race between dead-tuple generation and vacuum reclaim.
type RaceAssessment struct {
	Table                 TableID
	DeadTuples            int64
	DeadRatio             float64
	DeadRatePerHour       float64
	TriggerAt             int64
	HoursToTrigger        float64
	VacuumCapacityPerHour float64
	KeepingUp             bool
	WraparoundRatio       float64
	FrozenXIDAge          int64
	Risk                  RiskLevel
	Reasons               []string
}

// DeadRatio is dead / (live + dead).
func DeadRatio(live, dead int64) float64 {
	total := live + dead
	if total <= 0 {
		return 0
	}
	return float64(dead) / float64(total)
}

// DeadRatePerHour is dead tuples generated per hour over the stats window.
func DeadRatePerHour(deadGenerated int64, window time.Duration) float64 {
	if window <= 0 || deadGenerated <= 0 {
		return 0
	}
	hours := window.Hours()
	if hours <= 0 {
		return 0
	}
	return float64(deadGenerated) / hours
}

// VacuumCapacityPerHour estimates tuples one worker can reclaim under cost throttling.
func VacuumCapacityPerHour(costLimit int, costDelay time.Duration, costPerPage, tuplesPerPage, ioMultiplier float64) float64 {
	if costLimit <= 0 || costDelay <= 0 || costPerPage <= 0 || tuplesPerPage <= 0 {
		return 0
	}
	if ioMultiplier < 0 {
		ioMultiplier = 0
	}
	cycle := costDelay.Seconds() * (1 + ioMultiplier)
	if cycle <= 0 {
		return 0
	}
	cyclesPerHour := time.Hour.Seconds() / cycle
	pagesPerCycle := float64(costLimit) / costPerPage
	return cyclesPerHour * pagesPerCycle * tuplesPerPage
}

// HoursToTrigger is remaining dead tuples until the trigger, divided by dead rate.
// Negative remaining yields 0 (already due). Zero rate yields +Inf encoded as -1.
func HoursToTrigger(deadTuples, trigger int64, deadRatePerHour float64) float64 {
	remaining := trigger - deadTuples
	if remaining <= 0 {
		return 0
	}
	if deadRatePerHour <= 0 {
		return -1
	}
	return float64(remaining) / deadRatePerHour
}

// WraparoundRatio is frozen xid age / freeze_max_age.
func WraparoundRatio(frozenXIDAge, freezeMaxAge int64) float64 {
	if freezeMaxAge <= 0 || frozenXIDAge <= 0 {
		return 0
	}
	return float64(frozenXIDAge) / float64(freezeMaxAge)
}

// Assess computes the race for one table.
func Assess(snap TableSnapshot, cluster ClusterSettings, p AssessParams) RaceAssessment {
	reltuples := snap.RelTuples
	if reltuples < 0 {
		reltuples = 0
	}
	a := RaceAssessment{
		Table:                 snap.ID,
		DeadRatio:             DeadRatio(snap.LiveTuples, snap.DeadTuples),
		DeadTuples:            snap.DeadTuples,
		DeadRatePerHour:       DeadRatePerHour(snap.DeadGenerated(), snap.StatsWindow),
		TriggerAt:             snap.Settings.TriggerDeadTuples(reltuples),
		VacuumCapacityPerHour: VacuumCapacityPerHour(snap.Settings.CostLimit, snap.Settings.CostDelay, p.AssumedCostPerPage, p.AssumedTuplesPerPage, p.IOMultiplier),
		WraparoundRatio:       WraparoundRatio(snap.FrozenXIDAge, cluster.FreezeMaxAge),
		FrozenXIDAge:          snap.FrozenXIDAge,
		Reasons:               make([]string, 0, 4),
	}
	a.HoursToTrigger = HoursToTrigger(snap.DeadTuples, a.TriggerAt, a.DeadRatePerHour)
	a.KeepingUp = a.DeadRatePerHour <= a.VacuumCapacityPerHour || a.DeadRatePerHour == 0
	a.Risk, a.Reasons = classify(a, p)
	return a
}

func classify(a RaceAssessment, p AssessParams) (RiskLevel, []string) {
	reasons := make([]string, 0, 4)
	risk := RiskOK

	raise := func(level RiskLevel, reason string) {
		reasons = append(reasons, reason)
		if level > risk {
			risk = level
		}
	}

	switch {
	case a.WraparoundRatio >= p.WraparoundCriticalRatio:
		raise(RiskCritical, "freeze age is approaching wraparound")
	case a.WraparoundRatio >= p.WraparoundWarningRatio:
		raise(RiskWarning, "freeze age is high")
	}

	if !a.KeepingUp {
		raise(RiskCritical, "vacuum cannot keep up with the write workload")
	}

	switch {
	case a.DeadRatio >= p.CriticalDeadRatio:
		raise(RiskCritical, "dead tuple ratio is critical")
	case a.DeadRatio >= p.HighDeadRatio:
		raise(RiskHigh, "dead tuple ratio is high")
	}

	switch {
	case a.HoursToTrigger < 0:
		// unknown rate; do not flag scale factor
	case a.HoursToTrigger == 0 && a.DeadRatio >= p.HighDeadRatio:
		raise(RiskHigh, "vacuum trigger already exceeded")
	case a.HoursToTrigger > p.HighHoursToTrigger && a.DeadRatePerHour > 0 && a.TriggerAt >= p.MinTriggerForScaleWarning:
		raise(RiskHigh, "scale factor is too large for the write rate")
	}

	if risk == RiskOK && len(reasons) == 0 {
		reasons = append(reasons, "vacuum is keeping up")
	}
	return risk, reasons
}
