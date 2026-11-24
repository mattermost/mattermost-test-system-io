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
	v := f.Value()
	ph1 := fmt.Sprintf("$%d", len(*args)+1)
	ph2 := fmt.Sprintf("$%d", len(*args)+2)
	b.WriteString(" AND (")
	b.WriteString(column)
	b.WriteString(" = ")
	b.WriteString(ph1)
	b.WriteString(" OR ")
	b.WriteString(column)
	b.WriteString(" = ")
	b.WriteString(ph2)
	b.WriteString(" OR split_part(")
	b.WriteString(column)
	b.WriteString(", '/', 2) = ")
	b.WriteString(ph1)
	b.WriteString(" OR split_part(")
	b.WriteString(column)
	b.WriteString(", '/', 2) = ")
	b.WriteString(ph2)
	b.WriteString(")")
	*args = append(*args, v, v+".git")
}
