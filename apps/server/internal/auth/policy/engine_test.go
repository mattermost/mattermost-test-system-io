package policy

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/oidc"
)

func s(s string) *string { return &s }

func TestMatch_firstMatchingRuleWins(t *testing.T) {
	rules := []Rule{
		{Priority: 1, MatchRepositoryOwner: s("mattermost"), GrantRole: RoleUploader},
		{Priority: 2, MatchRepositoryOwner: s("other"), GrantRole: RoleAdmin},
	}
	claims := oidc.Claims{Repository: "mattermost/repo", RepositoryOwner: "mattermost"}
	got, err := Match(rules, claims)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got != RoleUploader {
		t.Errorf("got %q, want %q", got, RoleUploader)
	}
}

func TestMatch_wildcardAcceptsAnything(t *testing.T) {
	rules := []Rule{
		// All-wildcard rule — matches anything.
		{Priority: 100, GrantRole: RoleViewer},
	}
	claims := oidc.Claims{Repository: "any/thing", RepositoryOwner: "nobody"}
	got, err := Match(rules, claims)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got != RoleViewer {
		t.Errorf("wildcard rule should grant RoleViewer, got %q", got)
	}
}

func TestMatch_noMatchingRuleIsDenied(t *testing.T) {
	rules := []Rule{
		{MatchRepository: s("fooorg/bar"), GrantRole: RoleUploader},
	}
	claims := oidc.Claims{Repository: "otherorg/baz"}
	_, err := Match(rules, claims)
	if !errors.Is(err, ErrDenied) {
		t.Errorf("expected ErrDenied, got %v", err)
	}
}

func TestMatch_emptyRulesIsDenied(t *testing.T) {
	_, err := Match(nil, oidc.Claims{Repository: "anything"})
	if !errors.Is(err, ErrDenied) {
		t.Errorf("empty rules should deny, got %v", err)
	}
}

func TestMatch_explicitDenyRuleShortCircuits(t *testing.T) {
	rules := []Rule{
		{Priority: 1, MatchRef: s("refs/heads/main"), GrantRole: RoleDenied},
		{Priority: 2, GrantRole: RoleUploader}, // wildcard grant
	}
	claims := oidc.Claims{Ref: "refs/heads/main", Repository: "anything"}
	_, err := Match(rules, claims)
	if !errors.Is(err, ErrDenied) {
		t.Errorf("explicit deny should win over later wildcard grant, got %v", err)
	}
}

func TestMatch_multipleFieldsAllRequired(t *testing.T) {
	rule := Rule{
		MatchRepositoryOwner: s("mattermost"),
		MatchWorkflow:        s("ci"),
		MatchRef:             s("refs/heads/main"),
		GrantRole:            RoleAdmin,
	}

	cases := []struct {
		name   string
		claims oidc.Claims
		want   Role
		err    bool
	}{
		{
			name: "all match",
			claims: oidc.Claims{
				RepositoryOwner: "mattermost", Workflow: "ci", Ref: "refs/heads/main",
			},
			want: RoleAdmin,
		},
		{
			name: "workflow mismatch",
			claims: oidc.Claims{
				RepositoryOwner: "mattermost", Workflow: "release", Ref: "refs/heads/main",
			},
			err: true,
		},
		{
			name: "ref mismatch",
			claims: oidc.Claims{
				RepositoryOwner: "mattermost", Workflow: "ci", Ref: "refs/heads/feature",
			},
			err: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Match([]Rule{rule}, c.claims)
			if c.err {
				if !errors.Is(err, ErrDenied) {
					t.Errorf("expected ErrDenied, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMatchStrHelper(t *testing.T) {
	if !matchStr(nil, "anything") {
		t.Error("nil rule (wildcard) should match anything")
	}
	if !matchStr(s("foo"), "foo") {
		t.Error("exact match should return true")
	}
	if matchStr(s("foo"), "bar") {
		t.Error("mismatch should return false")
	}
	if !matchStr(s(""), "") {
		t.Error("empty rule should match empty claim")
	}
	if matchStr(s(""), "something") {
		t.Error("empty rule should NOT match non-empty claim")
	}
}
