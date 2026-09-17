package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/config"
	tsiodb "github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/identity"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
)

// importRemoteCmd copies completed report groups from another TSIO deployment's
// public read API (groups, shards, suites, cases) into this database, keeping
// the original ids and timestamps, and runs them through ingest.Consolidate so
// identities and observations are derived exactly as for a live upload.
//
// It exists to seed staging and local backtests with production history without
// database or S3 access. Groups that already exist locally are skipped, so the
// command is safe to re-run.
func importRemoteCmd() *cobra.Command {
	var baseURL, since, until, frameworks string
	var repositories []string
	var maxGroups, workers, greenSample int
	var prFailedOnly, greenMetadataOnly bool
	cmd := &cobra.Command{Use: "import-remote", Short: "Import completed report groups from a remote TSIO's public API", RunE: func(cmd *cobra.Command, _ []string) error {
		start, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return fmt.Errorf("--since must be RFC3339: %w", err)
		}
		end := time.Now().UTC()
		if until != "" {
			if end, err = time.Parse(time.RFC3339, until); err != nil {
				return fmt.Errorf("--until must be RFC3339: %w", err)
			}
		}
		if len(repositories) == 0 {
			return errors.New("--repository is required")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		pool, err := tsiodb.NewPool(cmd.Context(), cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		imp := &remoteImporter{base: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 3 * time.Minute}, pool: pool, out: cmd.OutOrStdout(), repos: map[string]bool{}, frameworks: map[string]bool{}, prFailedOnly: prFailedOnly, greenSample: greenSample, greenMetadataOnly: greenMetadataOnly}
		for _, r := range repositories {
			imp.repos[r] = true
		}
		for _, f := range strings.Split(frameworks, ",") {
			if f = strings.TrimSpace(f); f != "" {
				imp.frameworks[f] = true
			}
		}
		return imp.run(cmd.Context(), start, end, maxGroups, workers)
	}}
	cmd.Flags().StringVar(&baseURL, "base-url", "https://test-io.test.mattermost.com/api/v1", "Remote TSIO API base URL")
	cmd.Flags().StringArrayVar(&repositories, "repository", nil, "Repository slug to import (repeatable)")
	cmd.Flags().StringVar(&since, "since", "", "Only groups created at or after this RFC3339 timestamp (required)")
	cmd.Flags().StringVar(&until, "until", "", "Only groups created before this RFC3339 timestamp")
	cmd.Flags().StringVar(&frameworks, "frameworks", "playwright,detox,maestro", "Comma-separated frameworks to import")
	cmd.Flags().IntVar(&maxGroups, "max-groups", 0, "Stop after this many imported groups (0 = no limit)")
	cmd.Flags().IntVar(&workers, "workers", 4, "Concurrent group imports")
	cmd.Flags().BoolVar(&prFailedOnly, "pr-failed-only", false, "Import PR groups only when they had failed tests (trunk groups are always imported)")
	cmd.Flags().IntVar(&greenSample, "pr-green-sample", 0, "With --pr-failed-only, also import this many green PR groups (newest first)")
	cmd.Flags().BoolVar(&greenMetadataOnly, "pr-green-metadata-only", false, "Import green PR groups as metadata only (group + shard rows, no test cases) so sibling-run history is complete")
	_ = cmd.MarkFlagRequired("since")
	return cmd
}

const statusImported = "imported"

type remoteImporter struct {
	base              string
	client            *http.Client
	pool              *pgxpool.Pool
	out               interface{ Write([]byte) (int, error) }
	repos             map[string]bool
	frameworks        map[string]bool
	prFailedOnly      bool
	greenSample       int
	greenMetadataOnly bool
}

type remoteGroup struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	RunGroup             string          `json:"run_group"`
	Status               string          `json:"status"`
	Framework            string          `json:"framework"`
	Repository           string          `json:"repository"`
	Branch               string          `json:"branch"`
	Commit               string          `json:"commit"`
	GHRunID              string          `json:"gh_run_id"`
	GHPRNumber           *int            `json:"gh_pr_number"`
	GHRunAttempt         string          `json:"gh_run_attempt"`
	CreatedAt            string          `json:"created_at"`
	LastUploadAt         string          `json:"last_upload_at"`
	TotalReportsExpected int             `json:"total_reports_expected"`
	RawTestStats         json.RawMessage `json:"test_stats"`
	Reports              []struct {
		ID          string `json:"id"`
		GHJobID     string `json:"gh_job_id"`
		GHJobName   string `json:"gh_job_name"`
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
		CreatedAt   string `json:"created_at"`
	} `json:"reports"`
}

