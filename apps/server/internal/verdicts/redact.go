// Package verdicts stores and queries AI triage verdicts with error_context
// redaction and failure_signatures merge-lineage lookup.
package verdicts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
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
	ipv6HostRE      = `\[[0-9a-fA-F:.]+(?:%(?:[0-9A-Za-z._-]|%[0-9a-fA-F]{2})+)?\]`
	urlSchemeRE     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://`)
	urlQueryRE      = regexp.MustCompile(`(?i)\b((?:https?|wss?)://(?:[^\s"'<>()\[\]?#]|\[REDACTED\]|` + ipv6HostRE + `)+)\?[^\s"'<>)\]#]*`)
	pgURLQueryRE    = regexp.MustCompile(`(?i)\b(postgres(?:ql)?://(?:[^\s"'<>()\[\]?#,;]|\[REDACTED\]|` + ipv6HostRE + `)+)\?[^\s"<>()\]#,;]*`)
	bearerRE        = regexp.MustCompile(`(?i)\b(Bearer|Basic)[ \t]+([A-Za-z0-9._~+/=-]+)`)
	ghTokenRE       = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,})`)
	slackTokenRE    = regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)
	skTokenRE       = regexp.MustCompile(`\bsk-(?:ant-|proj-|svcacct-)?[A-Za-z0-9_-]{16,}`)
	awsKeyRE        = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	jwtRE           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	kvCredPrefixRE  = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:token|password|passwd|passphrase|secrets?|api[_-]?key|apikey|access[_-]?key|private[_-]?key|admin[_-]?key|adminkey|client[_-]?secret|authorization|cookies?|credentials?|auth[_-]?headers?|license))((?:\\*["'])?\s*[=:]\s*)`)
	jsonKeyPrefixRE = regexp.MustCompile(`(\\*["'])([A-Za-z0-9_.-]+)(\\*["']\s*:\s*)`)
	emailRE         = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
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
	"auth":          true,
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
	"secretkey":     true,
	"adminkey":      true,
	"licensekey":    true,
	"clientsecret":  true,
}

var sensitiveTwoSegmentSuffixes = map[string]bool{
	"apikey":       true,
	"accesskey":    true,
	"privatekey":   true,
	"secretkey":    true,
	"adminkey":     true,
	"licensekey":   true,
	"clientsecret": true,
}

var sensitiveContainerKeys = map[string]bool{
	"credentials": true,
	"credential":  true,
	"cookies":     true,
	"secrets":     true,
	"authheaders": true,
}

var metadataNumericKeys = map[string]bool{
	"expiresin":     true,
	"inputtokens":   true,
	"outputtokens":  true,
	"totaltokens":   true,
	"tokencount":    true,
	"secretversion": true,
	"secretcount":   true,
	"maxage":        true,
}

// RedactErrorContext validates, sanitizes UTF-8/NUL bytes, and recursively redacts
// secrets, tokens, credentialed URLs, query strings, and email addresses from a
// JSON error_context payload before storage. Returns nil for empty or null input.
func RedactErrorContext(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > MaxErrorContextBytes {
		return nil, fmt.Errorf("%w: error_context exceeds %d bytes", ErrValidation, MaxErrorContextBytes)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
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
	if err := validatePGNumericValues(redacted); err != nil {
		return nil, err
	}
	out, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted error_context: %w", err)
	}
	return out, nil
}

// RedactString normalizes invalid UTF-8 / NUL bytes to spaces and replaces
// credentialed URLs, URL query strings, tokens, key=value secrets, and email
// addresses in s.
func RedactString(s string) string {
	s = sanitizeUTF8ForRedaction(s)
	s = redactURLCredentials(s)
	s = urlQueryRE.ReplaceAllString(s, "${1}?"+redactedPlaceholder)
	s = pgURLQueryRE.ReplaceAllString(s, "${1}?"+redactedPlaceholder)
	s = ghTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = slackTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = skTokenRE.ReplaceAllString(s, redactedPlaceholder)
	s = awsKeyRE.ReplaceAllString(s, redactedPlaceholder)
	s = jwtRE.ReplaceAllString(s, redactedPlaceholder)
	s = redactKeyValues(s)
	s = redactBearerTokens(s)
	s = emailRE.ReplaceAllString(s, redactedPlaceholder)
	return s
}

