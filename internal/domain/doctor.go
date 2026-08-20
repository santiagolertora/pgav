package domain

import "fmt"

// DoctorParams are health-score knobs supplied by config.
type DoctorParams struct {
	MaxScore          int
	MinScore          int
	CriticalPenalty   int
	HighPenalty       int
	WarningPenalty    int
	LongXactPenalty   int
	WraparoundPenalty int
}

// HealthReport is the cluster autovacuum diagnosis.
type HealthReport struct {
	Score      int
	MaxScore   int
	Findings   []Finding
	Tables     []RaceAssessment
	OKCount    int
	TotalCount int
	Progress   []VacuumProgress
}

// Doctor builds findings and a score from assessments and blockers.
func Doctor(cluster ClusterSettings, tables []RaceAssessment, xacts []LongTransaction, progress []VacuumProgress, p DoctorParams) HealthReport {
	findings := make([]Finding, 0, len(tables)+len(xacts)+1)
	ok := 0

	if !cluster.AutovacuumEnabled {
		findings = append(findings, Finding{
			Severity: RiskCritical,
			Code:     FindingAutovacuumDisabled,
			Summary:  "autovacuum is disabled",
			Detail:   "PostgreSQL will not reclaim dead tuples automatically",
		})
	}

	for i := range tables {
		t := tables[i]
		if t.Risk == RiskOK {
			ok++
			continue
		}
		findings = append(findings, findingFromAssessment(t))
	}

	for i := range xacts {
		x := xacts[i]
		findings = append(findings, Finding{
			Severity: RiskWarning,
			Code:     FindingLongTransaction,
			Summary:  "idle transaction is holding xmin and blocking vacuum on every table",
			Detail:   FormatXact(x) + "\n" + TerminateSQL(x.PID),
		})
	}

	score := p.MaxScore
	for i := range findings {
		score -= penalty(findings[i], p)
	}
	if score > p.MaxScore {
		score = p.MaxScore
	}
	if score < p.MinScore {
		score = p.MinScore
	}

	return HealthReport{
		Score:      score,
		Findings:   findings,
		Tables:     tables,
		OKCount:    ok,
		TotalCount: len(tables),
		MaxScore:   p.MaxScore,
		Progress:   progress,
	}
}

func penalty(f Finding, p DoctorParams) int {
	switch f.Code {
	case FindingWraparound:
		return p.WraparoundPenalty
	case FindingLongTransaction:
		return p.LongXactPenalty
	case FindingAutovacuumDisabled:
		return p.CriticalPenalty
	default:
		switch f.Severity {
		case RiskCritical:
			return p.CriticalPenalty
		case RiskHigh:
			return p.HighPenalty
		case RiskWarning:
			return p.WarningPenalty
		default:
			return 0
		}
	}
}

func findingFromAssessment(a RaceAssessment) Finding {
	id := a.Table
	summary := "table needs attention"
	if len(a.Reasons) > 0 {
		summary = a.Reasons[0]
	}
	code := FindingHighDeadRatio
	detail := ""
	switch {
	case hasReason(a.Reasons, "freeze age is approaching wraparound"),
		hasReason(a.Reasons, "freeze age is high"):
		code = FindingWraparound
		if hasReason(a.Reasons, "freeze age is approaching wraparound") {
			summary = "freeze age is approaching wraparound"
		} else {
			summary = "freeze age is high"
		}
		detail = fmt.Sprintf("freeze age %.0f%% of autovacuum_freeze_max_age", a.WraparoundRatio*100)
	case !a.KeepingUp:
		code = FindingVacuumCannotKeepUp
		summary = "vacuum cannot keep up with the write workload"
	case hasReason(a.Reasons, "scale factor is too large for the write rate"):
		code = FindingScaleFactorTooLarge
	}
	return Finding{
		Severity: a.Risk,
		Table:    &id,
		Code:     code,
		Summary:  summary,
		Detail:   detail,
	}
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
