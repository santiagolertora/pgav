package cli

import (
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/santiagolertora/pgav/internal/app"
	"github.com/santiagolertora/pgav/internal/domain"
)

func writeStatus(w io.Writer, reports []app.TableReport) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TABLE\tDEAD\tDEAD%\tRATE/H\tKEEP UP\tFREEZE\tRISK"); err != nil {
		return fmt.Errorf("cli status header: %w", err)
	}
	for i := range reports {
		r := reports[i]
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s",
			r.Snapshot.ID.String(),
			formatCount(r.Snapshot.DeadTuples),
			formatRatio(r.Assessment.DeadRatio),
			formatCount(int64(math.Round(r.Assessment.DeadRatePerHour))),
			formatKeepUp(r.Assessment.KeepingUp),
			formatRatio(r.Assessment.WraparoundRatio),
			r.Assessment.Risk.String(),
		)
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return fmt.Errorf("cli status row: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("cli status flush: %w", err)
	}
	return nil
}

func writeAnalyze(w io.Writer, rep app.AnalyzeReport) error {
	s := rep.Snapshot
	a := rep.Assessment
	rec := rep.Recommendation
	b := &strings.Builder{}
	fmt.Fprintf(b, "Table: %s    risk=%s\n\n", s.ID.String(), a.Risk.String())
	fmt.Fprintf(b, "Settings:\n")
	fmt.Fprintf(b, "  autovacuum_vacuum_scale_factor = %s\n", formatScale(s.Settings.ScaleFactor))
	fmt.Fprintf(b, "  autovacuum_vacuum_threshold    = %d\n", s.Settings.Threshold)
	fmt.Fprintf(b, "  autovacuum_vacuum_cost_limit   = %d\n", s.Settings.CostLimit)
	fmt.Fprintf(b, "  autovacuum_vacuum_cost_delay   = %s\n\n", formatDelay(s.Settings.CostDelay))
	fmt.Fprintf(b, "Race:\n")
	fmt.Fprintf(b, "  dead tuples     %s (%s of table)\n", formatCount(s.DeadTuples), formatRatio(a.DeadRatio))
	fmt.Fprintf(b, "  vacuum trigger  %s dead tuples\n", formatCount(a.TriggerAt))
	fmt.Fprintf(b, "  write rate      %s dead tuples/hour\n", formatCount(int64(math.Round(a.DeadRatePerHour))))
	fmt.Fprintf(b, "  est. reclaim    ~%s tuples/hour  (cost_limit/delay model, not IOPS)\n", formatCount(int64(math.Round(a.VacuumCapacityPerHour))))
	fmt.Fprintf(b, "  keeping up      %s\n", formatKeepUp(a.KeepingUp))
	fmt.Fprintf(b, "  time to trigger %s\n", formatHours(a.HoursToTrigger))
	fmt.Fprintf(b, "  freeze age      %s of freeze_max_age\n", formatRatio(a.WraparoundRatio))
	fmt.Fprintf(b, "  last vacuum     %s\n", formatAgo(s.LastCleanup()))
	fmt.Fprintf(b, "  last analyze    %s", formatAgo(s.LastStats()))
	if s.ModSinceAnalyze > 0 {
		fmt.Fprintf(b, "  (%s rows modified since)", formatCount(s.ModSinceAnalyze))
	}
	fmt.Fprintf(b, "\n\n")
	for i := range rep.Progress {
		if i == 0 {
			fmt.Fprintf(b, "VACUUM in progress:\n")
		}
		fmt.Fprintf(b, "  %s\n", domain.FormatProgress(rep.Progress[i]))
		if i == len(rep.Progress)-1 {
			fmt.Fprintln(b)
		}
	}
	if rec.Changed() {
		fmt.Fprintf(b, "Recommendation (dry-run):\n")
		writeSettingDiffs(b, rec)
		writeImpact(b, rec)
		fmt.Fprintf(b, "\nWhy:\n")
		for _, r := range rec.Reasons {
			fmt.Fprintf(b, "  - %s\n", r)
		}
		fmt.Fprintf(b, "\nSQL (not executed):\n%s\n\n", rec.AlterSQL())
		fmt.Fprintf(b, "Next:\n  pgav tune            # cluster dry-run\n  pgav tune --apply    # execute ALTER TABLE\n")
	} else {
		fmt.Fprintf(b, "Recommendation:\n  no change: vacuum can keep up with this table\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("cli analyze write: %w", err)
	}
	return nil
}

func writeTune(w io.Writer, rep app.TuneReport) error {
	b := &strings.Builder{}
	changed := 0
	for i := range rep.Recommendations {
		if rep.Recommendations[i].Changed() {
			changed++
		}
	}

	switch {
	case rep.Applied:
		fmt.Fprintf(b, "APPLIED    %d ALTER TABLE statement(s)\n\n", len(rep.Statements))
	case changed == 0:
		fmt.Fprintf(b, "DRY-RUN    no setting changes    nothing applied\n\n")
	default:
		fmt.Fprintf(b, "DRY-RUN    %d table(s) would change    nothing applied\n\n", changed)
	}

	if len(rep.Blockers) > 0 {
		fmt.Fprintf(b, "Blocked: idle transaction is holding xmin\n")
		fmt.Fprintf(b, "  Settings can change, but dead tuples will not be reclaimed until this ends.\n")
		for i := range rep.Blockers {
			x := rep.Blockers[i]
			for _, line := range strings.Split(domain.FormatXact(x), "\n") {
				fmt.Fprintf(b, "  %s\n", line)
			}
			fmt.Fprintf(b, "  %s\n", domain.TerminateSQL(x.PID))
		}
		fmt.Fprintln(b)
	}

	for i := range rep.Recommendations {
		rec := rep.Recommendations[i]
		if !rec.Changed() {
			continue
		}
		fmt.Fprintf(b, "%s\n", rec.Table.String())
		writeSettingDiffs(b, rec)
		writeImpact(b, rec)
		for _, reason := range rec.Reasons {
			fmt.Fprintf(b, "  why: %s\n", reason)
		}
		fmt.Fprintln(b)
	}

	if changed == 0 && len(rep.Blockers) == 0 {
		fmt.Fprintf(b, "If dead tuples keep rising, look for idle transactions:\n  pgav doctor\n")
	}

	if len(rep.Statements) > 0 {
		if rep.Applied {
			fmt.Fprintf(b, "SQL (executed)\n")
		} else {
			fmt.Fprintf(b, "SQL (not executed)\n")
		}
		for _, stmt := range rep.Statements {
			fmt.Fprintf(b, "%s\n\n", stmt)
		}
	}

	fmt.Fprintf(b, "Next:\n")
	for _, step := range tuneNextSteps(rep, changed) {
		fmt.Fprintf(b, "  %s\n", step)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("cli tune write: %w", err)
	}
	return nil
}

func writeSettingDiffs(b *strings.Builder, rec domain.Recommendation) {
	if rec.Current.ScaleFactor != rec.Proposed.ScaleFactor {
		fmt.Fprintf(b, "  scale_factor  %s -> %s\n", formatScale(rec.Current.ScaleFactor), formatScale(rec.Proposed.ScaleFactor))
	}
	if rec.Current.Threshold != rec.Proposed.Threshold {
		fmt.Fprintf(b, "  threshold     %d -> %d\n", rec.Current.Threshold, rec.Proposed.Threshold)
	}
	if rec.Current.CostLimit != rec.Proposed.CostLimit {
		fmt.Fprintf(b, "  cost_limit    %d -> %d\n", rec.Current.CostLimit, rec.Proposed.CostLimit)
	}
}

func writeImpact(b *strings.Builder, rec domain.Recommendation) {
	imp := rec.Impact
	if rec.Current.CostLimit != rec.Proposed.CostLimit {
		fmt.Fprintf(b, "  I/O budget     ×%.1f  (cost_limit %d -> %d; estimate, not IOPS)\n",
			imp.IOBudgetMultiple, rec.Current.CostLimit, rec.Proposed.CostLimit)
	}
	if imp.CurrentTrigger != imp.ProposedTrigger {
		fmt.Fprintf(b, "  trigger        %s -> %s dead tuples before vacuum fires\n",
			formatCount(imp.CurrentTrigger), formatCount(imp.ProposedTrigger))
	}
}

func tuneNextSteps(rep app.TuneReport, changed int) []string {
	steps := make([]string, 0, 3)
	if len(rep.Blockers) > 0 {
		steps = append(steps, domain.TerminateSQL(rep.Blockers[0].PID)+"  -- release xmin, then re-run")
	}
	if !rep.Applied && changed > 0 {
		steps = append(steps, "pgav tune --apply    # execute the SQL above")
	}
	steps = append(steps, "pgav doctor")
	return steps
}

func writeDoctor(w io.Writer, report domain.HealthReport) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Autovacuum Health: %d/%d    %d/%d tables OK\n\n",
		report.Score, report.MaxScore, report.OKCount, report.TotalCount)
	if len(report.Progress) > 0 {
		fmt.Fprintf(b, "VACUUM IN PROGRESS\n")
		for i := range report.Progress {
			fmt.Fprintf(b, "  %s\n", domain.FormatProgress(report.Progress[i]))
		}
		fmt.Fprintln(b)
	}
	order := []domain.RiskLevel{domain.RiskCritical, domain.RiskHigh, domain.RiskWarning}
	for _, sev := range order {
		for i := range report.Findings {
			f := report.Findings[i]
			if f.Severity != sev {
				continue
			}
			name := "cluster"
			if f.Table != nil {
				name = f.Table.String()
			}
			fmt.Fprintf(b, "%s  %s\n", sev.String(), name)
			fmt.Fprintf(b, "  %s\n", f.Summary)
			if a, ok := assessmentFor(report.Tables, f.Table); ok {
				fmt.Fprintf(b, "  dead %s (%s)  rate %s/h  est. reclaim ~%s/h  freeze %s  keep up %s\n",
					formatCount(a.DeadTuples),
					formatRatio(a.DeadRatio),
					formatCount(int64(math.Round(a.DeadRatePerHour))),
					formatCount(int64(math.Round(a.VacuumCapacityPerHour))),
					formatRatio(a.WraparoundRatio),
					formatKeepUp(a.KeepingUp),
				)
			}
			if f.Detail != "" && f.Detail != f.Summary {
				for _, line := range strings.Split(f.Detail, "\n") {
					fmt.Fprintf(b, "  %s\n", line)
				}
			}
			fmt.Fprintln(b)
		}
	}
	fmt.Fprintf(b, "Next:\n")
	for _, step := range nextSteps(report) {
		fmt.Fprintf(b, "  %s\n", step)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("cli doctor write: %w", err)
	}
	return nil
}

