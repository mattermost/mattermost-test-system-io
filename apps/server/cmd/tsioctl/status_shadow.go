package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

type statusShadowFlags struct {
	limit       int
	repo        string
	format      string
	all         bool
	databaseURL string
}

func newStatusShadowCmd() *cobra.Command {
	var flags statusShadowFlags
	cmd := &cobra.Command{
		Use:   "status-shadow",
		Short: "Compare legacy vs shared test status and pass-rate rules on recent runs",
		Long: `Run read-only shadow comparison queries across recent orchestration_runs
and report_groups, reporting exact deltas between TSIO_STATUS_RULE=legacy
and TSIO_STATUS_RULE=shared without modifying any rows or scanning suites/test_cases.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateStatusShadowFlags(flags); err != nil {
				return err
			}
			dsn, err := resolveShadowDatabaseURL(flags.databaseURL, os.Getenv, config.LoadDatabaseURL)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			pool, err := db.NewPool(ctx, dsn)
			if err != nil {
				return err
			}
			defer pool.Close()

			report, err := reports.CompareStatusRules(ctx, pool, reports.StatusShadowOpts{
				Limit:      flags.limit,
				Repository: strings.TrimSpace(flags.repo),
			})
			if err != nil {
				return err
			}
			return renderStatusShadowReport(cmd.OutOrStdout(), report, flags.format, flags.all)
		},
	}
	cmd.Flags().IntVar(&flags.limit, "limit", 50, "Maximum recent orchestration runs and report groups to inspect (1..500)")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Optional exact repository filter (e.g. mattermost/mattermost)")
	cmd.Flags().StringVar(&flags.format, "format", "table", "Output format: table | json")
	cmd.Flags().BoolVar(&flags.all, "all", false, "Include unchanged rows in table output")
	cmd.Flags().StringVar(&flags.databaseURL, "database-url", "", "PostgreSQL connection URL override")
	return cmd
}

func validateStatusShadowFlags(flags statusShadowFlags) error {
	if flags.limit < 1 || flags.limit > 500 {
		return fmt.Errorf("--limit must be in [1, 500], got %d", flags.limit)
	}
	switch flags.format {
	case "table", "json":
		return nil
	default:
		return fmt.Errorf("--format must be table or json, got %q", flags.format)
	}
}

func resolveShadowDatabaseURL(
	flagDSN string,
	getenv func(string) string,
	loadDBURL func() (string, error),
) (string, error) {
	if dsn := strings.TrimSpace(flagDSN); dsn != "" {
		return dsn, nil
	}
	for _, key := range []string{"TSIO_DATABASE_URL", "DATABASE_URL"} {
		if dsn := strings.TrimSpace(getenv(key)); dsn != "" {
			return dsn, nil
		}
	}
	dsn, err := loadDBURL()
	if err != nil {
		return "", fmt.Errorf("resolve database url (pass --database-url or set TSIO_DATABASE_URL / DATABASE_URL): %w", err)
	}
	if strings.TrimSpace(dsn) == "" {
		return "", errors.New("resolved database URL is empty")
	}
	return dsn, nil
}

func renderStatusShadowReport(w io.Writer, report reports.StatusShadowReport, format string, showAll bool) error {
	if format == "json" {
		if !showAll {
			filteredOrch := make([]reports.OrchRunShadowDelta, 0, report.OrchestrationRuns.Changed)
			for _, r := range report.OrchestrationRuns.Rows {
				if r.Changed {
					filteredOrch = append(filteredOrch, r)
				}
			}
			report.OrchestrationRuns.Rows = filteredOrch

			filteredGroups := make([]reports.ReportGroupShadowDelta, 0, report.ReportGroups.Changed)
			for _, g := range report.ReportGroups.Rows {
				if g.Changed {
					filteredGroups = append(filteredGroups, g)
				}
			}
			report.ReportGroups.Rows = filteredGroups
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	orch := report.OrchestrationRuns
	if _, err := fmt.Fprintf(
		w,
		"ORCHESTRATION RUNS: checked=%d changed=%d delta(passed=%+d failed=%+d flaky=%+d skipped=%+d total=%+d)\n",
		orch.Checked, orch.Changed,
		orch.DeltaPassed, orch.DeltaFailed, orch.DeltaFlaky, orch.DeltaSkipped, orch.DeltaTotal,
	); err != nil {
		return err
	}
	if err := renderOrchTable(w, orch.Rows, showAll); err != nil {
		return err
	}

	grp := report.ReportGroups
	if _, err := fmt.Fprintf(w, "\nREPORT GROUPS (JSON PASS RATE): checked=%d changed=%d\n", grp.Checked, grp.Changed); err != nil {
		return err
	}
	return renderGroupTable(w, grp.Rows, showAll)
}

func renderOrchTable(w io.Writer, rows []reports.OrchRunShadowDelta, showAll bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	printedHeader := false
	for _, r := range rows {
		if !showAll && !r.Changed {
			continue
		}
		if !printedHeader {
			if _, err := fmt.Fprintln(tw, "ID\tREPO\tNAME\tRUN_ID#ATTEMPT\tLEGACY (P/F/FL/S/T RATE)\tSHARED (P/F/FL/S/T RATE)\tDELTA"); err != nil {
				return err
			}
			printedHeader = true
		}
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s#%s\t%s\t%s\t%+d/%+d/%+d/%+d/%+d %s\n",
			r.RunID,
			r.Repository,
			r.Name,
			r.GHRunID,
			r.GHRunAttempt,
			fmtOrchCounts(r.Legacy),
			fmtOrchCounts(r.Shared),
			r.Delta.Passed, r.Delta.Failed, r.Delta.Flaky, r.Delta.Skipped, r.Delta.Total,
			fmtRateDelta(r.Delta.PassRate),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderGroupTable(w io.Writer, rows []reports.ReportGroupShadowDelta, showAll bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	printedHeader := false
	for _, g := range rows {
		if !showAll && !g.Changed {
			continue
		}
		if !printedHeader {
			if _, err := fmt.Fprintln(tw, "ID\tREPO\tNAME\tRUN_ID#ATTEMPT\tCOUNTS (P/F/FL/S/T)\tLEGACY_RATE\tSHARED_RATE\tDELTA"); err != nil {
				return err
			}
			printedHeader = true
		}
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s#%s\t%d/%d/%d/%d/%d\t%s\t%s\t%s\n",
			g.GroupID,
			g.Repository,
			g.Name,
			g.GHRunID,
			g.GHRunAttempt,
			g.Stats.Passed, g.Stats.Failed, g.Stats.Flaky, g.Stats.Skipped, g.Stats.Total,
			fmtRate(g.LegacyPassRate),
			fmtRate(g.SharedPassRate),
			fmtRateDelta(g.DeltaPassRate),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func fmtOrchCounts(c reports.StatusCounts) string {
	return fmt.Sprintf("%d/%d/%d/%d/%d %s", c.Passed, c.Failed, c.Flaky, c.Skipped, c.Total, fmtRate(c.PassRate))
}

func fmtRate(p *int) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d%%", *p)
}

func fmtRateDelta(p *int) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+dpp", *p)
}
