package reports

import (
	"strings"
	"testing"
)

func TestRepositoryFilterAppendSQL(t *testing.T) {
	var b strings.Builder
	args := []any{}
	RepositoryFilter("").appendSQL(&b, &args, "repository")
	if b.Len() != 0 || len(args) != 0 {
		t.Fatal("inactive filter should not append SQL")
	}

	b.Reset()
	args = args[:0]
	RepositoryFilter("mattermost/mattermost.GIT").appendSQL(&b, &args, "repository")
	got := b.String()
	wantSQL := " AND (repository = ANY($1::text[]) OR split_part(repository, '/', 2) = ANY($1::text[]))"
	if got != wantSQL {
		t.Fatalf("sql = %q, want %q", got, wantSQL)
	}
	if len(args) != 1 {
		t.Fatalf("args = %v", args)
	}
	variants, ok := args[0].([]string)
	if !ok || len(variants) != 9 || variants[0] != "mattermost/mattermost" || variants[1] != "mattermost/mattermost.git" || variants[2] != "mattermost/mattermost.GIT" {
		t.Fatalf("variants = %v", args[0])
	}
}
