package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

var zephyrKeyRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(MM-T\d+)(?:$|[^a-z0-9])`)

// ExtractZephyrKey extracts the canonical uppercase Zephyr test key (e.g.
// "MM-T5294" from "MM-T5294_3" or "@mm-t5294"), checking title, fullTitle,
// and tags in precedence order. Returns "" when no Zephyr key is present.
func ExtractZephyrKey(fullTitle, title string, tags []string) string {
	for _, s := range []string{title, fullTitle} {
		if m := zephyrKeyRE.FindStringSubmatch(sanitizeUTF8(s)); len(m) == 2 {
			return strings.ToUpper(m[1])
		}
	}
	for _, tag := range tags {
		if m := zephyrKeyRE.FindStringSubmatch(sanitizeUTF8(tag)); len(m) == 2 {
			return strings.ToUpper(m[1])
		}
	}
	return ""
}

func sanitizeUTF8(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}

// SanitizeUTF8Ptr strips invalid UTF-8 and NUL bytes PostgreSQL text rejects.
func SanitizeUTF8Ptr(p *string) *string {
	if p == nil {
		return nil
	}
	s := sanitizeUTF8(*p)
	return &s
}

// NormalizeTestFile normalizes a test file path to a stable repo-relative form,
// reusing relativeDetoxPath slash, "./", and CI workspace stripping.
func NormalizeTestFile(file string) string {
	f := strings.TrimSpace(sanitizeUTF8(file))
	if f == "" {
		return ""
	}
	f = relativeDetoxPath(f)
	if rest, ok := strings.CutPrefix(f, "/github/workspace"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		f = strings.TrimLeft(rest, "/")
	}
	if f == "" {
		return ""
	}
	f = path.Clean(f)
	if f == "." {
		return ""
	}
	return f
}

// NormalizeTestTitle trims leading/trailing whitespace and collapses internal
// whitespace runs to single spaces.
func NormalizeTestTitle(title string) string {
	return strings.Join(strings.Fields(sanitizeUTF8(title)), " ")
}

// DeriveTestKey computes the stable per-repository test identity key and
// optional Zephyr key. Project and platform are intentionally excluded so the
// same logical test maps to one tests row across browsers and OS targets.
func DeriveTestKey(repository, file, fullTitle, title string, tags []string) (testKey, zephyrKey string) {
	if zk := ExtractZephyrKey(fullTitle, title, tags); zk != "" {
		return "zephyr:" + zk, zk
	}
	normRepo := strings.TrimSpace(sanitizeUTF8(repository))
	normFile := NormalizeTestFile(file)
	normTitle := NormalizeTestTitle(fullTitle)
	if normTitle == "" {
		normTitle = NormalizeTestTitle(title)
	}
	sum := sha256.Sum256([]byte(normRepo + "\x00" + normFile + "\x00" + normTitle))
	return "h:" + hex.EncodeToString(sum[:]), ""
}

// IdentityMeta provides the report-level context needed to derive and persist
// test identities and failure signatures.
type IdentityMeta struct {
	ReportGroupID uuid.UUID
	Repository    string
	Framework     string
	SeenAt        time.Time
}

// CaseIdentityInput holds the normalized inputs for resolving one test case's
// test_id and optional signature_id.
type CaseIdentityInput struct {
	File         string
	Title        string
	FullTitle    string
	Tags         []string
	Status       string
	ErrorMessage string
	ErrorStack   string
	StartTime    *time.Time
}

// TestIdentity holds the persisted database IDs for one test case.
type TestIdentity struct {
	TestID      uuid.UUID
	SignatureID *uuid.UUID
}

type testUpsertRow struct {
	testKey   string
	zephyrKey string
	file      string
	fullTitle string
	firstSeen time.Time
	lastSeen  time.Time
}

type sigUpsertRow struct {
	sig       errorsig.Signature
	firstSeen time.Time
}

// ResolveCaseIdentities derives test_key and algo_version=1 failure signatures
// for each case, bulk-upserts unique tests and failure_signatures inside tx in
// deterministic sorted order, and returns one TestIdentity per input case.
//
// Callers (Consolidate and future backfill jobs such as SEC-11506) MUST run tx
// at READ COMMITTED isolation and keep tx short (commit immediately after
// ResolveCaseIdentities rather than holding it open across suite/case writes):
// upsertTestsTx acquires row locks on existing shared `tests` rows via ON
// CONFLICT DO UPDATE, and upsertFailureSignaturesTx relies on ON CONFLICT
// (fingerprint) DO NOTHING followed by SELECT, which needs per-statement
// snapshot refresh to see rows committed concurrently by other shards.
func ResolveCaseIdentities(
	ctx context.Context, tx pgx.Tx, meta IdentityMeta, cases []CaseIdentityInput,
) ([]TestIdentity, error) {
	if len(cases) == 0 {
		return nil, nil
	}
	meta.Repository = sanitizeUTF8(meta.Repository)
	meta.Framework = sanitizeUTF8(meta.Framework)
	defaultSeen := meta.SeenAt.UTC()
	if defaultSeen.IsZero() {
		defaultSeen = time.Now().UTC()
	}

	uniqueTests := make(map[string]testUpsertRow, len(cases))
	uniqueSigs := make(map[string]sigUpsertRow)
	caseTestKeys := make([]string, len(cases))
	caseFPs := make([]string, len(cases))

	for i, c := range cases {
		seenAt := defaultSeen
		if c.StartTime != nil && !c.StartTime.IsZero() {
			seenAt = c.StartTime.UTC()
		}
		normFile := NormalizeTestFile(c.File)
		normTitle := NormalizeTestTitle(c.FullTitle)
		if normTitle == "" {
			normTitle = NormalizeTestTitle(c.Title)
		}
		testKey, zephyrKey := DeriveTestKey(meta.Repository, c.File, c.FullTitle, c.Title, c.Tags)
		caseTestKeys[i] = testKey

		if cur, ok := uniqueTests[testKey]; ok {
			if seenAt.Before(cur.firstSeen) {
				cur.firstSeen = seenAt
			}
			if seenAt.After(cur.lastSeen) {
				cur.lastSeen = seenAt
			}
			if cur.zephyrKey == "" {
				cur.zephyrKey = zephyrKey
			}
			if cur.file == "" {
				cur.file = normFile
			}
			if cur.fullTitle == "" {
				cur.fullTitle = normTitle
			}
			uniqueTests[testKey] = cur
		} else {
			uniqueTests[testKey] = testUpsertRow{
				testKey:   testKey,
				zephyrKey: zephyrKey,
				file:      normFile,
				fullTitle: normTitle,
				firstSeen: seenAt,
				lastSeen:  seenAt,
			}
		}

		// Per tsio-kpi-technical-spec.md, every failing attempt gets a signature;
		// passed, skipped, and retried-and-passed (flaky) rows leave signature_id NULL.
		status := sanitizeUTF8(c.Status)
		if teststatus.IsFail(status) {
			sig := errorsig.Compute(errorsig.Input{
				Framework:    meta.Framework,
				Status:       status,
				ErrorMessage: c.ErrorMessage,
				ErrorStack:   c.ErrorStack,
			})
			caseFPs[i] = sig.Fingerprint
			if cur, ok := uniqueSigs[sig.Fingerprint]; ok {
				if seenAt.Before(cur.firstSeen) {
					cur.firstSeen = seenAt
				}
				if cur.sig.TopFrame == "" {
					cur.sig.TopFrame = sig.TopFrame
				}
				if cur.sig.SampleMessage == "" {
					cur.sig.SampleMessage = sig.SampleMessage
				}
				uniqueSigs[sig.Fingerprint] = cur
			} else {
				uniqueSigs[sig.Fingerprint] = sigUpsertRow{sig: sig, firstSeen: seenAt}
			}
		}
	}

	testIDByKey, err := upsertTestsTx(ctx, tx, meta.Repository, meta.Framework, uniqueTests)
	if err != nil {
		return nil, err
	}
	sigIDByFP, err := upsertFailureSignaturesTx(ctx, tx, meta.ReportGroupID, uniqueSigs)
	if err != nil {
		return nil, err
	}

	out := make([]TestIdentity, len(cases))
	for i := range cases {
		out[i].TestID = testIDByKey[caseTestKeys[i]]
		if fp := caseFPs[i]; fp != "" {
			id := sigIDByFP[fp]
			out[i].SignatureID = &id
		}
	}
	return out, nil
}

func upsertTestsTx(
	ctx context.Context, tx pgx.Tx, repository, framework string, unique map[string]testUpsertRow,
) (map[string]uuid.UUID, error) {
	repository = sanitizeUTF8(repository)
	framework = sanitizeUTF8(framework)

	keys := make([]string, 0, len(unique))
	for k := range unique {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	zephyrs := make([]string, len(keys))
	files := make([]string, len(keys))
	titles := make([]string, len(keys))
	firstSeens := make([]time.Time, len(keys))
	lastSeens := make([]time.Time, len(keys))
	for i, k := range keys {
		r := unique[k]
		zephyrs[i] = sanitizeUTF8(r.zephyrKey)
		files[i] = sanitizeUTF8(r.file)
		titles[i] = sanitizeUTF8(r.fullTitle)
		firstSeens[i] = r.firstSeen
		lastSeens[i] = r.lastSeen
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO tests (repository, framework, test_key, zephyr_key, file, full_title, first_seen_at, last_seen_at)
		SELECT $1, $2, u.test_key, NULLIF(u.zephyr_key, ''), u.file, u.full_title, u.first_seen_at, u.last_seen_at
		FROM UNNEST($3::text[], $4::text[], $5::text[], $6::text[], $7::timestamptz[], $8::timestamptz[])
		     AS u(test_key, zephyr_key, file, full_title, first_seen_at, last_seen_at)
		ON CONFLICT (repository, test_key) DO UPDATE SET
			framework     = EXCLUDED.framework,
			zephyr_key    = COALESCE(EXCLUDED.zephyr_key, tests.zephyr_key),
			file          = CASE WHEN EXCLUDED.file <> '' THEN EXCLUDED.file ELSE tests.file END,
			full_title    = CASE WHEN EXCLUDED.full_title <> '' THEN EXCLUDED.full_title ELSE tests.full_title END,
			first_seen_at = LEAST(tests.first_seen_at, EXCLUDED.first_seen_at),
			last_seen_at  = GREATEST(tests.last_seen_at, EXCLUDED.last_seen_at)
		RETURNING test_key, id
	`, repository, framework, keys, zephyrs, files, titles, firstSeens, lastSeens)
	if err != nil {
		return nil, fmt.Errorf("upsert tests: %w", err)
	}
	defer rows.Close()

	out := make(map[string]uuid.UUID, len(keys))
	for rows.Next() {
		var k string
		var id uuid.UUID
		if err := rows.Scan(&k, &id); err != nil {
			return nil, fmt.Errorf("scan upserted test: %w", err)
		}
		out[k] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upserted tests: %w", err)
	}
	return out, nil
}