// failed reads the summary's failed count (0 when the summary is absent).
func (g remoteGroup) failed() int {
	var stats struct {
		Failed int `json:"failed"`
	}
	if len(g.RawTestStats) == 0 || json.Unmarshal(g.RawTestStats, &stats) != nil {
		return 0
	}
	return stats.Failed
}

type remoteSuite struct {
	ID        string  `json:"id"`
	ReportID  string  `json:"report_id"`
	Title     string  `json:"title"`
	FilePath  *string `json:"file_path"`
	StartTime *string `json:"start_time"`
	Ordinal   int     `json:"ordinal"`
}

type remoteCase struct {
	SuiteID      string  `json:"suite_id"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	RetryCount   int     `json:"retry_count"`
	DurationMS   int64   `json:"duration_ms"`
	ErrorMessage *string `json:"error_message"`
	ErrorStack   *string `json:"error_stack"`
	Ordinal      int     `json:"ordinal"`
}

func (imp *remoteImporter) get(ctx context.Context, path string, v any) error {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*attempt) * time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imp.base+path, nil)
		if err != nil {
			return err
		}
		res, err := imp.client.Do(req)
		if err != nil {
			last = err
			continue
		}
		if res.StatusCode != http.StatusOK {
			_ = res.Body.Close()
			last = fmt.Errorf("GET %s: %d", path, res.StatusCode)
			if res.StatusCode >= 400 && res.StatusCode < 500 {
				return last
			}
			continue
		}
		err = json.NewDecoder(res.Body).Decode(v)
		_ = res.Body.Close()
		if err == nil {
			return nil
		}
		last = err
	}
	return last
}

func (imp *remoteImporter) run(ctx context.Context, since, until time.Time, maxGroups, workers int) error {
	type page struct {
		Reports []remoteGroup `json:"reports"`
		Total   int           `json:"total"`
	}
	var candidates []remoteGroup
	greenKept := 0
	for offset := 0; ; offset += 200 {
		var p page
		if err := imp.get(ctx, fmt.Sprintf("/reports?limit=200&offset=%d", offset), &p); err != nil {
			return err
		}
		stop := len(p.Reports) == 0
		for _, g := range p.Reports {
			at, err := time.Parse(time.RFC3339, g.CreatedAt)
			if err != nil {
				continue
			}
			if at.Before(since) {
				stop = true // the list is newest-first
				break
			}
			if !at.Before(until) || g.Status != "completed" || !imp.repos[g.Repository] || !imp.frameworks[g.Framework] {
				continue
			}
			if imp.prFailedOnly && g.GHPRNumber != nil && g.failed() == 0 && !imp.greenMetadataOnly {
				if greenKept >= imp.greenSample {
					continue
				}
				greenKept++
			}
			candidates = append(candidates, g)
		}
		if stop || offset+200 >= p.Total {
			break
		}
	}
	// Oldest first so created_at ordering matches insertion order for any
	// consumer that relies on uuidv7/created_at monotonicity.
	for i, j := 0, len(candidates)-1; i < j; i, j = i+1, j-1 {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}
	if maxGroups > 0 && len(candidates) > maxGroups {
		candidates = candidates[len(candidates)-maxGroups:]
	}
	_, _ = fmt.Fprintf(imp.out, "candidate groups: %d\n", len(candidates))
	var (
		wg                        sync.WaitGroup
		mu                        sync.Mutex
		imported, skipped, failed int
	)
	queue := make(chan remoteGroup)
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range queue {
				status, err := imp.importGroup(ctx, g)
				mu.Lock()
				switch {
				case err != nil:
					failed++
					_, _ = fmt.Fprintf(imp.out, "FAIL %s %s %s: %v\n", g.Repository, g.Name, g.ID, err)
				case status == "skipped":
					skipped++
				default:
					imported++
					if imported%50 == 0 {
						_, _ = fmt.Fprintf(imp.out, "imported %d (skipped %d, failed %d)\n", imported, skipped, failed)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, g := range candidates {
		if ctx.Err() != nil {
			break
		}
		queue <- g
	}
	close(queue)
	wg.Wait()
	_, _ = fmt.Fprintf(imp.out, "done: imported %d, skipped %d, failed %d\n", imported, skipped, failed)
	return ctx.Err()
}

func (imp *remoteImporter) importGroup(ctx context.Context, summary remoteGroup) (string, error) {
	groupID, err := uuid.Parse(summary.ID)
	if err != nil {
		return "", err
	}
	var exists bool
	if err := imp.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM report_groups WHERE id=$1)`, groupID).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "skipped", nil
	}
	var g remoteGroup
	if err := imp.get(ctx, "/reports/"+summary.ID, &g); err != nil {
		return "", err
	}
	var suites []remoteSuite
	var cases []remoteCase
	var suitesErr, casesErr error
	var wg sync.WaitGroup
	if imp.greenMetadataOnly && g.GHPRNumber != nil && g.failed() == 0 {
		createdAt, err := time.Parse(time.RFC3339, g.CreatedAt)
		if err != nil {
			return "", fmt.Errorf("created_at: %w", err)
		}
		return statusImported, imp.insertGroup(ctx, groupID, g, createdAt)
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		var raw struct {
			Suites []remoteSuite `json:"suites"`
		}
		suitesErr = imp.get(ctx, "/reports/"+summary.ID+"/suites", &raw)
		suites = raw.Suites
	}()
	go func() {
		defer wg.Done()
		casesErr = imp.get(ctx, "/reports/"+summary.ID+"/cases", &cases)
	}()
	wg.Wait()
	if suitesErr != nil {
		return "", suitesErr
	}
	if casesErr != nil {
		return "", casesErr
	}
	createdAt, err := time.Parse(time.RFC3339, g.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("created_at: %w", err)
	}
	if err := imp.insertGroup(ctx, groupID, g, createdAt); err != nil {
		return "", err
	}
	if imp.greenMetadataOnly && g.GHPRNumber != nil && g.failed() == 0 {
		return statusImported, nil // green PR run: metadata only
	}
	return statusImported, imp.consolidateReports(ctx, g, suites, cases)
}

