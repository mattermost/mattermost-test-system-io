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
	RepositoryFilter("mattermost/mattermost.git").appendSQL(&b, &args, "repository")
	got := b.String()
	wantSQL := " AND (repository = $1 OR repository = $2 OR split_part(repository, '/', 2) = $1 OR split_part(repository, '/', 2) = $2)"
	if got != wantSQL {
		t.Fatalf("sql = %q, want %q", got, wantSQL)
	}
	if len(args) != 2 || args[0] != "mattermost/mattermost" || args[1] != "mattermost/mattermost.git" {
		t.Fatalf("args = %v", args)
	}
}
