package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
)

func TestValidateStatusShadowFlags(t *testing.T) {
	if err := validateStatusShadowFlags(statusShadowFlags{limit: 50, format: "table"}); err != nil {
		t.Fatalf("valid table flags: %v", err)
	}
	if err := validateStatusShadowFlags(statusShadowFlags{limit: 500, format: "json"}); err != nil {
		t.Fatalf("valid json flags: %v", err)
	}
	for _, bad := range []statusShadowFlags{
		{limit: 0, format: "table"},
		{limit: 501, format: "table"},
		{limit: 10, format: "csv"},
	} {
		if err := validateStatusShadowFlags(bad); err == nil {
			t.Fatalf("expected error for flags %+v", bad)
		}
	}
}

func TestResolveShadowDatabaseURL(t *testing.T) {
	noLoad := func() (string, error) {
		return "", errors.New("loadDBURL should not be called")
	}
	envMap := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	got, err := resolveShadowDatabaseURL("postgres://flag@db/tsio", envMap(map[string]string{
		"TSIO_DATABASE_URL": "postgres://tsio@db/tsio",
		"DATABASE_URL":      "postgres://dburl@db/tsio",
	}), noLoad)
	if err != nil || got != "postgres://flag@db/tsio" {
		t.Fatalf("flag precedence got (%q, %v)", got, err)
	}

	got, err = resolveShadowDatabaseURL("", envMap(map[string]string{
		"TSIO_DATABASE_URL": "postgres://tsio@db/tsio",
		"DATABASE_URL":      "postgres://dburl@db/tsio",
	}), noLoad)
	if err != nil || got != "postgres://tsio@db/tsio" {
		t.Fatalf("TSIO_DATABASE_URL precedence got (%q, %v)", got, err)
	}

	got, err = resolveShadowDatabaseURL("", envMap(map[string]string{
		"DATABASE_URL": "postgres://dburl@db/tsio",
	}), noLoad)
	if err != nil || got != "postgres://dburl@db/tsio" {
		t.Fatalf("DATABASE_URL precedence got (%q, %v)", got, err)
	}

	got, err = resolveShadowDatabaseURL("", envMap(nil), func() (string, error) {
		return "postgres://cfg@db/tsio", nil
	})
	if err != nil || got != "postgres://cfg@db/tsio" {
		t.Fatalf("config.LoadDatabaseURL fallback got (%q, %v)", got, err)
	}

	if _, err := resolveShadowDatabaseURL("", envMap(nil), func() (string, error) {
		return "", errors.New("missing DB host")
	}); err == nil {
		t.Fatal("expected error when no DSN and loadDBURL fails")
	}

	t.Setenv("TSIO_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("TSIO_S3_BUCKET", "")
	t.Setenv("TSIO_SESSION_SECRET", "")
	t.Setenv("TSIO_DB_HOST", "db.internal")
	t.Setenv("TSIO_DB_PORT", "5432")
	t.Setenv("TSIO_DB_USER", "tsio")
	t.Setenv("TSIO_DB_PASSWORD", "secret")
	t.Setenv("TSIO_DB_NAME", "tsio")
	t.Setenv("TSIO_DB_SSLMODE", "disable")
	got, err = resolveShadowDatabaseURL("", os.Getenv, config.LoadDatabaseURL)
	if err != nil || got != "postgres://tsio:secret@db.internal:5432/tsio?sslmode=disable" {
		t.Fatalf("split TSIO_DB_* without S3/session got (%q, %v)", got, err)
	}
}

func TestRenderStatusShadowReport(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	runID := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f1")
	groupID := uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f2")

	rep := reports.StatusShadowReport{
		Limit: 50,
		OrchestrationRuns: reports.OrchShadowSection{
			Checked:     2,
			Changed:     1,
			DeltaPassed: -1,
			DeltaFlaky:  1,
			Rows: []reports.OrchRunShadowDelta{
				{
					RunID:        runID,
					Repository:   "mattermost/mattermost",
					Name:         "pw-changed",
					GHRunID:      "10",
					GHRunAttempt: "1",
					Legacy:       reports.StatusCounts{Passed: 2, Total: 2, PassRate: intPtr(100)},
					Shared:       reports.StatusCounts{Passed: 1, Flaky: 1, Total: 2, PassRate: intPtr(100)},
					Delta:        reports.StatusCountDelta{Passed: -1, Flaky: 1, PassRate: intPtr(0)},
					Changed:      true,
				},
				{
					RunID:        uuid.MustParse("019f8aaf-ba43-78c0-99ba-716d7ea2f1f3"),
					Repository:   "mattermost/mattermost",
					Name:         "pw-same",
					GHRunID:      "11",
					GHRunAttempt: "1",
					Legacy:       reports.StatusCounts{Passed: 1, Total: 1, PassRate: intPtr(100)},
					Shared:       reports.StatusCounts{Passed: 1, Total: 1, PassRate: intPtr(100)},
					Delta:        reports.StatusCountDelta{PassRate: intPtr(0)},
					Changed:      false,
				},
			},
		},
		ReportGroups: reports.ReportGroupShadowSection{
			Checked: 1,
			Changed: 1,
			Rows: []reports.ReportGroupShadowDelta{
				{
					GroupID:        groupID,
					Repository:     "mattermost/mattermost",
					Name:           "cypress",
					GHRunID:        "12",
					GHRunAttempt:   "1",
					Stats:          reports.StatusCounts{Passed: 8, Failed: 1, Flaky: 1, Skipped: 5, Total: 15},
					LegacyPassRate: intPtr(60),
					SharedPassRate: intPtr(90),
					DeltaPassRate:  intPtr(30),
					Changed:        true,
				},
			},
		},
	}

	var tableBuf bytes.Buffer
	if err := renderStatusShadowReport(&tableBuf, rep, "table", false); err != nil {
		t.Fatalf("render table: %v", err)
	}
	out := tableBuf.String()
	for _, want := range []string{
		"ORCHESTRATION RUNS: checked=2 changed=1 delta(passed=-1 failed=+0 flaky=+1 skipped=+0 total=+0)",
		"pw-changed",
		"REPORT GROUPS (JSON PASS RATE): checked=1 changed=1",
		"60%",
		"90%",
		"+30pp",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pw-same") {
		t.Fatalf("unchanged row should be omitted when --all=false:\n%s", out)
	}

	var jsonBuf bytes.Buffer
	if err := renderStatusShadowReport(&jsonBuf, rep, "json", false); err != nil {
		t.Fatalf("render json: %v", err)
	}
	var decoded reports.StatusShadowReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(decoded.OrchestrationRuns.Rows) != 1 || decoded.OrchestrationRuns.Rows[0].Name != "pw-changed" {
		t.Fatalf("filtered json rows = %+v", decoded.OrchestrationRuns.Rows)
	}
}
