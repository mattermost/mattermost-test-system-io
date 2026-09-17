package verdict

import (
	"fmt"
	"sort"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

func applyClusters(v *Verdict, in Inputs, trunk map[string][]triage.Observation) {
	known := map[string]bool{}
	allowed := map[string]bool{}
	for _, observations := range trunk {
		for _, o := range triage.Trials(observations, in.Now, in.Policy.Thresholds) {
			allowed[o.ReportGroupID] = true
		}
	}
	for _, o := range in.Trunk {
		if allowed[o.ReportGroupID] && o.BranchKind == triage.BranchTrunk && o.Branch == in.BaseRef && o.Lane == in.Lane && !o.IsInfraStub && triage.IsFailure(o.Status) && o.ErrorSignature != "" && !o.ObservedAt.Before(in.Now.Add(-time.Duration(in.Policy.Thresholds.WindowDays)*24*time.Hour)) && !o.ObservedAt.After(in.Now) {
			known[o.ErrorSignature] = true
		}
	}
	// A signature that already recurs on other PRs is not novel to this PR.
	for sig, prs := range in.CrossPRSignatures {
		if prs >= 2 {
			known[sig] = true
		}
	}
	clusters := map[string][]int{}
	for i, f := range v.Findings {
		if f.Signature != "" {
			clusters[f.Signature] = append(clusters[f.Signature], i)
		}
	}
	for sig, indices := range clusters {
		if known[sig] || len(indices) < in.Policy.Thresholds.ClusterMin {
			continue
		}
		for _, index := range indices {
			f := &v.Findings[index]
			// The hard infra rule and explicit PR ownership take precedence over
			// cluster relabeling, and a finding whose failure already fuzzy-matches
			// trunk's dominant failure (locus or Jaccard) is a known failure even
			// when its exact signature hash drifted.
			if f.Class == classInfra || f.Class == "OWNED_BY_PR" || f.PR.SignatureMatch {
				continue
			}
			f.Class = "REGRESSION_CLUSTER"
			f.Blocking = true
			f.Reason = fmt.Sprintf("%d tests share a failure signature never seen on trunk in this window.", len(indices))
		}
	}
}

func applyAreas(v *Verdict, in Inputs, trunk map[string][]triage.Observation) {
	groups := map[string][]triage.Observation{}
	for _, observations := range trunk {
		for _, o := range triage.Trials(observations, in.Now, in.Policy.Thresholds) {
			groups[o.ReportGroupID] = append(groups[o.ReportGroupID], o)
		}
	}
	order := make([]string, 0, len(groups))
	for key := range groups {
		order = append(order, key)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := groups[order[i]][0], groups[order[j]][0]
		if triage.RunTime(a).Equal(triage.RunTime(b)) {
			return order[i] > order[j]
		}
		return triage.RunTime(a).After(triage.RunTime(b))
	})
	// Use the whole evidence window (the same runs the flake statistics saw).
	// Looking only at the last ten runs blocked tests that flaked earlier in
	// the window but not recently, which contradicted FLAKY_CONFIRMED.
	maxFails := map[string]int{}
	for _, id := range order {
		counts := map[string]int{}
		for _, o := range groups[id] {
			if triage.IsFailure(o.Status) {
				counts[o.File]++
			}
		}
		for file, count := range counts {
			maxFails[file] = max(maxFails[file], count)
		}
	}
	byFile := map[string][]int{}
	for i, f := range v.Findings {
		if f.Class != classInfra {
			byFile[f.File] = append(byFile[f.File], i)
		}
	}
	for file, indices := range byFile {
		excess := len(indices) - maxFails[file] - in.Policy.Thresholds.AreaSlack
		// Existing blocking findings already account for excess failures; only upgrade additional exonerations.
		for _, i := range indices {
			if v.Findings[i].Blocking {
				excess--
			}
		}
		for j := len(indices) - 1; j >= 0 && excess > 0; j-- {
			f := &v.Findings[indices[j]]
			// Cross-PR evidence is per identity and stronger than a per-file
			// trunk maximum, so it is not second-guessed by the area rule.
			if f.Blocking || f.Class == classBrokenOnTrunk || f.Class == classInsufficient || f.Class == classFlakyCrossPR {
				continue
			}
			// ASSUMPTION: explicit quarantine remains subject to the systemic-breakage guards, like other exonerations.
			f.Class = "REGRESSION_AREA"
			f.Blocking = true
			f.Reason = fmt.Sprintf("This file has %d PR failures; trunk runs in the window had at most %d (slack %d).", len(indices), maxFails[file], in.Policy.Thresholds.AreaSlack)
			excess--
		}
	}
}
