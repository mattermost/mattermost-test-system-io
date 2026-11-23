package orchestration

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestUUIDStringsNonNil(t *testing.T) {
	t.Parallel()

	empty, err := json.Marshal(uuidStrings(nil))
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if string(empty) != "[]" {
		t.Fatalf("uuidStrings(nil) marshaled to %s, want []", empty)
	}

	id := uuid.Must(uuid.NewV7())
	got := uuidStrings([]uuid.UUID{id})
	if len(got) != 1 || got[0] != id.String() {
		t.Fatalf("uuidStrings([%s]) = %v", id, got)
	}
}
