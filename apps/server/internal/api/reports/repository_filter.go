package reports

import (
	"fmt"
	"strings"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
)

// RepositoryOption is a home repo filter option.
type RepositoryOption struct {
	Repository     string `json:"repository"`
	RepositoryName string `json:"repository_name"`
}

// RepositoryFilter matches a full repo slug or trailing segment.
type RepositoryFilter string

// Active reports whether a filter is set.
func (f RepositoryFilter) Active() bool {
	return authapi.NormalizeRepository(string(f)) != ""
}

// Value returns the raw filter value.
func (f RepositoryFilter) Value() string {
	return authapi.NormalizeRepository(string(f))
}

// SQL AND clause for repository filter.
func (f RepositoryFilter) appendSQL(b *strings.Builder, args *[]any, column string) {
	if !f.Active() {
		return
	}
	ph := fmt.Sprintf("$%d", len(*args)+1)
	b.WriteString(" AND (")
	b.WriteString(column)
	b.WriteString(" = ANY(")
	b.WriteString(ph)
	b.WriteString("::text[]) OR split_part(")
	b.WriteString(column)
	b.WriteString(", '/', 2) = ANY(")
	b.WriteString(ph)
	b.WriteString("::text[]))")
	*args = append(*args, authapi.RepositoryLookupVariants(string(f)))
}
