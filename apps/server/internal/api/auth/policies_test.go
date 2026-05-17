package authapi

import "testing"

func TestParseRepositoryPattern(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		in         string
		wantRepo   *string
		wantOwner  *string
		wantOK     bool
		wantReason string
	}{
		{in: "*", wantOK: true, wantReason: "* allows everything (dev only)"},
		{in: "  ", wantOK: false, wantReason: "whitespace-only is empty after trim"},
		{in: "", wantOK: false, wantReason: "empty must not silently become an all-repos policy"},
		{in: "owner/*", wantOwner: str("owner"), wantOK: true},
		{in: "owner/repo", wantRepo: str("owner/repo"), wantOK: true},
		{in: "  owner/repo  ", wantRepo: str("owner/repo"), wantOK: true, wantReason: "trim before match"},
		{in: "/*", wantOK: false, wantReason: "missing owner"},
		{in: "a/b/c", wantOK: false, wantReason: "too many slashes"},
		{in: "owner/sub/*", wantOK: false, wantReason: "nested owner segments not allowed"},
		{in: "owner/", wantOK: false, wantReason: "missing repo half"},
		{in: "/repo", wantOK: false, wantReason: "missing owner half"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			repo, owner, ok := parseRepositoryPattern(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (%s)", ok, tc.wantOK, tc.wantReason)
			}
			if !ok {
				return
			}
			if !strPtrEq(repo, tc.wantRepo) {
				t.Errorf("matchRepo = %v, want %v", deref(repo), deref(tc.wantRepo))
			}
			if !strPtrEq(owner, tc.wantOwner) {
				t.Errorf("matchOwner = %v, want %v", deref(owner), deref(tc.wantOwner))
			}
		})
	}
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
