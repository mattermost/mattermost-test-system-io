package ingest

import (
	"encoding/json"
	"testing"
)

func TestCanonicalPGNumber(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1", "1e0", true},
		{"1.0", "1e0", true},
		{"1e0", "1e0", true},
		{"10E-1", "1e0", true},
		{"0.00", "0", true},
		{"-0", "0", true},
		{"-12.50", "-1.25e1", true},
		{"0.0012", "1.2e-3", true},
		{"9007199254740993", "9.007199254740993e15", true},
		{"9007199254740992", "9.007199254740992e15", true},
		{"1e131071", "1e131071", true},
		{"1e-16383", "1e-16383", true},
		{"1e131072", "", false},
		{"1e200000", "", false},
		{"1e-16384", "", false},
		{"1e99999999999999999999", "", false},
		{"", "", false},
		{"1x", "", false},
	}
	for _, c := range cases {
		got, ok := canonicalPGNumber(json.Number(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("canonicalPGNumber(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
