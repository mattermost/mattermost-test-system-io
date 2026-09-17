package verdict

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// InputsHash canonicalizes sets without losing any policy or evidence changes.
func InputsHash(in Inputs) [32]byte {
	decision := Classify(in)
	decision.ComputedAt = time.Time{}
	in.Now = time.Time{}
	in.ChangedFiles = unique(in.ChangedFiles)
	in.Abandoned = unique(in.Abandoned)
	in.UnrepresentedFailures = unique(in.UnrepresentedFailures)
	in.PR = append([]triage.Observation(nil), in.PR...)
	in.Trunk = append([]triage.Observation(nil), in.Trunk...)
	in.Quarantine = append([]triage.Quarantine(nil), in.Quarantine...)
	less := func(a, b triage.Observation) bool {
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		return string(aa) < string(bb)
	}
	sort.Slice(in.PR, func(i, j int) bool { return less(in.PR[i], in.PR[j]) })
	sort.Slice(in.Trunk, func(i, j int) bool { return less(in.Trunk[i], in.Trunk[j]) })
	sort.Slice(in.Quarantine, func(i, j int) bool { return in.Quarantine[i].ID < in.Quarantine[j].ID })
	// Time only changes the hash when it changes the actual evaluation. This
	// preserves polling idempotency while retaining exact freshness/confidence
	// transitions, including transitions within the same clock hour.
	payload, _ := json.Marshal(struct {
		Version  string
		Inputs   Inputs
		Decision Verdict
	}{triage.EngineVersion, in, decision})
	return sha256.Sum256(payload)
}

func unique(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return slices.Compact(out)
}

func markdownText(s string) string {
	s = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", "&#124;", "\n", " ", "\r", " ", "`", "&#96;", "[", "&#91;", "]", "&#93;").Replace(s)
	return s
}

// Render produces escaped markdown and a GitHub-compatible short description.
func Render(v Verdict) Markdown {
	var b strings.Builder
	fmt.Fprintf(&b, "## E2E triage: %s\n\nMode: **%s** · Confidence: **%.0f%%** · Engine: `%s`\n\n", v.Verdict, markdownText(v.Mode), v.Confidence*100, markdownText(v.EngineVersion))
	if v.Reason != "" {
		fmt.Fprintf(&b, "%s\n\n", markdownText(v.Reason))
	}
	fmt.Fprintf(&b, "%d passed · %d failed · %d exonerated · %d blocking · %d infra\n\n", v.Counts.Passed, v.Counts.Failed, v.Counts.Exonerated, v.Counts.Blocking, v.Counts.Infra)
	if v.HumanOverride != nil {
		fmt.Fprintf(&b, "**Human override:** %s applied %s → %s. The engine verdict remains available for audit.\n\n", markdownText(v.HumanOverride.Actor), markdownText(v.HumanOverride.Label), markdownText(v.HumanOverride.ResultingState))
	}
	if len(v.Findings) > 0 {
		b.WriteString("| Test | Classification | Trunk runs / failures / flakes | Explanation |\n| --- | --- | --- | --- |\n")
		for _, f := range v.Findings {
			fmt.Fprintf(&b, "| %s | %s | %d / %d / %d | %s |\n", markdownText(f.FullTitle), f.Class, f.Trunk.Runs, f.Trunk.Fails, f.Trunk.Flaky, markdownText(f.Reason))
		}
	}
	fmt.Fprintf(&b, "\nThresholds: %d days, %d runs, minimum %d runs; probability ≥ %.3f; confidence ≥ %.2f.\n", v.ThresholdsUsed.WindowDays, v.ThresholdsUsed.MaxTrunkRuns, v.ThresholdsUsed.MinTrunkRuns, v.ThresholdsUsed.PMin, v.ThresholdsUsed.ConfidenceFloor)
	summary := b.String()
	comment := summary + "\nExisting repository override labels (E2E/Override, E2E/Verified, or E2E Tests/verified) take precedence.\n"
	description := fmt.Sprintf("%s: %d passed, %d failed, %d exonerated, %d blocking · triage", v.Verdict, v.Counts.Passed, v.Counts.Failed, v.Counts.Exonerated, v.Counts.Blocking)
	if len([]rune(description)) > 140 {
		description = string([]rune(description)[:140])
	}
	return Markdown{CheckSummary: summary, PRComment: comment, StatusDescription: description}
}
