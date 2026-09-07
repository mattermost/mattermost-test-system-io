//go:build e2e

// Playwright runs the same spec under every configured project — chrome,
// firefox — and reports projectName on each test. The ingest path read that
// field into its struct and never used it, so both browsers' runs of one test
// landed on one stable_key and merged into a single flakiness history.
//
// That is the same failure mode test_cases.file exists to prevent, one axis
// over: a regression that only reproduces under firefox reads as a flake,
// because the chrome run of the same test keeps passing and dilutes it.
//
// This mirrors TestStableKey_DisambiguatesIdenticalTitlesAcrossFiles, which
// pins the file axis for Cypress, Detox and Maestro.
package testhistory

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

const projectTitle = "MM-T7322 renders the channel list"

// projectPlaywrightReport is one project's run of one test. Both projects run
// the same spec file with the same title, which is exactly what Playwright
// emits for a multi-project config.
func projectPlaywrightReport(project, status string) string {
	result := fmt.Sprintf(
		`{"status": %q, "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z",
		  "errors": [{"message": "Error: channel list not visible", "stack": "at list.spec.ts:9:5"}]}`,
		status)
	if status == "passed" {
		result = fmt.Sprintf(
			`{"status": %q, "duration": 100, "retry": 0, "startTime": "2026-01-01T00:00:00.000Z"}`,
			status)
	}
	outcome := "unexpected"
	if status == "passed" {
		outcome = "expected"
	}
	return fmt.Sprintf(`{
  "config": {"projects": [{"name": %q}]},
  "suites": [{
    "title": "channels/list.spec.ts", "file": "channels/list.spec.ts",
    "specs": [{"title": %q, "tests": [{"projectName": %q, "status": %q, "results": [%s]}]}]
  }]
}`, project, projectTitle, project, outcome, result)
}

func stableKeyForProject(t *testing.T, env *testenv.Env, project string) string {
	t.Helper()
	var key string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT stable_key FROM test_cases WHERE project = $1 LIMIT 1`, project).Scan(&key); err != nil {
		t.Fatalf("stable_key for project %s: %v", project, err)
	}
	return key
}

func TestStableKey_DisambiguatesIdenticalTitlesAcrossProjects(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	// firefox fails every run; chrome passes every run. If the two collapsed
	// onto one stable_key, the merged series would show three fails and three
	// passes on a single key — a false flake — instead of two clean, opposite
	// series, and the firefox regression would be dismissed as flakiness.
	for i := 0; i < 3; i++ {
		ingestReport(t, env, tok, "playwright", "playwright-firefox",
			sha("r0", i), fmt.Sprintf("ff-%d", i), projectPlaywrightReport("firefox", "failed"))
		ingestReport(t, env, tok, "playwright", "playwright-chrome",
			sha("r1", i), fmt.Sprintf("ch-%d", i), projectPlaywrightReport("chrome", "passed"))
	}

	keyFirefox := stableKeyForProject(t, env, "firefox")
	keyChrome := stableKeyForProject(t, env, "chrome")

	if keyFirefox == keyChrome {
		t.Fatalf("stable_key collided across projects: both %q — chrome and firefox "+
			"produced the same key from an identical title", keyFirefox)
	}
	// The MM-T id alone must not be the key: mattermost/mattermost carries
	// MM-T ids AND runs multiple projects, so a project prefix that only
	// applied to the title fallback would leave this repository collapsing.
	if keyFirefox == "MM-T7322" || keyChrome == "MM-T7322" {
		t.Fatalf("stable_key was not disambiguated: firefox=%q chrome=%q, "+
			"want each prefixed by its project", keyFirefox, keyChrome)
	}

	q := func(key string) url.Values {
		v := url.Values{}
		v.Set("repo", "mattermost")
		v.Set("test_id", key)
		v.Set("branch", "master")
		return v
	}
	histFirefox := getJSON(t, env, "/api/v1/tests/history?"+q(keyFirefox).Encode())
	histChrome := getJSON(t, env, "/api/v1/tests/history?"+q(keyChrome).Encode())

	sf := histFirefox["summary"].(map[string]any)
	sc := histChrome["summary"].(map[string]any)

	if sf["runs"].(float64) != 3 || sf["failed"].(float64) != 3 || sf["passed"].(float64) != 0 {
		t.Fatalf("firefox history = %v, want 3 runs all failed — it must not have "+
			"absorbed chrome's passes and read as a flake", sf)
	}
	if sc["runs"].(float64) != 3 || sc["passed"].(float64) != 3 || sc["failed"].(float64) != 0 {
		t.Fatalf("chrome history = %v, want 3 runs all passed — it must not have "+
			"absorbed firefox's failures", sc)
	}
}

// TestStableKey_FrameworksWithoutProjectsKeepTheUnprefixedKey guards the other
// half: Cypress, Detox and Maestro have no project concept, write NULL, and
// must keep the key shape they already had, so their stored history stays
// joinable to itself.
func TestStableKey_FrameworksWithoutProjectsKeepTheUnprefixedKey(t *testing.T) {
	env := testenv.Start(t)
	tok := uploaderToken(t, env)

	const body = `{
  "stats": {"start": "2026-01-01T00:00:00.000Z"},
  "results": [{
    "title": "root", "file": "cypress/e2e/list.cy.js", "fullFile": "cypress/e2e/list.cy.js",
    "suites": [],
    "tests": [{"title": "MM-T7323 opens the list", "fullTitle": "MM-T7323 opens the list",
               "duration": 40, "state": "passed", "pass": true}]
  }]
}`
	ingestReport(t, env, tok, "cypress", "cypress-noproject", sha("r2", 0), "cy-0", body)

	var key string
	var project *string
	if err := env.Pool.QueryRow(context.Background(),
		`SELECT stable_key, project FROM test_cases WHERE external_test_id = 'MM-T7323'`).
		Scan(&key, &project); err != nil {
		t.Fatalf("query: %v", err)
	}
	if project != nil {
		t.Errorf("project = %q, want NULL for a framework with no project concept", *project)
	}
	if key != "MM-T7323" {
		t.Errorf("stable_key = %q, want the bare MM-T id — a NULL project must not "+
			"prefix anything, or existing history stops joining to itself", key)
	}
}
