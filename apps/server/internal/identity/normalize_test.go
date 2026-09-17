package identity

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInferLane(t *testing.T) {
	for name, want := range map[string]string{
		"mobile-pr-detox-ios":                                      "ios",
		"mobile-main-detox-android":                                "android",
		"mobile-pr-maestro-ios-e2e":                                "ios",
		"mobile-main-maestro-android-e2e":                          "android",
		"playwright-full-enterprise":                               "enterprise",
		"playwright-full-enterprise-master":                        "enterprise",
		"playwright-full-fips-master":                              "fips",
		"playwright-full-fips-release":                             "fips",
		"playwright-full-enterprise-release-cut":                   "enterprise",
		"playwright-full-enterprise-upgrade-from-release-11.8":     "enterprise-upgrade-from-release-11.8",
		"playwright-full-enterprise-upgrade-from-release-11.7-esr": "enterprise-upgrade-from-release-11.7-esr",
		"cypress-full-enterprise":                                  "enterprise",
		"mobile-pr":                                                "",
	} {
		if got := InferLane(name, nil); got != want {
			t.Errorf("InferLane(%q) = %q, want %q", name, got, want)
		}
	}
	if got := InferLane("playwright-full-enterprise-master", json.RawMessage(`{"lane":"custom"}`)); got != "custom" {
		t.Errorf("metadata lane must win, got %q", got)
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct{ name, framework, in, want string }{
		{"playwright project", "playwright", "[chromium] › MM-T5640_1 should work", "MM-T5640_1 should work"},
		{"project space", "playwright", "[enterprise] MM-T1 test", "MM-T1 test"},
		{"project colon", "playwright", "[fips]: MM-T1 test", "MM-T1 test"},
		{"detox separator", "detox", "MM-T6206_1 - should display", "MM-T6206_1 should display"},
		{"colon", "detox", "MM-T1: works", "MM-T1 works"},
		{"nested separator", "detox", "suite MM-T1 - works", "suite MM-T1 works"},
		{"whitespace", "detox", "  suite\t MM-T1\n works  ", "suite MM-T1 works"},
		{"uuid", "detox", "MM-T1 id 550e8400-e29b-41d4-a716-446655440000", "MM-T1 id #"},
		{"uuid uppercase", "playwright", "MM-T1 550E8400-E29B-41D4-A716-446655440000", "MM-T1 #"},
		{"hex", "playwright", "MM-T1 deadbeef", "MM-T1 #"},
		{"hex long", "playwright", "MM-T1 cafebabedeadbeef", "MM-T1 #"},
		{"hex prefix", "detox", "MM-T1 0xdeadbeef", "MM-T1 #"},
		{"short hex retained", "detox", "MM-T1 abcdefg", "MM-T1 abcdefg"},
		{"bracket integer", "playwright", "MM-T1 test [42]", "MM-T1 test #"},
		{"multiple brackets", "detox", "MM-T1 [1] [12]", "MM-T1 # #"},
		{"unbracketed integer retained", "detox", "MM-T1 test 123", "MM-T1 test 123"},
		{"id retained", "detox", "MM-T12345678_9999 works", "MM-T12345678_9999 works"},
		{"rename retained", "detox", "MM-T1 renamed test", "MM-T1 renamed test"},
		{"empty", "detox", "", ""},
		{"blank", "playwright", " \n ", ""},
		{"unicode", "detox", "MM-T1 café 日本語", "MM-T1 café 日本語"},
		{"hyphen inside title", "detox", "MM-T1 a - b", "MM-T1 a - b"},
		{"colon inside title", "playwright", "MM-T1 a: b", "MM-T1 a: b"},
		{"detox bracket prefix retained", "detox", "[suite] MM-T1 work", "[suite] MM-T1 work"},
		{"leading zeros", "detox", "MM-T001_02 - works", "MM-T001_02 works"},
		{"bracket text", "detox", "MM-T1 [admin] works", "MM-T1 [admin] works"},
		{"negative bracket", "detox", "MM-T1 [-1] works", "MM-T1 [-1] works"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTitle(tt.framework, tt.in); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}
func TestStableKey(t *testing.T) {
	a := StableKey("mattermost/mobile", "detox", "/home/runner/work/mobile/mobile/detox/e2e/a.js", "MM-T1 - test")
	b := StableKey("mattermost/mobile", "detox", "detox/e2e/a.js", "MM-T1: test")
	if !bytes.Equal(a, b) {
		t.Fatal("workspace or separator forked identity")
	}
	for _, key := range [][]byte{StableKey("mattermost/mobile", "detox", "detox/e2e/a.js", "MM-T1 renamed"), StableKey("mattermost/mobile", "detox", "detox/e2e/b.js", "MM-T1 test"), StableKey("mattermost/other", "detox", "detox/e2e/a.js", "MM-T1 test"), StableKey("mattermost/mobile", "playwright", "detox/e2e/a.js", "MM-T1 test")} {
		if bytes.Equal(a, key) {
			t.Fatal("rename/file/repository/framework collision")
		}
	}
	if bytes.Equal(StableKey("ab", "c", "d", "e"), StableKey("a", "bc", "d", "e")) {
		t.Fatal("ambiguous key encoding")
	}
}
func TestErrorSignature(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\n\x1b[31mError timeout 123ms\x1b[0m\nstack", "Error timeout #"},
		{"at 2026-09-16T10:20:30.123Z error 1234", "at # error #"},
		{"Expected 12 got 123", "Expected 12 got #"},
		{"Error deadbeef 550e8400-e29b-41d4-a716-446655440000", "Error # #"},
		{"Error bad at /repo/a.ts:123:4", "Error bad"},
		{"Error \"" + strings.Repeat("x", 41) + "\"", "Error #"},
		{"  \n\t", ""},
	}
	for _, tt := range cases {
		sig, excerpt := ErrorSignature(tt.in)
		if excerpt != tt.want {
			t.Errorf("%q => %q want %q", tt.in, excerpt, tt.want)
		}
		if tt.want != "" && len(sig) != 32 {
			t.Fatal("not sha256")
		}
	}
	_, excerpt := ErrorSignature(strings.Repeat("界", 310))
	if len([]rune(excerpt)) != 300 {
		t.Fatal("excerpt rune limit")
	}
	a, _ := ErrorSignature("Error 123ms")
	b, _ := ErrorSignature("Error 456ms")
	if !bytes.Equal(a, b) {
		t.Fatal("duration drift changed signature")
	}
}
func TestMetadataAndLocus(t *testing.T) {
	if got := FailureLocus("Error\n at /repo/e2e-tests/playwright/specs/a.ts:42:4\n at detox/e2e/b.js:10"); got != "e2e-tests/playwright/specs/a.ts:42" {
		t.Fatal(got)
	}
	if !IsInfraStub("ci/job.stub", "x") || !IsInfraStub("x", "CI infrastructure failure") || IsInfraStub("ci/job.js", "x") {
		t.Fatal("infra detection")
	}
	pr := 1
	for _, tt := range []struct {
		branch, run string
		pr          *int
		want        string
	}{{"main", "", nil, "trunk"}, {"master", "", nil, "trunk"}, {"x", "mobile-main-ios", nil, "trunk"}, {"main", "", &pr, "pr"}, {"release-1.0", "", nil, "release"}, {"other", "", nil, "other"}} {
		if got := InferBranchKind(tt.branch, tt.run, tt.pr); got != tt.want {
			t.Fatal(got, tt.want)
		}
	}
	for _, tt := range []struct{ name, env, want string }{{"mobile-pr-detox-ios", "{}", "ios"}, {"mobile-main-detox-ipad", "{}", "ipad"}, {"playwright-full-enterprise", "{}", "enterprise"}, {"unknown", `{"lane":"custom"}`, "custom"}, {"unknown", "{}", ""}} {
		if got := InferLane(tt.name, json.RawMessage(tt.env)); got != tt.want {
			t.Fatal(got, tt.want)
		}
	}
	if MMTID("MM-T123_4 test") != "MM-T123_4" || MMTID("suite MM-T123") == "MM-T123" {
		t.Fatal("leaf id")
	}
}
