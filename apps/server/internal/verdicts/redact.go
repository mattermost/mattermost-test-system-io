// Package verdicts stores and queries AI triage verdicts with error_context
// redaction and failure_signatures merge-lineage lookup.
package verdicts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// MaxErrorContextBytes caps serialized error_context payloads accepted on write.
const MaxErrorContextBytes = 64 * 1024

const redactedPlaceholder = "[REDACTED]"

var (
	// ErrValidation indicates an invalid request payload or unknown reference.
	ErrValidation = errors.New("invalid verdict request")
	// ErrRepoMismatch indicates a referenced entity belongs to a different repository.
	ErrRepoMismatch = errors.New("repository mismatch")
	// ErrNotFound indicates no matching verdict was found.
	ErrNotFound = errors.New("verdict not found")
)

var (
	credURLRE    = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@"'` + "`" + `]+?:[^@\s"'` + "`" + `]+?)@`)
	urlQueryRE   = regexp.MustCompile(`(?i)\b((?:https?|wss?)://(?:[^\s"'<>()\]?#]|\[REDACTED\])+)\?[^\s"'<>)\]#]*`)
	bearerRE     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	ghTokenRE    = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,})`)
	slackTokenRE = regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)
	skTokenRE    = regexp.MustCompile(`\bsk-(?:ant-|proj-|svcacct-)?[A-Za-z0-9_-]{16,}`)
	awsKeyRE     = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	jwtRE        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	kvCredRE     = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:token|password|passwd|passphrase|secret|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|authorization|cookie|license))(\s*[=:]\s*)(?:Bearer\s+)?(?:"[^"]*"|'[^']*'|[^\s"',;&}\]]+)`)
	emailRE      = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
)

var sensitiveNormalizedKeys = map[string]bool{
	"authorization": true,
	"auth":          true,
	"bearer":        true,
	"cookie":        true,
	"cookies":       true,
	"setcookie":     true,
	"xapikey":       true,
	"xadminkey":     true,
	"apikey":        true,
	"accesskey":     true,
	"secretkey":     true,
	"privatekey":    true,
	"clientsecret":  true,
	"secret":        true,
	"secrets":       true,
	"password":      true,
	"passwd":        true,
	"passphrase":    true,
	"token":         true,
	"accesstoken":   true,
	"refreshtoken":  true,
	"idtoken":       true,
	"bearertoken":   true,
	"sessiontoken":  true,
	"csrftoken":     true,
	"credential":    true,
	"credentials":   true,
	"email":         true,
	"useremail":     true,
	"authoremail":   true,
	"license":       true,
	"licensekey":    true,
	"jwt":           true,
}

var sensitiveKeySegments = map[string]bool{
	"authorization": true,
	"password":      true,
	"passwd":        true,
	"passphrase":    true,
	"secret":        true,
	"secrets":       true,
	"token":         true,
	"cookie":        true,
	"cookies":       true,
	"credential":    true,
	"credentials":   true,
	"email":         true,
	"apikey":        true,
	"privatekey":    true,
	"accesskey":     true,
}

var sensitiveContainerKeys = map[string]bool{
	"credentials": true,
	"credential":  true,
	"cookies":     true,
	"secrets":     true,
	"authheaders": true,
}

// RedactErrorContext validates, sanitizes UTF-8/NUL bytes, and recursively redacts
// secrets, tokens, credentialed URLs, query strings, and email addresses from a
// JSON error_context payload before storage. Returns nil for empty or null input.
func RedactErrorContext(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(raw) > MaxErrorContextBytes {
		return nil, fmt.Errorf("%w: error_context exceeds %d bytes", ErrValidation, MaxErrorContextBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil, fmt.Errorf("%w: invalid error_context JSON", ErrValidation)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: invalid error_context JSON", ErrValidation)
	}
	if val == nil {
		return nil, nil
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: error_context must be a JSON object", ErrValidation)
	}

	redacted := redactValue(obj, false)
	out, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted error_context: %w", err)
	}
	return out, nil
}

// RedactString strips invalid UTF-8 / NUL bytes and replaces credentialed URLs,
// URL query strings, tokens, key=value secrets, and email addresses in s.
func RedactString(s string) string {
	s = sanitizeUTF8(s)
	s = credURLRE.ReplaceAllString(s, "${1}"+redactedPlaceholder+"@")
	s = urlQueryRE.ReplaceAllString(s, "${1}?"+redactedPlaceholder)
	s = bearerRE.ReplaceAllString(s, "${1} "+redactedPlaceholder)
	s = ghTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = slackTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = skTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = awsKeyRE.ReplaceAllString(s, redactedPlaceholder)
	s = jwtRE.ReplaceAllString(s, redactedPlaceholder)
	s = kvCredRE.ReplaceAllString(s, "${1}${2}"+redactedPlaceholder)
	s = emailRE.ReplaceAllString(s, redactedPlaceholder)
	return s
}

func redactValue(v any, forceRedact bool) any {
	switch x := v.(type) {
	case string:
		if forceRedact {
			return redactedPlaceholder
		}
		return RedactString(x)
	case []any:
		out := make([]any, len(x))
		for i, elem := range x {
			out[i] = redactValue(elem, forceRedact)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			cleanKey := sanitizeUTF8(k)
			if _, exists := out[cleanKey]; exists {
				continue
			}
			childForce := forceRedact || isSensitiveContainerKey(cleanKey)
			if !childForce && isSensitiveKey(cleanKey) {
				if _, isStr := x[k].(string); isStr {
					out[cleanKey] = redactedPlaceholder
					continue
				}
			}
			out[cleanKey] = redactValue(x[k], childForce)
		}
		return out
	default:
		return v
	}
}

func isSensitiveKey(key string) bool {
	joined, segs := splitKey(key)
	if sensitiveNormalizedKeys[joined] {
		return true
	}
	if len(segs) == 0 {
		return false
	}
	return sensitiveKeySegments[segs[len(segs)-1]]
}

func isSensitiveContainerKey(key string) bool {
	joined, _ := splitKey(key)
	return sensitiveContainerKeys[joined]
}

func splitKey(key string) (joined string, segments []string) {
	var buf strings.Builder
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			s := cur.String()
			segments = append(segments, s)
			buf.WriteString(s)
			cur.Reset()
		}
	}
	var prevLower bool
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			prevLower = false
			continue
		}
		if unicode.IsUpper(r) && prevLower {
			flush()
		}
		cur.WriteRune(unicode.ToLower(r))
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	flush()
	return buf.String(), segments
}

func sanitizeUTF8(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\x00", "")
}

func sanitizeUTF8Ptr(p *string) *string {
	if p == nil {
		return nil
	}
	s := sanitizeUTF8(*p)
	return &s
}
