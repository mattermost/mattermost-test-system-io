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
	RepositoryFilter("mattermost/mattermost").appendSQL(&b, &args, "repository")
	got := b.String()
	if got != " AND (repository = $1 OR split_part(repository, '/', 2) = $1)" {
		t.Fatalf("sql = %q", got)
	}
	if len(args) != 1 || args[0] != "mattermost/mattermost" {
		t.Fatalf("args = %v", args)
	}
}
