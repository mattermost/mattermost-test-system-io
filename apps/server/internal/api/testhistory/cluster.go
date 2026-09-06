package testhistory

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxClusters     = 15
	maxMembersShown = 20
	signatureLen    = 240
)

var (
	uuidRe  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	hexRe   = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	numRe   = regexp.MustCompile(`\d{2,}`)
	spaceRe = regexp.MustCompile(`\s+`)
	// Playwright's reporter colors its expect() failures; the escape
	// sequences are not part of the cause and must not reach the label.
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
)

type evidenceMember struct {
	ExternalTestID *string `json:"external_test_id,omitempty"`
	StableKey      string  `json:"stable_key"`
	FullTitle      string  `json:"full_title"`
	Status         string  `json:"status"`
}

type evidenceCluster struct {
	Signature      string           `json:"signature"`
	Label          string           `json:"label"`
	MemberCount    int              `json:"member_count"`
	Members        []evidenceMember `json:"members"`
	Representative evidenceFailure  `json:"representative"`
}

// clusterFailures groups failures by normalized error text. Three hundred
// "element not visible" failures are one cause, not three hundred
// investigations.
func clusterFailures(failures []evidenceFailure) ([]evidenceCluster, bool) {
	type bucket struct {
		label string
		items []evidenceFailure
	}
	bySig := map[string]*bucket{}
	order := []string{}
	for _, f := range failures {
		sig, label := signatureOf(f)
		b, ok := bySig[sig]
		if !ok {
			b = &bucket{label: label}
			bySig[sig] = b
			order = append(order, sig)
		}
		b.items = append(b.items, f)
	}

	out := make([]evidenceCluster, 0, len(order))
	for _, sig := range order {
		b := bySig[sig]
		rep := b.items[0]
		for _, it := range b.items[1:] {
			rep = mergeFailure(rep, it)
		}
		members := make([]evidenceMember, 0, len(b.items))
		for i, it := range b.items {
			if i >= maxMembersShown {
				break
			}
			members = append(members, evidenceMember{
				ExternalTestID: it.ExternalTestID,
				StableKey:      it.StableKey,
				FullTitle:      it.FullTitle,
				Status:         it.Status,
			})
		}
		out = append(out, evidenceCluster{
			Signature:      sig,
			Label:          b.label,
			MemberCount:    len(b.items),
			Members:        members,
			Representative: rep,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].MemberCount > out[j].MemberCount
	})
	truncated := len(out) > maxClusters
	if truncated {
		out = out[:maxClusters]
	}
	return out, truncated
}

func signatureOf(f evidenceFailure) (hash, label string) {
	raw := ""
	if f.ErrorMessage != nil {
		raw = *f.ErrorMessage
	}
	if raw == "" && f.ErrorStack != nil {
		raw = firstLine(*f.ErrorStack)
	}
	if raw == "" {
		raw = f.Status + ":" + f.FullTitle
	}
	label = normalizeError(raw)
	if label == "" {
		label = "unknown"
	}
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:8]), label
}

func normalizeError(msg string) string {
	s := ansiRe.ReplaceAllString(msg, "")
	s = strings.ToLower(s)
	s = uuidRe.ReplaceAllString(s, "<id>")
	s = hexRe.ReplaceAllString(s, "<hex>")
	s = numRe.ReplaceAllString(s, "<n>")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > signatureLen {
		runes := []rune(s)
		s = string(runes[:signatureLen])
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
