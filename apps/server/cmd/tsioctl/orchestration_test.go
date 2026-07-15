package main

import (
	"testing"
	"time"
)

func TestParseRetention(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"4w", 4 * 7 * 24 * time.Hour, false},
		{"720h", 720 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"0d", 0, false},
		{"", 0, true},
		{"d", 0, true},
		{"-30d", 0, true},
		{"30x", 0, true},
		{"30 days", 0, true},
	}
	for _, tc := range cases {
		got, err := parseRetention(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseRetention(%q) expected error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRetention(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseRetention(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
