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
		_, _, err := prepareBatchInput(BatchInput{Repository: "   ", Items: []CreateItem{{Label: "flaky"}}})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("empty repo: got %v, want ErrValidation", err)
		}

		_, _, err = prepareBatchInput(BatchInput{Repository: "mattermost/mattermost", Items: nil})
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
