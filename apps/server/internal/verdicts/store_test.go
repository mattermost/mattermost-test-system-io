package verdicts

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPrepareBatchInput(t *testing.T) {
	t.Run("validates repository, items count, label, confidence, error_context", func(t *testing.T) {
		for _, badRepo := range []string{"   ", ".git", ".GIT"} {
			_, _, err := prepareBatchInput(BatchInput{Repository: badRepo, Items: []CreateItem{{Label: "flaky"}}})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("bad repo %q: got %v, want ErrValidation", badRepo, err)
			}
		}

		_, _, err := prepareBatchInput(BatchInput{Repository: "mattermost/mattermost", Items: nil})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("empty items: got %v, want ErrValidation", err)
		}

		tooMany := make([]CreateItem, MaxBatchSize+1)
		for i := range tooMany {
			tooMany[i] = CreateItem{Label: "flaky"}
		}
		_, _, err = prepareBatchInput(BatchInput{Repository: "mattermost/mattermost", Items: tooMany})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("oversized batch: got %v, want ErrValidation", err)
		}

		_, _, err = prepareBatchInput(BatchInput{Repository: "mattermost/mattermost", Items: []CreateItem{{Label: "  "}}})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("blank label: got %v, want ErrValidation", err)
		}

		for _, badConf := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
			c := badConf
			_, _, err = prepareBatchInput(BatchInput{
				Repository: "mattermost/mattermost",
				Items:      []CreateItem{{Label: "flaky", Confidence: &c}},
			})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("bad confidence %v: got %v, want ErrValidation", badConf, err)
			}
		}
	})

	t.Run("normalizes repository and redacts error_context", func(t *testing.T) {
		conf := 0.9
		fp := "  abc123  "
		repo, items, err := prepareBatchInput(BatchInput{
			Repository: "  mattermost/mattermost.git  ",
			Items: []CreateItem{{
				Fingerprint:  &fp,
				Label:        "caused by PR",
				Confidence:   &conf,
				ErrorContext: json.RawMessage(`{"token":"ghp_1234567890abcdef1234567890abcdef"}`),
			}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo != "mattermost/mattermost" {
			t.Fatalf("repo = %q, want mattermost/mattermost", repo)
		}
		if items[0].fingerprint == nil || *items[0].fingerprint != "abc123" {
			t.Fatalf("fingerprint = %v, want abc123", items[0].fingerprint)
		}
		if items[0].errorContext == nil || strings.Contains(*items[0].errorContext, "ghp_") {
			t.Fatalf("errorContext not redacted: %v", items[0].errorContext)
		}
	})

	t.Run("validates triage_url and rejects unsafe or credentialed URLs", func(t *testing.T) {
		for _, okURL := range []string{
			"https://github.com/mattermost/mattermost/actions/runs/123",
			"http://ci.internal:8080/runs/1",
		} {
			u := okURL
			_, items, err := prepareBatchInput(BatchInput{
				Repository: "mattermost/mattermost",
				Items:      []CreateItem{{Label: "flaky", TriageURL: &u}},
			})
			if err != nil {
				t.Fatalf("valid triage_url %q rejected: %v", okURL, err)
			}
			if items[0].triageURL == nil || *items[0].triageURL != okURL {
				t.Fatalf("triageURL = %v, want %q", items[0].triageURL, okURL)
			}
		}

		for _, emptyURL := range []string{"", "   "} {
			u := emptyURL
			_, items, err := prepareBatchInput(BatchInput{
				Repository: "mattermost/mattermost",
				Items:      []CreateItem{{Label: "flaky", TriageURL: &u}},
			})
			if err != nil {
				t.Fatalf("empty triage_url %q rejected: %v", emptyURL, err)
			}
			if items[0].triageURL != nil {
				t.Fatalf("empty triage_url %q normalized to %q, want nil", emptyURL, *items[0].triageURL)
			}
		}

		for _, badURL := range []string{
			"javascript:alert(1)",
			"https://user:pass@example.com/run/1",
			"https://example.com/run?X-Amz-Signature=abc",
			"https://example.com/ghp_1234567890abcdef1234567890abcdef",
			"https://example.com\nfoo",
			"https://example.com/alice%40example.com",
			"https://example.com/token%3Dsecret",
			"https://example.com/run#password%3Dsecret",
			"https://example.com/run#Bearer%20secret123",
		} {
			u := badURL
			_, _, err := prepareBatchInput(BatchInput{
				Repository: "mattermost/mattermost",
				Items:      []CreateItem{{Label: "flaky", TriageURL: &u}},
			})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("bad triage_url %q: got %v, want ErrValidation", badURL, err)
			}
		}
	})

	t.Run("rejects out-of-range json.Number in error_context while preserving valid PG numeric", func(t *testing.T) {
		for _, badJSON := range []string{`{"huge": 1e200000}`, `{"tiny": 1e-20000}`} {
			_, _, err := prepareBatchInput(BatchInput{
				Repository: "mattermost/mattermost",
				Items:      []CreateItem{{Label: "flaky", ErrorContext: json.RawMessage(badJSON)}},
			})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("bad numeric %s: got %v, want ErrValidation", badJSON, err)
			}
		}

		_, _, err := prepareBatchInput(BatchInput{
			Repository: "mattermost/mattermost",
			Items:      []CreateItem{{Label: "flaky", ErrorContext: json.RawMessage(`{"ok": 1e500, "password": 1e200000}`)}},
		})
		if err != nil {
			t.Fatalf("valid large numeric / redacted secret numeric failed: %v", err)
		}
	})
}

func TestVerdictDTOsNeverIncludeErrorContext(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[Verdict](), reflect.TypeFor[CreatedVerdict]()} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			tag := f.Tag.Get("json")
			if strings.Contains(strings.ToLower(f.Name), "errorcontext") || strings.Contains(tag, "error_context") {
				t.Fatalf("%s must not expose error_context field (found %s json:%q)", typ.Name(), f.Name, tag)
			}
		}
	}

	v := Verdict{
		ID:         uuid.New(),
		Repository: "mattermost/mattermost",
		Label:      "flaky",
		CreatedAt:  time.Now().UTC(),
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal Verdict: %v", err)
	}
	if strings.Contains(string(b), "error_context") {
		t.Fatalf("Verdict JSON contains error_context: %s", b)
	}

	cv := CreatedVerdict{
		ID:         uuid.New(),
		Repository: "mattermost/mattermost",
		Label:      "flaky",
		CreatedAt:  time.Now().UTC(),
		Created:    true,
	}
	cb, err := json.Marshal(cv)
	if err != nil {
		t.Fatalf("marshal CreatedVerdict: %v", err)
	}
	if strings.Contains(string(cb), "error_context") {
		t.Fatalf("CreatedVerdict JSON contains error_context: %s", cb)
	}
}
