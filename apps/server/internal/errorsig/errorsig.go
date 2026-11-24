// Package errorsig implements versioned error message normalization,
// error class and top stack frame extraction, fallback signatures, and
// deterministic SHA-256 failure fingerprinting for TSIO KPI tracking.
//
// It depends only on the Go standard library and internal/teststatus.
package errorsig

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/teststatus"
)

// AlgoVersion is the frozen error signature algorithm version for SEC-11524 / SEC-11519.
const AlgoVersion int16 = 1

const (
	maxNormalizedRunes = 500
	maxSampleRunes     = 1000

	phUUID = "<uuid>"
	phHex  = "<hex>"
	phNum  = "<num>"
	phTS   = "<ts>"
	phURL  = "<url>"
	phTmp  = "<tmp>"
	phDur  = "<dur>"
	phRepo = "<repo>/"

	fallbackSpecDidNotRun = "spec-did-not-run"
	fallbackInterrupted   = "interrupted"
	fallbackTimeout       = "timeout"
	unknownToken          = "unknown"
)

// Input holds the raw failure fields extracted from a test case.
type Input struct {
	Framework    string
	Status       string
	ErrorMessage string
	ErrorStack   string
}

// Signature is the deterministic normalized signature for a test failure.
type Signature struct {
	AlgoVersion       int16
	ErrorClass        string
	NormalizedMessage string
	TopFrame          string
	SampleMessage     string
	Fallback          string
	Fingerprint       string
}

var (
	ansiCSIRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiESCRE = regexp.MustCompile(`\x1b[@-Z\\-_]`)

	decorLineRE = regexp.MustCompile(`^[\s─\-=*~│+^_]+$`)

	errClassRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*):(?:\s|$)`)

	uuidRE      = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	isoTSRE     = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`)
	urlRE       = regexp.MustCompile(`(?:https?|wss?)://[^\s"'<>)\]]+`)
	winTmpRE    = regexp.MustCompile(`[A-Za-z]:\\Users\\[^\\\s]+\\AppData\\Local\\Temp\\[^\s"')]+`)
	unixTmpRE   = regexp.MustCompile(`(?:/private)?/tmp/[^\s"')]+|/var/folders/[^\s"')]+`)
	ghWorkRE    = regexp.MustCompile(`(?:/home/runner|/Users/runner)/work/[^/\s]+/[^/\s]+/|/github/workspace/`)
	winWorkRE   = regexp.MustCompile(`[A-Za-z]:\\a\\[^\\\s]+\\[^\\\s]+\\`)
	prefixedHex = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	longHexRE   = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	mmIDRE      = regexp.MustCompile(`\b[a-z0-9]{26}\b`)
	durRE       = regexp.MustCompile(`\b(?:\d+(?:\.\d+)?\s*(?:ms|us|µs|ns|h|m|s))+\b`)
	numRE       = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	wsRE        = regexp.MustCompile(`\s+`)

	// Selector patterns protected before replacing numbers/hex so stable test IDs,
	// roles, CSS IDs/classes, and nth selectors remain intact.
	attrSelectorRE  = regexp.MustCompile(`\[[A-Za-z_:][A-Za-z0-9_.:-]*(?:[~|^$*]?=(?:"[^"]*"|'[^']*'|[^\]]*))?\]`)
	dataAttrRE      = regexp.MustCompile(`\b(?:(?:data-(?:testid|cy|test|qa)|testID|accessibilityLabel)\s*[:=]|id\s*=)\s*(?:"[^"]*"|'[^']*')`)
	locatorCallRE   = regexp.MustCompile(`\b(?:getByTestId|getByRole|getByText|getByLabel|getByPlaceholder|locator|by\.id|by\.text|by\.label)\((?:[^()'"]|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\((?:[^()'"]|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')*\))*\)`)
	nthSelectorRE   = regexp.MustCompile(`(?::nth-(?:child|of-type)|\.nth)\(\d+\)`)
	cssIdentTokenRE = regexp.MustCompile(`(^|[\s(,:'">\x00])((?:[#.][A-Za-z_-][A-Za-z0-9_-]*)+)`)

	bareFrameRE     = regexp.MustCompile(`^(?:(?:[A-Za-z]:[\\/]|/|file://(?:/?[A-Za-z]:[\\/]|/)?)[^\s\\/:(][^\t\r\n(:]*|(?:webpack://[^/\s]*/|https?://[^\s/]+/)?[^\s(:]+)\.(?:[cm]?[jt]sx?|cy\.[jt]s|ya?ml|kt|java|go):\d+(?::\d+)?$`)
	webpackPrefixRE = regexp.MustCompile(`webpack://[^/]*/(?:\./)?`)
	cypressProxyRE  = regexp.MustCompile(`https?://[^/]+/__cypress/tests\?p=`)
)