// insertGroup writes the report_groups row and its per-shard reports rows with
// the original ids and timestamps.
func (imp *remoteImporter) insertGroup(ctx context.Context, groupID uuid.UUID, g remoteGroup, createdAt time.Time) error {
	lastUpload := createdAt
	if t, err := time.Parse(time.RFC3339, g.LastUploadAt); err == nil {
		lastUpload = t
	}
	kind := identity.InferBranchKind(g.Branch, g.RunGroup+" "+g.Name, g.GHPRNumber)
	baseRef := "main"
	if g.Repository == "mattermost/mattermost" {
		baseRef = "master"
	}
	if kind == "trunk" || kind == "release" {
		baseRef = g.Branch
	}
	var env any
	if lane := identity.InferLane(g.Name, nil); lane != "" {
		env = map[string]string{"lane": lane}
	}
	runGroup := g.RunGroup
	if runGroup == "" {
		runGroup = g.Name
	}
	attempt := g.GHRunAttempt
	if attempt == "" {
		attempt = "1"
	}
	total := g.TotalReportsExpected
	if total <= 0 {
		total = max(1, len(g.Reports))
	}
	return pgx.BeginFunc(ctx, imp.pool, func(tx pgx.Tx) error {
		var stats any
		if len(g.RawTestStats) > 0 && string(g.RawTestStats) != "null" {
			stats = g.RawTestStats
		}
		if _, err := tx.Exec(ctx, `INSERT INTO report_groups(id,framework,name,run_group,status,repository,branch,commit_sha,gh_run_id,gh_run_attempt,gh_pr_number,environment_metadata,created_at,updated_at,total_reports_expected,last_upload_at,branch_kind,base_ref,test_stats_json,reports_count)
 VALUES($1,$2,$3,$4,'completed',$5,$6,$7,$8,$9,$10,$11,$12,$12,$13,$14,$15,$16,$17,$18)`,
			groupID, g.Framework, g.Name, runGroup, g.Repository, g.Branch, g.Commit, g.GHRunID, attempt, g.GHPRNumber, env, createdAt, total, lastUpload, kind, baseRef, stats, len(g.Reports)); err != nil {
			return err
		}
		for _, r := range g.Reports {
			rid, err := uuid.Parse(r.ID)
			if err != nil {
				return err
			}
			at, err := time.Parse(time.RFC3339, r.CreatedAt)
			if err != nil {
				at = createdAt
			}
			name := r.GHJobName
			if name == "" {
				name = r.DisplayName
			}
			var jobID, jobName *string
			if r.GHJobID != "" {
				jobID = &r.GHJobID
			}
			if r.GHJobName != "" {
				jobName = &r.GHJobName
			}
			if _, err := tx.Exec(ctx, `INSERT INTO reports(id,report_group_id,name,status,gh_job_id,gh_job_name,created_at,updated_at) VALUES($1,$2,$3,'complete',$4,$5,$6,$6)`, rid, groupID, name, jobID, jobName, at); err != nil {
				return err
			}
		}
		return nil
	})
}