func redactURLCredentials(s string) string {
	matches := urlSchemeRE.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last, searchFrom := 0, 0
	for _, loc := range matches {
		if loc[0] < searchFrom {
			continue
		}
		authStart := loc[1]
		lastAt := findURLUserinfoEnd(s, authStart)
		if lastAt <= authStart {
			continue
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:authStart])
		b.WriteString(redactedPlaceholder)
		last = lastAt
		searchFrom = lastAt + 1
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func findURLUserinfoEnd(s string, authStart int) int {
	lastAt := -1
	validAtThrough := -1
	for i := authStart; i < len(s); i++ {
		if s[i] == '@' {
			lastAt = i
			continue
		}
		if s[i] == ',' || s[i] == ';' {
			if i >= validAtThrough {
				atIdx, ok := separatorHasUserinfoAt(s, i)
				if !ok {
					break
				}
				validAtThrough = atIdx
			}
			continue
		}
		if isURLAuthorityStop(s[i]) {
			break
		}
	}
	return lastAt
}

func separatorHasUserinfoAt(s string, sepIdx int) (int, bool) {
	for i := sepIdx + 1; i < len(s); i++ {
		b := s[i]
		if b == '@' {
			return i, true
		}
		if b == '=' || isURLAuthorityStop(b) {
			return 0, false
		}
	}
	return 0, false
}

func isURLAuthorityStop(b byte) bool {
	return strings.IndexByte("/?# \t\n\r\f\v\"'`<>()[]{}", b) >= 0
}

func redactBearerTokens(s string) string {
	matches := bearerRE.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range matches {
		if isKVAssignmentToken(s, loc[0], loc[4], loc[5]) {
			continue
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:loc[4]])
		b.WriteString(redactedPlaceholder)
		last = loc[5]
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func isKVAssignmentToken(s string, matchStart, pos, end int) bool {
	tok := s[pos:end]
	quoteStart := end
	trimmed := tok
	if end < len(s) && (s[end] == '"' || s[end] == '\'') {
		trimmed = strings.TrimRight(tok, "\\")
		quoteStart = pos + len(trimmed)
	}
	if strings.Contains(strings.TrimRight(trimmed, "="), "=") {
		return true
	}
	if strings.HasSuffix(trimmed, "=") {
		if strings.HasPrefix(s[end:], redactedPlaceholder) {
			return true
		}
		if !strings.HasSuffix(trimmed, "==") && isOpeningQuoteAfterEquals(s, matchStart, quoteStart) {
			return true
		}
	}
	if strings.HasSuffix(trimmed, ":") && (end == len(s) || strings.IndexByte(" \t\r\n\"'", s[end]) >= 0) {
		return true
	}
	if end < len(s) && s[end] == ':' && (end+1 >= len(s) || strings.IndexByte(" \t\r\n\"'\\", s[end+1]) >= 0) {
		return true
	}
	return false
}

func isOpeningQuoteAfterEquals(s string, matchStart, quoteStart int) bool {
	qEnd, isQuoted, ok := scanQuotedCredValue(s, quoteStart)
	if !isQuoted || !ok {
		return false
	}
	nSlash := 0
	for quoteStart+nSlash < len(s) && s[quoteStart+nSlash] == '\\' {
		nSlash++
	}
	qIdx := quoteStart + nSlash
	quote := s[qIdx]
	if hasUnclosedLineQuote(s, matchStart, quote, nSlash) {
		return false
	}
	openContent := qIdx + 1
	if nSlash > 0 && openContent <= qEnd-1-nSlash && hasShallowerQuote(s[openContent:qEnd-1-nSlash], quote, nSlash) {
		return false
	}
	return openContent < qEnd && strings.IndexByte("\n\r\f\v,;}])", s[openContent]) < 0
}

func hasShallowerQuote(s string, quote byte, nSlash int) bool {
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			bs := 0
			for i+bs < len(s) && s[i+bs] == '\\' {
				bs++
			}
			i += bs
			if i < len(s) && s[i] == quote {
				if bs < nSlash || bs%2 == 0 {
					return true
				}
				i++
			}
			continue
		}
		if s[i] == quote {
			return true
		}
		i++
	}
	return false
}