var runtimeFrameSubstrings = []string{
	"node_modules/",
	`node_modules\`,
	"node:internal/",
	"(internal/",
	"webpack/bootstrap",
	"playwright-core/",
	"@playwright/test/",
	"__cypress/runner/",
	"maestro.orchestra.",
	"maestro.cli.",
	"java.lang.",
	"java.util.",
	"kotlin.",
	"io.grpc.",
	"okhttp3.",
}

// Compute returns the frozen algo_version=1 failure signature.
// TopFrame is always extracted and populated on the returned Signature,
// while Fingerprint hashes (AlgoVersion, ErrorClass, NormalizedMessage).
func Compute(in Input) Signature {
	return ComputeWithOptions(in, false)
}

// ComputeWithOptions computes a failure signature, optionally including
// TopFrame inside the SHA-256 fingerprint for split/merge evaluation.
func ComputeWithOptions(in Input, includeTopFrame bool) Signature {
	topFrame := ExtractTopFrame(in.ErrorStack, in.ErrorMessage)
	firstLine := firstMeaningfulLine(in.ErrorMessage, false)
	if firstLine == "" {
		firstLine = firstMeaningfulLine(in.ErrorStack, true)
	}

	if strings.EqualFold(firstLine, fallbackSpecDidNotRun) {
		sig := Signature{
			AlgoVersion:       AlgoVersion,
			NormalizedMessage: fallbackSpecDidNotRun,
			TopFrame:          topFrame,
			SampleMessage:     truncateRunes(firstLine, maxSampleRunes),
			Fallback:          fallbackSpecDidNotRun,
		}
		sig.Fingerprint = hashSignature(sig, includeTopFrame)
		return sig
	}

	if firstLine == "" {
		fb := fallbackForEmpty(in.Status, in.Framework)
		sig := Signature{
			AlgoVersion:       AlgoVersion,
			NormalizedMessage: fb,
			TopFrame:          topFrame,
			Fallback:          fb,
		}
		sig.Fingerprint = hashSignature(sig, includeTopFrame)
		return sig
	}

	errClass := ExtractErrorClass(firstLine)
	norm := NormalizeMessage(firstLine)

	sig := Signature{
		AlgoVersion:       AlgoVersion,
		ErrorClass:        errClass,
		NormalizedMessage: norm,
		TopFrame:          topFrame,
		SampleMessage:     truncateRunes(firstLine, maxSampleRunes),
	}
	sig.Fingerprint = hashSignature(sig, includeTopFrame)
	return sig
}

// NormalizeMessage strips ANSI codes, extracts the first meaningful line,
// normalizes dynamic tokens while keeping stable selectors, collapses
// whitespace, and truncates to 500 UTF-8 runes.
func NormalizeMessage(raw string) string {
	line := firstMeaningfulLine(raw, false)
	if line == "" {
		return ""
	}
	if strings.EqualFold(line, fallbackSpecDidNotRun) {
		return fallbackSpecDidNotRun
	}

	s := uuidRE.ReplaceAllString(line, phUUID)
	s = isoTSRE.ReplaceAllString(s, phTS)
	s = urlRE.ReplaceAllString(s, phURL)
	s = winTmpRE.ReplaceAllString(s, phTmp)
	s = unixTmpRE.ReplaceAllString(s, phTmp)
	s = ghWorkRE.ReplaceAllString(s, phRepo)
	s = winWorkRE.ReplaceAllString(s, phRepo)

	var selectors []string
	protect := func(match string) string {
		idx := len(selectors)
		selectors = append(selectors, match)
		return "\x00SEL" + strconv.Itoa(idx) + "\x00"
	}

	s = locatorCallRE.ReplaceAllStringFunc(s, protect)
	s = attrSelectorRE.ReplaceAllStringFunc(s, protect)
	s = dataAttrRE.ReplaceAllStringFunc(s, protect)
	s = nthSelectorRE.ReplaceAllStringFunc(s, protect)
	s = cssIdentTokenRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := cssIdentTokenRE.FindStringSubmatch(m)
		if len(parts) != 3 {
			return m
		}
		return parts[1] + protect(parts[2])
	})

	s = prefixedHex.ReplaceAllString(s, phHex)
	s = longHexRE.ReplaceAllString(s, phHex)
	s = mmIDRE.ReplaceAllStringFunc(s, func(tok string) string {
		if hasDigit(tok) {
			return phHex
		}
		return tok
	})
	s = durRE.ReplaceAllString(s, phDur)
	s = numRE.ReplaceAllString(s, phNum)

	for i, sel := range selectors {
		s = strings.ReplaceAll(s, "\x00SEL"+strconv.Itoa(i)+"\x00", sel)
	}

	s = strings.TrimSpace(wsRE.ReplaceAllString(s, " "))
	return truncateRunes(s, maxNormalizedRunes)
}

// ExtractErrorClass returns the leading error class name from a stripped
// first error line (e.g. "TimeoutError", "AssertionError"), or "".
func ExtractErrorClass(firstLine string) string {
	s := strings.TrimSpace(stripANSI(firstLine))
	if s == "" || strings.Contains(s, "://") && strings.Index(s, "://") == strings.Index(s, ":") {
		return ""
	}
	m := errClassRE.FindStringSubmatch(s)
	if len(m) != 2 {
		return ""
	}
	cls := m[1]
	// Require either an uppercase start (Error, TimeoutError) or dotted class (io.grpc.StatusRuntimeException).
	if !unicode.IsUpper(rune(cls[0])) && !strings.Contains(cls, ".") {
		return ""
	}
	return cls
}

// ExtractTopFrame finds the first non-runtime application stack frame from
// stack, falling back to msg lines after the first line (for Detox/Maestro).
func ExtractTopFrame(stack, msg string) string {
	if frame := scanLinesForFrame(stack, false); frame != "" {
		return frame
	}
	return scanLinesForFrame(msg, true)
}

func scanLinesForFrame(raw string, skipFirst bool) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(stripANSI(raw), "\r\n", "\n")
	lines := strings.Split(cleaned, "\n")
	start := 0
	if skipFirst {
		start = 1
	}
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || isRuntimeFrame(line) {
			continue
		}
		if isFrameLine(line) {
			return normalizeFrame(line)
		}
	}
	return ""
}

func isFrameLine(line string) bool {
	return strings.HasPrefix(line, "at ") || bareFrameRE.MatchString(line)
}

func isRuntimeFrame(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "at "))
	if strings.HasPrefix(trimmed, "internal/") || strings.HasPrefix(trimmed, `internal\`) {
		return true
	}
	for _, sub := range runtimeFrameSubstrings {
		if strings.Contains(line, sub) {
			return true
		}
	}
	return false
}

func normalizeFrame(line string) string {
	f := strings.TrimSpace(strings.TrimPrefix(line, "at "))
	f = winWorkRE.ReplaceAllString(f, phRepo)
	f = strings.ReplaceAll(f, `\`, "/")
	f = strings.TrimPrefix(f, "file://")
	f = cypressProxyRE.ReplaceAllString(f, "")
	f = webpackPrefixRE.ReplaceAllString(f, "")
	f = ghWorkRE.ReplaceAllString(f, phRepo)
	if idx := strings.Index(f, "/work/"); idx >= 0 {
		rest := f[idx+len("/work/"):]
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) == 3 && parts[0] == parts[1] && parts[2] != "" {
			start := strings.LastIndexAny(f[:idx], "(\"'") + 1
			if start == 0 && !strings.HasPrefix(f, "/") && (len(f) < 3 || f[1] != ':' || f[2] != '/') {
				if sp := strings.Index(f[:idx], " /"); sp >= 0 {
					start = sp + 1
				}
			}
			f = f[:start] + phRepo + parts[2]
		}
	}
	return truncateRunes(strings.TrimSpace(wsRE.ReplaceAllString(f, " ")), maxNormalizedRunes)
}

func fallbackForEmpty(status, framework string) string {
	normStatus := strings.TrimSpace(status)
	switch {
	case normStatus == teststatus.Interrupted || strings.EqualFold(normStatus, fallbackInterrupted):
		return fallbackInterrupted
	case normStatus == teststatus.TimedOut || strings.EqualFold(normStatus, "timedout") || strings.EqualFold(normStatus, fallbackTimeout):
		return fallbackTimeout
	default:
		fw := strings.ToLower(strings.TrimSpace(framework))
		if fw == "" {
			fw = unknownToken
		}
		return "no-error:" + unknownToken + ":" + fw
	}
}

func hashSignature(sig Signature, includeTopFrame bool) string {
	payload := strconv.Itoa(int(sig.AlgoVersion)) + "|" + sig.ErrorClass + "|" + sig.NormalizedMessage
	if includeTopFrame {
		payload += "|" + sig.TopFrame
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func stripANSI(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	s = ansiOSCRE.ReplaceAllString(s, "")
	s = ansiCSIRE.ReplaceAllString(s, "")
	s = ansiESCRE.ReplaceAllString(s, "")
	return s
}

func firstMeaningfulLine(raw string, skipFrames bool) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(stripANSI(raw), "\r\n", "\n")
	lines := strings.Split(cleaned, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || decorLineRE.MatchString(trimmed) {
			continue
		}
		if skipFrames && isFrameLine(trimmed) {
			continue
		}
		if isBareErrorHeader(trimmed) {
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || decorLineRE.MatchString(next) {
					continue
				}
				if isFrameLine(next) || isRuntimeFrame(next) {
					break
				}
				return strings.TrimSuffix(trimmed, ":") + ": " + next
			}
		}
		return trimmed
	}
	return ""
}

func isBareErrorHeader(trimmed string) bool {
	if strings.EqualFold(trimmed, "error") {
		return true
	}
	cls := ExtractErrorClass(trimmed)
	return cls != "" && strings.TrimSuffix(trimmed, ":") == cls
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func truncateRunes(s string, limit int) string {
	s = strings.ToValidUTF8(s, "")
	if limit <= 0 || len(s) <= limit {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