// consolidateReports rebuilds each shard's extracted suites and runs them
// through the real ingest path (which also derives identities/observations).
func (imp *remoteImporter) consolidateReports(ctx context.Context, g remoteGroup, suites []remoteSuite, cases []remoteCase) error {
	bySuite := map[string][]remoteCase{}
	for _, c := range cases {
		bySuite[c.SuiteID] = append(bySuite[c.SuiteID], c)
	}
	byReport := map[string][]remoteSuite{}
	for _, s := range suites {
		byReport[s.ReportID] = append(byReport[s.ReportID], s)
	}
	// Chronological shard order so attempt_index follows time (retest shards last).
	sort.SliceStable(g.Reports, func(i, j int) bool { return g.Reports[i].CreatedAt < g.Reports[j].CreatedAt })
	for _, r := range g.Reports {
		rid, _ := uuid.Parse(r.ID)
		var extracted []ingest.ExtractedSuite
		var earliest *time.Time
		seq := 0
		for _, s := range byReport[r.ID] {
			var start *time.Time
			if s.StartTime != nil {
				if t, err := time.Parse(time.RFC3339, *s.StartTime); err == nil {
					start = &t
					if earliest == nil || t.Before(*earliest) {
						earliest = &t
					}
				}
			}
			es := ingest.ExtractedSuite{Title: s.Title, FilePath: s.FilePath, StartTime: start}
			for _, c := range bySuite[s.ID] {
				es.Cases = append(es.Cases, ingest.ExtractedCase{
					Title:        c.Title,
					FullTitle:    remoteFullTitle(g.Framework, s, c.Title),
					Status:       c.Status,
					DurationMs:   c.DurationMS,
					RetryCount:   c.RetryCount,
					ErrorMessage: c.ErrorMessage,
					ErrorStack:   c.ErrorStack,
					Sequence:     seq,
					StartTime:    start,
				})
				seq++
			}
			extracted = append(extracted, es)
		}
		if len(extracted) == 0 {
			continue
		}
		if _, err := ingest.Consolidate(ctx, imp.pool, rid, extracted, earliest, earliest); err != nil {
			return fmt.Errorf("consolidate report %s: %w", r.ID, err)
		}
	}
	return nil
}

// remoteFullTitle rebuilds the ancestor-prefixed title the framework extractors
// would have produced. The public API does not expose full_title, and the stored
// suite hierarchy is flat, so this is a stable approximation: consistent across
// every imported group, which is what identity matching needs.
func remoteFullTitle(framework string, s remoteSuite, title string) string {
	switch framework {
	case "detox", "maestro":
		// Jest fullName: ancestor titles and the leaf joined by single spaces.
		prefix := strings.ReplaceAll(s.Title, " > ", " ")
		if prefix == "" || prefix == "Root" {
			return title
		}
		return prefix + " " + title
	default:
		parts := []string{}
		if s.FilePath != nil && *s.FilePath != "" {
			parts = append(parts, *s.FilePath)
		}
		if s.Title != "" && (s.FilePath == nil || s.Title != *s.FilePath) {
			parts = append(parts, s.Title)
		}
		parts = append(parts, title)
		return strings.Join(parts, " > ")
	}
}
