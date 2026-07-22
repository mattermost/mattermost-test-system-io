// Package testenv spins up a disposable Postgres (via testcontainers-go), runs
// the real migrations, builds the production HTTP handler tree via
// internal/server, and starts an httptest.Server — everything the E2E suites
// need to exercise the upload/auth paths against real Postgres semantics.
//
// S3/MinIO is deliberately swapped for an in-memory fake ObjectStore: the E2E
// tests that use this helper care about auth, request routing, and consolidation
// logic — not about S3 itself.
package testenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/apikey"
	authoidc "github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/session"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/server"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/testutil/oidcmock"
)

// Env is what a test fixture receives. Test code uses ServerURL to make
// requests, Pool for direct DB setup/assertions, and Mock to mint tokens.
type Env struct {
	Pool         *pgxpool.Pool
	ServerURL    string
	Mock         *oidcmock.Provider
	Store        *FakeStore
	Orchestrator *orchestration.Store
}

// Option configures Start. Most tests don't need any.
type Option func(*startConfig)

type startConfig struct {
	store storage.ObjectStore
}

// WithStore swaps in a caller-supplied ObjectStore instead of the default
// FakeStore — e.g. a store that wraps FakeStore with an artificial delay, to
// make a background pipeline's duration observable in a test. When set,
// Env.Store is left nil since it's typed *FakeStore; the test already holds
// its own reference to whatever it passed in.
func WithStore(s storage.ObjectStore) Option {
	return func(c *startConfig) { c.store = s }
}

