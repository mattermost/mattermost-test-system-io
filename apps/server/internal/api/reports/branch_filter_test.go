package reports

import (
	"strings"
	"testing"
)

func TestBranchFilterAppendSQL(t *testing.T) {
	var b strings.Builder
	args := []any{}
	BranchFilter("").appendSQL(&b, &args)
	if b.Len() != 0 {
		t.Fatal("empty filter should not append SQL")
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterMainMaster).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "branch = $1") || !strings.Contains(b.String(), "branch = $6") {
		t.Fatalf("sql = %q", b.String())
	}
	if len(args) != 6 {
		t.Fatalf("args = %v", args)
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterRelease).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "release-") {
		t.Fatalf("sql = %q", b.String())
	}

	b.Reset()
	args = args[:0]
	BranchFilter(branchFilterPR).appendSQL(&b, &args)
	if !strings.Contains(b.String(), "gh_pr_number") {
		t.Fatalf("sql = %q", b.String())
	}
}

func TestResolveBranchFilter(t *testing.T) {
	opts := []BranchFilterOption{{Value: branchFilterMainMaster, Label: "master"}}
	if got := ResolveBranchFilter("", opts); got != branchFilterMainMaster {
		t.Fatalf("got %q", got)
	}
	if got := ResolveBranchFilter("kind:pr", opts); got != "kind:pr" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveBranchFilter("", nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
func TestClassifyBranchFilterRow(t *testing.T) {
	pr := 99
	kind, sawMain, ok := classifyBranchFilterRow("main", &pr)
	if !ok || kind != branchKindPR || sawMain {
		t.Fatalf("kind=%d sawMain=%v ok=%v", kind, sawMain, ok)
	}
	kind, sawMain, ok = classifyBranchFilterRow("refs/heads/master", nil)
	if !ok || kind != branchKindMainMaster || sawMain {
		t.Fatalf("kind=%d sawMain=%v", kind, sawMain)
	}
	kind, _, ok = classifyBranchFilterRow("release-10.5", nil)
	if !ok || kind != branchKindRelease {
		t.Fatalf("kind=%d", kind)
	}
	if _, _, ok = classifyBranchFilterRow("release", nil); ok {
		t.Fatal("bare release branch should be excluded")
	}
	kind, _, ok = classifyBranchFilterRow("pr-12", nil)
	if !ok || kind != branchKindPR {
		t.Fatalf("kind=%d", kind)
	}
	kind, sawMain, ok = classifyBranchFilterRow("main", nil)
	if !ok || kind != branchKindMainMaster || !sawMain {
		t.Fatalf("kind=%d sawMain=%v", kind, sawMain)
	}
}