func upsertFailureSignaturesTx(
	ctx context.Context, tx pgx.Tx, reportGroupID uuid.UUID, unique map[string]sigUpsertRow,
) (map[string]uuid.UUID, error) {
	if len(unique) == 0 {
		return nil, nil
	}
	fps := make([]string, 0, len(unique))
	for fp := range unique {
		fps = append(fps, fp)
	}
	slices.Sort(fps)

	algos := make([]int16, len(fps))
	errClasses := make([]string, len(fps))
	normMsgs := make([]string, len(fps))
	topFrames := make([]string, len(fps))
	sampleMsgs := make([]string, len(fps))
	firstSeens := make([]time.Time, len(fps))
	for i, fp := range fps {
		r := unique[fp]
		algos[i] = r.sig.AlgoVersion
		errClasses[i] = r.sig.ErrorClass
		normMsgs[i] = r.sig.NormalizedMessage
		topFrames[i] = r.sig.TopFrame
		sampleMsgs[i] = r.sig.SampleMessage
		firstSeens[i] = r.firstSeen
	}

	var groupID *uuid.UUID
	if reportGroupID != uuid.Nil {
		groupID = &reportGroupID
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO failure_signatures (
			fingerprint, algo_version, error_class, normalized_message,
			top_frame, sample_message, first_seen_at, first_seen_report_group_id
		)
		SELECT u.fingerprint, u.algo_version, NULLIF(u.error_class, ''), u.normalized_message,
		       NULLIF(u.top_frame, ''), NULLIF(u.sample_message, ''), u.first_seen_at, $8
		FROM UNNEST($1::text[], $2::smallint[], $3::text[], $4::text[], $5::text[], $6::text[], $7::timestamptz[])
		     AS u(fingerprint, algo_version, error_class, normalized_message, top_frame, sample_message, first_seen_at)
		ON CONFLICT (fingerprint) DO NOTHING
	`, fps, algos, errClasses, normMsgs, topFrames, sampleMsgs, firstSeens, groupID); err != nil {
		return nil, fmt.Errorf("insert failure_signatures: %w", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT fingerprint, id FROM failure_signatures WHERE fingerprint = ANY($1::text[])`, fps)
	if err != nil {
		return nil, fmt.Errorf("select failure_signatures: %w", err)
	}
	defer rows.Close()

	out := make(map[string]uuid.UUID, len(fps))
	for rows.Next() {
		var fp string
		var id uuid.UUID
		if err := rows.Scan(&fp, &id); err != nil {
			return nil, fmt.Errorf("scan failure_signature: %w", err)
		}
		out[fp] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate failure_signatures: %w", err)
	}
	return out, nil
}
