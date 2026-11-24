package ingest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractZephyrKey(t *testing.T) {
	cases := []struct {
		name      string
		fullTitle string
		title     string
		tags      []string
		want      string
	}{
		{
			name:      "underscore suffix boundary MM-T5294_3",
			fullTitle: "Search - Search Messages MM-T5294_3 - should search messages",
			title:     "MM-T5294_3 - should search messages",
			want:      "MM-T5294",
		},
		{
			name:      "leading key in title",
			fullTitle: "Channel > MM-T1 renders",
			title:     "MM-T1 renders",
			want:      "MM-T1",
		},
		{
			name:      "lowercase normalized to upper",
			fullTitle: "Suite > [mm-t42] opens modal",
			title:     "[mm-t42] opens modal",
			want:      "MM-T42",
		},
		{
			name:      "tags fallback with at-prefix",
			fullTitle: "Login > logs in with SSO",
			title:     "logs in with SSO",
			tags:      []string{"@smoke", "@mm-t777"},
			want:      "MM-T777",
		},
		{
			name:      "title takes precedence over ancestor suite key",
			fullTitle: "Suite MM-T100 > MM-T200 child test",
			title:     "MM-T200 child test",
			want:      "MM-T200",
		},
		{
			name:      "ancestor fullTitle used when title omits key",
			fullTitle: "Suite MM-T100 > renders",
			title:     "renders",
			want:      "MM-T100",
		},
		{
			name:      "rejects embedded alphanumeric tokens and missing digits",
			fullTitle: "fooMM-T12 MM-T12bar MM-T",
			title:     "fooMM-T12 MM-T12bar MM-T",
			tags:      []string{"XMM-T99"},
			want:      "",
		},
		{
			name:      "strips NUL inside digits",
			fullTitle: "Suite > MM-T12\x003 test",
			title:     "MM-T12\x003 test",
			want:      "MM-T123",
		},
		{
			name:      "strips NUL after MM-T prefix",
			fullTitle: "Suite > MM-T\x00123 test",
			title:     "MM-T\x00123 test",
			want:      "MM-T123",
		},
		{
			name:      "strips invalid UTF-8 bytes",
			fullTitle: "Suite > MM-T\xff\xfe123 test",
			title:     "MM-T\xff\xfe123 test",
			want:      "MM-T123",
		},
		{
			name:      "strips NUL inside tag",
			fullTitle: "Suite > test",
			title:     "test",
			tags:      []string{"@MM-T\x00123"},
			want:      "MM-T123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractZephyrKey(tc.fullTitle, tc.title, tc.tags); got != tc.want {
				t.Fatalf("ExtractZephyrKey(%q, %q, %v) = %q, want %q",
					tc.fullTitle, tc.title, tc.tags, got, tc.want)
			}
		})
	}
}

