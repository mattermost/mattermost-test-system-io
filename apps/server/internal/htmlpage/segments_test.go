package htmlpage

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
)

func TestIsRepoPageSegment(t *testing.T) {
	for _, tc := range []struct {
		seg  string
		want bool
	}{
		{"mattermost", true},
		{"mattermost-mobile", true},
		{"r", false},
		{"fragment", false},
		{"abc1234", false},
		{"abcdef0123456789abcdef0123456789abcdef01", false},
		{"", false},
	} {
		if got := IsRepoPageSegment(tc.seg); got != tc.want {
			t.Fatalf("IsRepoPageSegment(%q) = %v, want %v", tc.seg, got, tc.want)
		}
	}
}

func TestRenderRepoPage(t *testing.T) {
	html, err := RenderRepo(RepoPage{
		Title:           "mattermost · Test System IO",
		RepoSlug:        "mattermost",
		RepoDisplayName: "mattermost",
		BranchFilterOptions: []reports.BranchFilterOption{
			{Value: "branch:main-master", Label: "master"},
		},
		SelectedBranchFilter: "branch:main-master",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`data-repository="mattermost"`,
		`breadcrumb-link">Reports`,
		`breadcrumb-chevron`,
		`breadcrumb-current">mattermost`,
		`Filter by branch or pull request`,
		`id="filter-branch"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