func hasUnclosedLineQuote(s string, pos int, quote byte, nSlash int) bool {
	lineStart := strings.LastIndexAny(s[:pos], "\r\n") + 1
	step := 2 * (nSlash + 1)
	open := false
	for i := lineStart; i < pos; {
		if s[i] == '\\' {
			bs := 0
			for i+bs < pos && s[i+bs] == '\\' {
				bs++
			}
			i += bs
			if i < pos && s[i] == quote {
				if (!open && bs == nSlash) || (open && bs >= nSlash && (bs-nSlash)%step == 0) {
					open = !open
				}
				i++
			}
			continue
		}
		if s[i] == quote && nSlash == 0 {
			open = !open
		}
		i++
	}
	return open
}

func isPasswordKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "password") ||
		strings.HasSuffix(lower, "passwd") ||
		strings.HasSuffix(lower, "passphrase")
}

func isCookieKey(key string) bool {
	return strings.HasSuffix(strings.ToLower(key), "cookie")
}

type kvCredMatch struct {
	start     int
	prefixEnd int
	key       string
}

func collectKVCredMatches(s string) []kvCredMatch {
	kvLocs := kvCredPrefixRE.FindAllStringSubmatchIndex(s, -1)
	jsonLocs := jsonKeyPrefixRE.FindAllStringSubmatchIndex(s, -1)
	if len(kvLocs) == 0 && len(jsonLocs) == 0 {
		return nil
	}
	matches := make([]kvCredMatch, 0, len(kvLocs)+len(jsonLocs))
	for _, loc := range kvLocs {
		matches = append(matches, kvCredMatch{
			start:     loc[0],
			prefixEnd: loc[1],
			key:       s[loc[2]:loc[3]],
		})
	}
	for _, loc := range jsonLocs {
		openTok := s[loc[2]:loc[3]]
		key := s[loc[4]:loc[5]]
		closeTok := s[loc[6]:loc[7]]
		openSlash := len(openTok) - 1
		openQuote := openTok[openSlash]
		closeSlash := 0
		for closeSlash < len(closeTok) && closeTok[closeSlash] == '\\' {
			closeSlash++
		}
		if openSlash != closeSlash || openQuote != closeTok[closeSlash] {
			continue
		}
		if !isSensitiveKey(key) && !isSensitiveContainerKey(key) {
			continue
		}
		matches = append(matches, kvCredMatch{
			start:     loc[0],
			prefixEnd: loc[1],
			key:       key,
		})
	}
	slices.SortFunc(matches, func(a, b kvCredMatch) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.prefixEnd - b.prefixEnd
	})
	return matches
}

func redactKeyValues(s string) string {
	matches := collectKVCredMatches(s)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last, searchFrom := 0, 0
	for _, m := range matches {
		if m.start < searchFrom {
			continue
		}
		valEnd, ok := scanCredValue(s, m.prefixEnd, !isPasswordKey(m.key), isCookieKey(m.key))
		if !ok {
			searchFrom = m.prefixEnd
			continue
		}
		if b.Len() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:m.prefixEnd])
		b.WriteString(redactedPlaceholder)
		last = valEnd
		searchFrom = valEnd
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func scanCredValue(s string, start int, allowAuthScheme, allowSemicolon bool) (int, bool) {
	pos, schemeEnd := start, 0
	if allowAuthScheme {
		pos, schemeEnd = skipAuthSchemePrefix(s, start)
	}
	if pos < len(s) {
		if end, isQuoted, ok := scanQuotedCredValue(s, pos); isQuoted {
			if ok {
				return end, true
			}
			return scanUnclosedQuotedCredEnd(s, pos), true
		}
		if end, ok := scanJSONContainerValue(s, pos); ok {
			return end, true
		}
		if end, ok := scanUnquotedCredValue(s, pos, allowSemicolon); ok {
			if schemeEnd == 0 || !isKVAssignmentToken(s, start, pos, end) {
				return end, true
			}
		}
	}
	if schemeEnd > 0 {
		return schemeEnd, true
	}
	return 0, false
}