func TestNormalizeTestFile(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{".", ""},
		{"./", ""},
		{"/github/workspace/", ""},
		{"/github/workspace/.", ""},
		{"/github/workspace/./", ""},
		{"/github/workspace//tests/a.spec.ts", "tests/a.spec.ts"},
		{"/github/workspace2/tests/a.spec.ts", "/github/workspace2/tests/a.spec.ts"},
		{"./e2e/login.spec.ts", "e2e/login.spec.ts"},
		{`e2e\channels\post.cy.ts`, "e2e/channels/post.cy.ts"},
		{"/home/runner/work/mattermost-mobile/mattermost-mobile/e2e/detox/test/foo.e2e.ts", "e2e/detox/test/foo.e2e.ts"},
		{"/github/workspace/e2e/maestro/flows/login.yml", "e2e/maestro/flows/login.yml"},
		{"/github/workspace/./tests/a.spec.ts", "tests/a.spec.ts"},
		{"/home/runner/work/mattermost/mattermost/./tests/a.spec.ts", "tests/a.spec.ts"},
		{"././tests/a.spec.ts", "tests/a.spec.ts"},
		{"tests/./a.spec.ts", "tests/a.spec.ts"},
		{`C:\a\b`, "C:/a/b"},
		{"/work/mattermost/mattermost-webapp/tests/a.spec.ts", "/work/mattermost/mattermost-webapp/tests/a.spec.ts"},
		{"e2e/bad\x00file\xff\xfe.spec.ts", "e2e/badfile.spec.ts"},
	}
	for _, tc := range cases {
		if got := NormalizeTestFile(tc.in); got != tc.want {
			t.Errorf("NormalizeTestFile(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeTestTitle(t *testing.T) {
	if got := NormalizeTestTitle("  Suite  >\t Sub \n  test  "); got != "Suite > Sub test" {
		t.Fatalf("NormalizeTestTitle = %q, want %q", got, "Suite > Sub test")
	}
	if got := NormalizeTestTitle("Title\x00with\xff\xfebad"); got != "Titlewithbad" {
		t.Fatalf("NormalizeTestTitle NUL/UTF-8 = %q, want %q", got, "Titlewithbad")
	}
}

func TestDeriveTestKey(t *testing.T) {
	// Zephyr-keyed test stays stable across file moves and title rewording.
	k1, z1 := DeriveTestKey("mattermost/mattermost", "a.spec.ts", "Suite > MM-T5294_3 old title", "MM-T5294_3 old title", nil)
	k2, z2 := DeriveTestKey("mattermost/mattermost", "b.spec.ts", "Suite > new title", "new title", []string{"@mm-t5294"})
	if k1 != "zephyr:MM-T5294" || z1 != "MM-T5294" {
		t.Fatalf("got (%q, %q), want (zephyr:MM-T5294, MM-T5294)", k1, z1)
	}
	if k1 != k2 || z1 != z2 {
		t.Fatalf("zephyr key drifted across rename/tag: (%q,%q) vs (%q,%q)", k1, z1, k2, z2)
	}

	// Non-Zephyr test uses h:<sha256> and is invariant under path/whitespace normalization
	// and independent of project/platform (neither is part of DeriveTestKey).
	h1, zh1 := DeriveTestKey("mattermost/mattermost", "./e2e/login.spec.ts", "Login > logs in", "logs in", nil)
	h2, zh2 := DeriveTestKey("mattermost/mattermost", `e2e\login.spec.ts`, "  Login  >  logs  in ", "logs in", nil)
	if zh1 != "" || zh2 != "" {
		t.Fatalf("unexpected zephyr key: %q / %q", zh1, zh2)
	}
	if !strings.HasPrefix(h1, "h:") || len(h1) != 66 {
		t.Fatalf("unexpected hash key format: %q", h1)
	}
	if h1 != h2 {
		t.Fatalf("hash key not invariant under normalization: %q vs %q", h1, h2)
	}

	// Changing repository, file, or fullTitle changes the hash key.
	hDiffRepo, _ := DeriveTestKey("mattermost/desktop", "e2e/login.spec.ts", "Login > logs in", "logs in", nil)
	hDiffFile, _ := DeriveTestKey("mattermost/mattermost", "e2e/other.spec.ts", "Login > logs in", "logs in", nil)
	hDiffTitle, _ := DeriveTestKey("mattermost/mattermost", "e2e/login.spec.ts", "Login > logs out", "logs out", nil)
	if h1 == hDiffRepo || h1 == hDiffFile || h1 == hDiffTitle {
		t.Fatalf("hash key collided across repo/file/title: base=%q repo=%q file=%q title=%q",
			h1, hDiffRepo, hDiffFile, hDiffTitle)
	}

	// Workspace prefix + residual ./ variants yield identical DeriveTestKey hashes.
	baseKey, _ := DeriveTestKey("mattermost/mattermost", "./tests/a.spec.ts", "Suite > case", "case", nil)
	for _, pathVariant := range []string{
		"/github/workspace/./tests/a.spec.ts",
		"/home/runner/work/mattermost/mattermost/./tests/a.spec.ts",
		"././tests/a.spec.ts",
		"tests/./a.spec.ts",
	} {
		gotKey, _ := DeriveTestKey("mattermost/mattermost", pathVariant, "Suite > case", "case", nil)
		if gotKey != baseKey {
			t.Fatalf("DeriveTestKey(%q) = %q, want %q", pathVariant, gotKey, baseKey)
		}
	}

	// NUL and invalid UTF-8 bytes are stripped before hashing.
	dirtyKey, _ := DeriveTestKey("mattermost/mattermost\x00", "tests/a\x00\xff\xfe.spec.ts", "Suite > \x00case\xff\xfe", "case", nil)
	cleanKey, _ := DeriveTestKey("mattermost/mattermost", "tests/a.spec.ts", "Suite > case", "case", nil)
	if dirtyKey != cleanKey {
		t.Fatalf("dirty vs clean DeriveTestKey mismatch: %q vs %q", dirtyKey, cleanKey)
	}
}

func TestEncodeAnnotations_SanitizesNULPreservesEscapedBackslash(t *testing.T) {
	got := string(encodeAnnotations([]json.RawMessage{
		json.RawMessage(`{"type":"issue","description":"bad\u0000desc","escaped":"keep\\u0000literal"}`),
	}))
	if strings.Contains(got, `\u0000`) && !strings.Contains(got, `keep\\u0000literal`) {
		t.Fatalf("unexpected encoded annotations: %s", got)
	}
	if !strings.Contains(got, `"baddesc"`) || !strings.Contains(got, `"keep\\u0000literal"`) {
		t.Fatalf("encodeAnnotations did not sanitize NUL / preserve escaped backslash cleanly: %s", got)
	}
}
