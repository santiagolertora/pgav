package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show autovacuum risk for every user table",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := rt.prepare(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			reports, err := svc.Status(cmd.Context())
			if err != nil {
				return fmt.Errorf("cli status: %w", err)
			}
			if err := writeStatus(rt.opts.Stdout, reports); err != nil {
				return fmt.Errorf("cli status render: %w", err)
			}
			return nil
		},
	}
}

func newAnalyzeCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "analyze TABLE",
		Short: "Explain why a table's autovacuum settings win or lose the race",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := rt.prepare(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			rep, err := svc.Analyze(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("cli analyze: %w", err)
			}
			if err := writeAnalyze(rt.opts.Stdout, rep); err != nil {
				return fmt.Errorf("cli analyze render: %w", err)
			}
			return nil
		},
	}
}

func newTuneCmd(rt *runtime) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "tune",
		Short: "Dry-run per-table autovacuum changes (use --apply to execute)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := rt.prepare(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			rep, err := svc.Tune(cmd.Context(), apply)
			if err != nil {
				return fmt.Errorf("cli tune: %w", err)
			}
			if err := writeTune(rt.opts.Stdout, rep); err != nil {
				return fmt.Errorf("cli tune render: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute ALTER TABLE statements (default is dry-run)")
	return cmd
}

func newDoctorCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Score cluster autovacuum health and list findings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := rt.prepare(cmd)
			if err != nil {
				return err
			}
			defer cleanup()
			rep, err := svc.Doctor(cmd.Context())
			if err != nil {
				return fmt.Errorf("cli doctor: %w", err)
			}
			if err := writeDoctor(rt.opts.Stdout, rep); err != nil {
				return fmt.Errorf("cli doctor render: %w", err)
			}
			return nil
		},
	}
}
