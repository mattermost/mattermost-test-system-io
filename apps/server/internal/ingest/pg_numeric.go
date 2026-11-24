package ingest

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// PostgreSQL numeric limits: 131072 digits before the decimal point and a
// display scale (digits after it, trailing zeros included) of 16383.
const (
	pgNumericMaxIntDigits = 131072
	pgNumericMaxScale     = 16383
)

// canonicalPGNumber reports whether n fits PostgreSQL numeric and returns its
// canonical scientific spelling, so 1, 1.0 and 1e0 all become "1e0" while
// distinct values stay distinct. It runs in O(len(n)) without big-number math.
func canonicalPGNumber(n json.Number) (string, bool) {
	s, neg := strings.CutPrefix(string(n), "-")
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
			return "", false
		}
		var err error
		if exp, err = strconv.ParseInt(rest[1:], 10, 64); err != nil {
			return "", false
		}
	}
	if intPart == "" || exp > math.MaxInt32/2 || exp < -math.MaxInt32/2 ||
		int64(len(frac))-exp > pgNumericMaxScale {
		return "", false
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
		return "0", true
	}
	last := total - 1
	for digit(last) == '0' {
		last--
	}
	sigExp := int64(len(intPart)-1-first) + exp
	if sigExp+1 > pgNumericMaxIntDigits {
		return "", false
	}
	var b strings.Builder
	b.Grow(last - first + 24)
	if neg {
		b.WriteByte('-')
	}
	b.WriteByte(digit(first))
	if last > first {
		b.WriteByte('.')
		for k := first + 1; k <= last; k++ {
			b.WriteByte(digit(k))
		}
	}
	b.WriteByte('e')
	b.WriteString(strconv.FormatInt(sigExp, 10))
	return b.String(), true
}

// canonicalJSONValue returns a copy of v with every json.Number canonicalized
// by canonicalPGNumber, or false when any number would not fit numeric.
func canonicalJSONValue(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		c, ok := canonicalPGNumber(x)
		return json.Number(c), ok
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, ok := canonicalJSONValue(e)
			if !ok {
				return nil, false
			}
			out[i] = c
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			c, ok := canonicalJSONValue(e)
			if !ok {
				return nil, false
			}
			out[k] = c
		}
		return out, true
	default:
		return v, true
	}
}