func assessmentFor(tables []domain.RaceAssessment, id *domain.TableID) (domain.RaceAssessment, bool) {
	if id == nil {
		return domain.RaceAssessment{}, false
	}
	for i := range tables {
		if tables[i].Table == *id {
			return tables[i], true
		}
	}
	return domain.RaceAssessment{}, false
}

func nextSteps(report domain.HealthReport) []string {
	steps := make([]string, 0, 3)
	var analyze string
	hasXact := false
	hasTune := false
	var freeze string
	for i := range report.Findings {
		f := report.Findings[i]
		if f.Code == domain.FindingLongTransaction {
			hasXact = true
			continue
		}
		if f.Code == domain.FindingWraparound && f.Table != nil && freeze == "" {
			freeze = f.Table.String()
		}
		if f.Table != nil {
			hasTune = true
			if analyze == "" {
				analyze = f.Table.String()
			}
		}
	}
	if hasXact {
		steps = append(steps, "Terminate idle transactions first. They block vacuum on every table.")
	}
	if freeze != "" {
		steps = append(steps, "VACUUM (FREEZE) "+freeze+"  -- wraparound; do not wait on autovacuum")
	}
	if analyze != "" {
		steps = append(steps, "pgav analyze "+analyze)
	}
	if hasTune {
		steps = append(steps, "pgav tune            # dry-run; nothing is applied")
	}
	if len(steps) == 0 {
		steps = append(steps, "No action. Re-check after the next traffic peak.")
	}
	return steps
}

func formatKeepUp(ok bool) string {
	if ok {
		return "yes"
	}
	return "NO"
}

func formatCount(n int64) string {
	if n < 0 {
		n = 0
	}
	abs := float64(n)
	switch {
	case abs >= 1_000_000_000:
		return trimFloat(abs/1_000_000_000) + "B"
	case abs >= 1_000_000:
		return trimFloat(abs/1_000_000) + "M"
	case abs >= 1_000:
		return trimFloat(abs/1_000) + "K"
	default:
		return fmt.Sprintf("%d", n)
	}
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func formatRatio(r float64) string {
	return fmt.Sprintf("%.0f%%", r*100)
}

func formatScale(v float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

func formatHours(h float64) string {
	if h < 0 {
		return "n/a"
	}
	if h == 0 {
		return "due now"
	}
	return fmt.Sprintf("%.1f hours", h)
}

func formatDelay(d time.Duration) string {
	if d <= 0 {
		return "n/a"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return domain.CompactAge(d)
}

func formatAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return domain.CompactAge(time.Since(t)) + " ago"
}