func scanJSONContainerValue(s string, pos int) (int, bool) {
	if pos >= len(s) || (s[pos] != '{' && s[pos] != '[') || strings.HasPrefix(s[pos:], redactedPlaceholder) {
		return 0, false
	}
	unclosedDouble := hasUnclosedLineQuote(s, pos, '"', 0)
	stack := []byte{s[pos]}
	for i := pos + 1; i < len(s); {
		if s[i] == '"' && unclosedDouble {
			return 0, false
		}
		if s[i] == '"' || s[i] == '\'' || s[i] == '\\' {
			if qEnd, isQuoted, ok := scanQuotedCredValue(s, i); isQuoted {
				if !ok {
					return 0, false
				}
				i = qEnd
				continue
			}
			i++
			continue
		}
		switch s[i] {
		case '{', '[':
			stack = append(stack, s[i])
		case '}':
			if stack[len(stack)-1] != '{' {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		case ']':
			if stack[len(stack)-1] != '[' {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
		i++
	}
	return 0, false
}

func skipAuthSchemePrefix(s string, pos int) (nextPos, schemeEnd int) {
	for _, scheme := range []string{"bearer", "basic"} {
		n := len(scheme)
		if len(s)-pos >= n+1 && strings.EqualFold(s[pos:pos+n], scheme) && (s[pos+n] == ' ' || s[pos+n] == '\t') {
			schemeEnd = pos + n
			pos += n + 1
			for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
				pos++
			}
			return pos, schemeEnd
		}
	}
	return pos, 0
}

func scanUnquotedCredValue(s string, pos int, allowSemicolon bool) (int, bool) {
	end := pos
	for end < len(s) {
		if strings.HasPrefix(s[end:], redactedPlaceholder) {
			end += len(redactedPlaceholder)
			continue
		}
		if allowSemicolon && end > pos && s[end-1] == '=' {
			if qEnd, isQuoted, ok := scanQuotedCredValue(s, end); isQuoted {
				if !ok {
					return scanUnclosedQuotedCredEnd(s, end), true
				}
				end = qEnd
				continue
			}
		}
		if allowSemicolon && s[end] == ';' {
			next := end + 1
			for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
				next++
			}
			if next < len(s) && strings.IndexByte(" \t\n\r\f\v\"',;&}]", s[next]) < 0 {
				end = next
				continue
			}
		}
		if strings.IndexByte(" \t\n\r\f\v\"',;&}]", s[end]) >= 0 {
			break
		}
		end++
	}
	if end == pos {
		return 0, false
	}
	switch s[pos:end] {
	case "true", "false", "null":
		return 0, false
	}
	return end, true
}

// scanQuotedCredValue matches a quoted value with arbitrary backslash-escaping depth
// (e.g., "...", \"...\", \\"...", \\\"...\\\"). At JSON nesting depth m, the delimiter
// uses D = 2^m - 1 backslashes and each literal backslash doubles to 2(D+1) backslashes,
// so a closing quote has k >= D backslashes with (k - D) % (2*(D+1)) == 0.
func scanQuotedCredValue(s string, start int) (end int, isQuoted, ok bool) {
	nSlash := 0
	for start+nSlash < len(s) && s[start+nSlash] == '\\' {
		nSlash++
	}
	qIdx := start + nSlash
	if qIdx >= len(s) || (s[qIdx] != '"' && s[qIdx] != '\'') {
		return 0, false, false
	}
	quote := s[qIdx]
	step := 2 * (nSlash + 1)
	for i := qIdx + 1; i < len(s); {
		if s[i] == '\\' {
			bs := 0
			for i+bs < len(s) && s[i+bs] == '\\' {
				bs++
			}
			i += bs
			if i < len(s) && s[i] == quote {
				if bs >= nSlash && (bs-nSlash)%step == 0 {
					return i + 1, true, true
				}
				i++
			}
			continue
		}
		if s[i] == quote && nSlash == 0 {
			return i + 1, true, true
		}
		i++
	}
	return 0, true, false
}

func scanUnclosedQuotedCredEnd(s string, pos int) int {
	nSlash := 0
	for pos+nSlash < len(s) && s[pos+nSlash] == '\\' {
		nSlash++
	}
	qIdx := pos + nSlash
	quote := s[qIdx]
	for i := qIdx + 1; i < len(s); {
		if s[i] == '\r' || s[i] == '\n' {
			return i
		}
		if nSlash > 0 && s[i] == '\\' {
			bs := 0
			for i+bs < len(s) && s[i+bs] == '\\' {
				bs++
			}
			if i+bs < len(s) && s[i+bs] == quote && (bs < nSlash || bs%2 == 0) {
				if bs%2 == 1 {
					return i + bs - 1
				}
				return i + bs
			}
			i += bs
			continue
		}
		if nSlash > 0 && s[i] == quote {
			return i
		}
		i++
	}
	return len(s)
}

func redactValue(v any, forceRedact bool) any {
	switch x := v.(type) {
	case string:
		if forceRedact {
			return redactedPlaceholder
		}
		return RedactString(x)
	case json.Number:
		if forceRedact {
			return redactedPlaceholder
		}
		return x
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
			redactedKey := RedactString(cleanKey)
			if _, exists := out[redactedKey]; exists {
				continue
			}
			if num, isNum := x[k].(json.Number); isNum {
				if !isMetadataNumericKey(cleanKey) && (forceRedact || isSensitiveKey(cleanKey)) {
					out[redactedKey] = redactedPlaceholder
				} else {
					out[redactedKey] = num
				}
				continue
			}
			childForce := forceRedact || isSensitiveContainerKey(cleanKey) || isSensitiveKey(cleanKey)
			out[redactedKey] = redactValue(x[k], childForce)
		}
		return out
	default:
		return v
	}
}

