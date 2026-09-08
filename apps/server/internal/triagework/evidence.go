package triagework

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Image and harness settings come from the immutable, principal-bound receipt
// of the report containing this test. Group metadata is deliberately ignored.
func loadTrustedMetadata(ctx context.Context, tx pgx.Tx, ev *Evidence) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT r.registration_receipt->'environment_metadata'
 FROM reports r JOIN suites s ON s.report_id=r.id JOIN test_cases t ON t.suite_id=s.id
 WHERE r.report_group_id=$1 AND t.stable_key=$2`, ev.ReportGroupID, ev.StableKey)
	if err != nil {
		return err
	}
	defer rows.Close()
	var canonical []byte
	count := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		digest, normalized, e := normalizeMetadata(raw)
		if e != nil {
			return e
		}
		if count > 0 && !bytes.Equal(canonical, normalized) {
			return conflict("test reports disagree on immutable image or harness environment")
		}
		if count == 0 {
			canonical = normalized
			ev.ImageDigest = digest
			ev.EnvironmentMetadata = raw
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return conflict("test has no trusted registration environment")
	}
	return nil
}

func normalizeMetadata(raw []byte) (string, []byte, error) {
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
		return "", nil, conflict("missing trusted registration environment")
	}
	var digest string
	for _, key := range []string{"server_image_digest", "image_digest"} {
		if value := metadata[key]; value != nil {
			var candidate string
			if json.Unmarshal(value, &candidate) != nil || !imagePattern.MatchString(candidate) {
				return "", nil, conflict("master evidence requires a pullable image pinned by sha256 digest")
			}
			if digest != "" && digest != candidate {
				return "", nil, conflict("stored image digest aliases disagree")
			}
			digest = candidate
		}
	}
	if digest == "" {
		return "", nil, conflict("master evidence requires a pullable image pinned by sha256 digest")
	}
	// These fields may differ by shard; the selected test's project is derived
	// from its test row. All other settings must agree, including future fields.
	delete(metadata, "worker_index")
	delete(metadata, "project")
	delete(metadata, "server_image_digest")
	metadata["image_digest"], _ = json.Marshal(digest)
	normalized, err := json.Marshal(metadata)
	return digest, normalized, err
}