// Start boots Postgres + migrations + the real HTTP handler. When the OIDC
// argument is nil, no OIDC verifier is wired — tests that don't need it avoid
// the ~1 s mock-provider bootstrap.
func Start(t *testing.T, opts ...Option) *Env {
	t.Helper()

	cfg := &startConfig{}
	for _, o := range opts {
		o(cfg)
	}

	ctx := context.Background()
	pgC, err := tcpostgres.Run(ctx,
		"postgres:18.3",
		tcpostgres.WithDatabase("tsio"),
		tcpostgres.WithUsername("tsio"),
		tcpostgres.WithPassword("tsio"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
		tcpostgres.WithInitScripts(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	})

	// Snapshot support is optional — ignore any error.
	_ = pgC.Snapshot(ctx, tcpostgres.WithSnapshotName("pristine"))

	databaseURL, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}

	if err := db.Migrate(databaseURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Mock OIDC provider (always bootstrap; cheap once per test).
	mockProv := oidcmock.NewProvider(t)
	oidcV, err := authoidc.New(ctx, mockProv.Issuer, "tsio")
	if err != nil {
		t.Fatalf("oidc verifier: %v", err)
	}

	var fakeStore *FakeStore
	var store storage.ObjectStore = cfg.store
	if store == nil {
		fakeStore = NewFakeStore()
		store = fakeStore
	}

	hub := events.NewHub()
	var logOut = io.Discard
	if os.Getenv("TSIO_TEST_LOG") == "1" {
		logOut = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logOut, nil))

	orchStore := &orchestration.Store{Pool: pool, Logger: logger}
	orchPublisher := &orchestration.Publisher{Hub: hub, Logger: logger}

	// Start a fast-ticking reaper so run-timeout / lease-timeout E2E tests do
	// not have to wait for the production 5 s default. Stops in t.Cleanup.
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	reaper := &orchestration.Reaper{
		Store:     orchStore,
		Publisher: orchPublisher,
		Logger:    logger,
		Interval:  500 * time.Millisecond,
	}
	if err := reaper.Start(reaperCtx); err != nil {
		t.Fatalf("start reaper: %v", err)
	}
	t.Cleanup(func() {
		reaperCancel()
		reaper.Stop()
	})

	handler := server.Build(server.Deps{
		Logger:                 logger,
		Pool:                   pool,
		Store:                  store,
		APIKeys:                &apikey.Repo{Pool: pool},
		Sessions:               &session.Manager{Pool: pool, TTL: time.Hour},
		Refresh:                &session.RefreshManager{Pool: pool, TTL: time.Hour},
		Policy:                 &policy.Engine{Pool: pool},
		OIDC:                   oidcV,
		OAuth:                  nil, // human sign-in not exercised in these tests
		Hub:                    hub,
		Publisher:              &events.Publisher{Hub: hub},
		OrchestrationStore:     orchStore,
		OrchestrationPublisher: orchPublisher,
		Version:                "test",
		// Client-facing /config + /info defaults — match the production env
		// defaults (see internal/config).
		UploadTimeoutMs: 3_600_000, // 1h
		SearchMinLength: 3,
		Environment:     "test",
		RepoURL:         "https://github.com/mattermost/mattermost-test-system-io",
		// OpenAPISpecPath intentionally unset — tests craft synthetic requests,
		// some of which legitimately don't match the spec (missing auth etc.).
		MaxUploadBytes:   1 << 24, // 16 MiB
		MaxArtifactBytes: 1 << 22, // 4 MiB
		PresignTTL:       5 * time.Minute,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &Env{
		Pool:         pool,
		ServerURL:    srv.URL,
		Mock:         mockProv,
		Store:        fakeStore,
		Orchestrator: orchStore,
	}
}

// IssueAPIKey inserts a live API key and returns the plaintext.
func (e *Env) IssueAPIKey(t *testing.T, name string) (plaintext string) {
	t.Helper()
	iss, err := apikey.Issue()
	if err != nil {
		t.Fatalf("issue apikey: %v", err)
	}
	repo := &apikey.Repo{Pool: e.Pool}
	if _, err := repo.Insert(context.Background(), name, iss); err != nil {
		t.Fatalf("insert apikey: %v", err)
	}
	return iss.PlainText
}

// InsertPolicy installs a github_oidc_policies row — convenience for tests.
func (e *Env) InsertPolicy(t *testing.T, name string, priority int, grantRole string, matchers map[string]string) {
	t.Helper()
	_, err := e.Pool.Exec(context.Background(), `
		INSERT INTO github_oidc_policies (name, enabled, priority,
		    match_repository, match_repository_owner, match_workflow, match_ref, match_environment,
		    grant_role)
		VALUES ($1,true,$2,$3,$4,$5,$6,$7,$8)
	`, name, priority,
		nullIf(matchers["repository"]),
		nullIf(matchers["repository_owner"]),
		nullIf(matchers["workflow"]),
		nullIf(matchers["ref"]),
		nullIf(matchers["environment"]),
		grantRole,
	)
	if err != nil {
		t.Fatalf("insert policy: %v", err)
	}
}

// DefaultReportGroup inserts a synthetic report_group keyed by
// ("mattermost/test", "<fake-sha>", "e2e-run", "default", "1") and returns its
// UUID. The composite-identity schema replaced the slug-based tenant model in
// 007-web-api-parity.
func (e *Env) DefaultReportGroup(t *testing.T) string {
	t.Helper()
	var id string
	err := e.Pool.QueryRow(context.Background(), `
		INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt)
		VALUES ('playwright','default','mattermost/test','main','0000000000000000000000000000000000000000','e2e-run','1')
		ON CONFLICT (repository, commit_sha, gh_run_id, name, gh_run_attempt) DO UPDATE SET updated_at = now()
		RETURNING id
	`).Scan(&id)
	if err != nil {
		t.Fatalf("insert default group: %v", err)
	}
	return id
}

// RegisterResponse captures the outcome of POST /api/v1/reports/register.
// Exposed so tests can distinguish the "auth accepted + register succeeded"
// path from policy/validation errors.
type RegisterResponse struct {
	ReportID   string
	StatusCode int
	Body       []byte
}

// RegisterStatelessUpload POSTs a minimal valid body to
// /api/v1/reports/register using the supplied Authorization header value
// (e.g. "Bearer <jwt>" or "ApiKey <plaintext>"). Use this from tests that
// previously hit the retired POST /api/v1/reports bundle endpoint to prove
// the protected path works and to exercise the per-shard `reports` row
// insert (incl. `uploaded_by_oidc_subject`).
func (e *Env) RegisterStatelessUpload(t *testing.T, authHeader string, body map[string]any) RegisterResponse {
	t.Helper()

	// Fill in defaults matching testenv.DefaultReportGroup's composite key so
	// every test registers under a stable group unless it overrides.
	defaults := map[string]any{
		"repository":     "mattermost/test",
		"commit":         "0000000000000000000000000000000000000000",
		"gh_run_id":      "e2e-run",
		"gh_run_attempt": "1",
		"framework":      "playwright",
		"name":           "default",
		"branch":         "main",
		"gh_job_id":      "job-" + randHex(t, 8),
		"gh_job_name":    "default",
		"json_files":     []any{},
		"screenshots":    []any{},
	}
	for k, v := range body {
		defaults[k] = v
	}
	payload, err := json.Marshal(defaults)
	if err != nil {
		t.Fatalf("marshal register body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.ServerURL+"/api/v1/reports/register", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	out := RegisterResponse{StatusCode: resp.StatusCode, Body: raw}
	if resp.StatusCode < 300 {
		var decoded struct {
			ReportID string `json:"report_id"`
		}
		_ = json.Unmarshal(raw, &decoded)
		out.ReportID = decoded.ReportID
	}
	return out
}

// ---- helpers ----

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- FakeStore ----

// FakeStore is an in-memory ObjectStore used by the E2E suite so tests don't
// need MinIO/S3 containers. Safe for concurrent use.
type FakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	meta    map[string]storage.ObjectMeta
}

// NewFakeStore returns an empty FakeStore.
func NewFakeStore() *FakeStore {
	return &FakeStore{objects: map[string][]byte{}, meta: map[string]storage.ObjectMeta{}}
}

// Put stores an object.
func (f *FakeStore) Put(_ context.Context, key string, body io.Reader, contentType string, size int64) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = b
	f.meta[key] = storage.ObjectMeta{ContentType: contentType, SizeBytes: size, LastModified: time.Now()}
	return nil
}

// Get retrieves.
func (f *FakeStore) Get(_ context.Context, key string) (io.ReadCloser, storage.ObjectMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	if !ok {
		return nil, storage.ObjectMeta{}, storage.ErrNotFound
	}
	return io.NopCloser(readerOf(b)), f.meta[key], nil
}

// Delete drops an object.
func (f *FakeStore) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	delete(f.meta, key)
	return nil
}

// List returns every key with the given prefix, in lexicographic order so the
// output is deterministic across test runs.
func (f *FakeStore) List(_ context.Context, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0)
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// PresignGet returns a phony URL that tests never dereference.
func (f *FakeStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "fake://" + key, nil
}

// Has reports whether the given key exists.
func (f *FakeStore) Has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

func readerOf(b []byte) *byteSliceReader { return &byteSliceReader{b: b} }

type byteSliceReader struct{ b []byte }

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