func isMetadataNumericKey(key string) bool {
	joined, _ := splitKey(key)
	return metadataNumericKeys[joined]
}

func isSensitiveKey(key string) bool {
	joined, segs := splitKey(key)
	if sensitiveNormalizedKeys[joined] {
		return true
	}
	n := len(segs)
	if n == 0 {
		return false
	}
	if sensitiveKeySegments[segs[n-1]] {
		return true
	}
	if n >= 2 && sensitiveTwoSegmentSuffixes[segs[n-2]+segs[n-1]] {
		return true
	}
	return false
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

func sanitizeUTF8ForRedaction(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, " "), "\x00", " ")
}

func sanitizeUTF8Ptr(p *string) *string {
	if p == nil {
		return nil
	}
	s := sanitizeUTF8(*p)
	return &s
}

const (
	pgNumericMaxIntDigits = 131072
	pgNumericMaxScale     = 16383
)

func validatePGNumericValues(v any) error {
	switch x := v.(type) {
	case json.Number:
		if !fitsPGNumeric(x) {
			return fmt.Errorf("%w: error_context numeric value out of range", ErrValidation)
		}
	case []any:
		for _, elem := range x {
			if err := validatePGNumericValues(elem); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, elem := range x {
			if err := validatePGNumericValues(elem); err != nil {
				return err
			}
		}
	}
	return nil
}

func fitsPGNumeric(n json.Number) bool {
	s, _ := strings.CutPrefix(string(n), "-")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intPart, rest := s[:i], s[i:]
	frac := ""
	if strings.HasPrefix(rest, ".") {
		j := 1
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		frac, rest = rest[1:j], rest[j:]
	}
	var exp int64
	if rest != "" {
		if rest[0] != 'e' && rest[0] != 'E' {
			return false
		}
		var err error
		if exp, err = strconv.ParseInt(rest[1:], 10, 64); err != nil {
			return false
		}
	}
	if intPart == "" || exp > math.MaxInt32/2 || exp < -math.MaxInt32/2 || int64(len(frac))-exp > pgNumericMaxScale {
		return false
	}
	digit := func(k int) byte {
		if k < len(intPart) {
			return intPart[k]
		}
		return frac[k-len(intPart)]
	}
	total := len(intPart) + len(frac)
	first := 0
	for first < total && digit(first) == '0' {
		first++
	}
	if first == total {
		return true
	}
	sigExp := int64(len(intPart)-1-first) + exp
	return sigExp+1 <= pgNumericMaxIntDigits
}
