// Package identity derives framework-independent test identities and immutable observations.
package identity

import (
	"crypto/sha256"
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

var (
	mmID          = regexp.MustCompile(`^MM-T\d+(?:_\d+)?`)
	uuidToken     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	hexToken      = regexp.MustCompile(`(?i)\b(?:0x)?[0-9a-f]{8,}\b`)
	bracketNumber = regexp.MustCompile(`\[\d+\]`)
	projectPrefix = regexp.MustCompile(`^\[[^\]]+\]\s*(?:[›»:]\s*)?`)
	idSeparator   = regexp.MustCompile(`(MM-T\d+(?:_\d+)?)\s*(?:-\s*|:\s*)`)
	ansi          = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	isoTime       = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`)
	duration      = regexp.MustCompile(`\b\d+(?:\.\d+)?ms\b`)
	largeNumber   = regexp.MustCompile(`\b\d{3,}\b`)
	longQuote     = regexp.MustCompile(`"[^"\n]{41,}"|'[^'\n]{41,}'`)
	fileSuffix    = regexp.MustCompile(`\s*(?:at\s+)?\(?[^\s]+:\d+:\d+\)?\s*$`)
	locus         = regexp.MustCompile(`(?:e2e-tests/playwright/|detox/e2e/)[^\s:()]+:\d+`)
	infraFile     = regexp.MustCompile(`^ci/.*\.stub$`)
)

// MMTID returns the Zephyr identifier at the start of a leaf title.
func MMTID(title string) string { return mmID.FindString(title) }

// NormalizeTitle removes execution-specific tokens while preserving test semantics.
func NormalizeTitle(framework, fullTitle string) string {
	s := strings.TrimSpace(fullTitle)
	if framework == "playwright" {
		s = projectPrefix.ReplaceAllString(s, "")
	}
	s = idSeparator.ReplaceAllString(s, "$1 ")
	s = uuidToken.ReplaceAllString(s, "#")
	s = hexToken.ReplaceAllString(s, "#")
	s = bracketNumber.ReplaceAllString(s, "#")
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeFile removes repository workspace prefixes and normalizes separators.
func NormalizeFile(repository, file string) string {
	s := path.Clean(strings.ReplaceAll(file, `\`, "/"))
	// Workspace roots differ between GitHub workers and local backfills.
	for _, prefix := range []string{"e2e-tests/", "detox/", "ci/"} {
		if i := strings.Index("/"+s, "/"+prefix); i >= 0 {
			return ("/" + s)[i+1:]
		}
	}
	repo := path.Base(repository)
	if repo != "." && repo != "" {
		if i := strings.LastIndex(s, "/"+repo+"/"); i >= 0 {
			s = s[i+len(repo)+2:]
		}
	}
	return strings.TrimPrefix(s, "./")
}

// StableKey hashes the null-delimited repository, framework, file and title.
func StableKey(repository, framework, file, title string) []byte {
	sum := sha256.Sum256([]byte(strings.Join([]string{repository, framework, NormalizeFile(repository, file), NormalizeTitle(framework, title)}, "\x00")))
	return sum[:]
}

// ErrorSignature hashes the normalized first non-empty error line and returns its excerpt.
func ErrorSignature(message string) ([]byte, string) {
	message = ansi.ReplaceAllString(message, "")
	var s string
	for _, line := range strings.Split(message, "\n") {
		if s = strings.TrimSpace(line); s != "" {
			break
		}
	}
	if s == "" {
		return nil, ""
	}
	s = fileSuffix.ReplaceAllString(s, "")
	for _, re := range []*regexp.Regexp{isoTime, duration, uuidToken, hexToken, longQuote, largeNumber} {
		s = re.ReplaceAllString(s, "#")
	}
	s = strings.Join(strings.Fields(s), " ")
	sum := sha256.Sum256([]byte(s))
	excerpt := []rune(s)
	if len(excerpt) > 300 {
		excerpt = excerpt[:300]
	}
	return sum[:], string(excerpt)
}

// FailureLocus returns the first repository test stack frame, without the column.
func FailureLocus(stack string) string { return locus.FindString(strings.ReplaceAll(stack, `\`, "/")) }

// IsInfraStub recognizes synthetic infrastructure failures excluded from health.
func IsInfraStub(file, title string) bool {
	return infraFile.MatchString(file) || title == "CI infrastructure failure"
}

// InferBranchKind classifies legacy producer metadata conservatively.
func InferBranchKind(branch, runGroup string, pr *int) string {
	if pr != nil {
		return "pr"
	}
	if branch == "main" || branch == "master" || strings.Contains(runGroup, "-main") {
		return "trunk"
	}
	if strings.HasPrefix(branch, "release-") {
		return "release"
	}
	return "other"
}

// ValidBranchKind accepts supported branch partitions or an omitted value.
func ValidBranchKind(kind string) bool {
	return kind == "" || kind == "pr" || kind == "trunk" || kind == "release" || kind == "other"
}

// InferLane prefers explicit metadata and falls back to the producer naming
// conventions. Trunk/release runs carry a branch marker after the lane
// ("playwright-full-enterprise-master", "...-release", "...-release-cut") and
// upgrade runs carry a variant after it ("...-enterprise-upgrade-from-release-11.8");
// the marker is dropped, the variant stays part of the lane so upgrade evidence
// never mixes with the plain lane.
func InferLane(name string, metadata json.RawMessage) string {
	var env struct {
		Lane string `json:"lane"`
	}
	if json.Unmarshal(metadata, &env) == nil && env.Lane != "" {
		return env.Lane
	}
	for _, marker := range []string{"-release-cut", "-release", "-master", "-main"} {
		name = strings.TrimSuffix(name, marker)
	}
	for _, token := range []string{"detox-ios", "detox-android", "detox-ipad", "maestro-ios", "maestro-android", "enterprise", "fips"} {
		i := strings.Index(name, "-"+token)
		if i < 0 {
			continue
		}
		rest := name[i+1+len(token):]
		if rest != "" && !strings.HasPrefix(rest, "-") {
			continue
		}
		lane := strings.TrimPrefix(strings.TrimPrefix(token, "detox-"), "maestro-")
		rest = strings.TrimSuffix(rest, "-e2e")
		return lane + rest
	}
	return ""
}
