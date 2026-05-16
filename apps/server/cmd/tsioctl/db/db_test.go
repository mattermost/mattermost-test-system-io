package db

import "testing"

func TestIsLocalDatabaseTarget(t *testing.T) {
	cases := []struct {
		dsn  string
		want bool
	}{
		{"postgres://tsio:tsio@localhost:6432/tsio?sslmode=disable", true},
		{"postgres://tsio:tsio@127.0.0.1:5432/tsio", true},
		{"postgres://tsio:tsio@[::1]:5432/tsio", true},
		{"postgres://tsio@postgres.local:5432/tsio", true},
		// RFC1918
		{"postgres://tsio@10.0.0.5:5432/tsio", true},
		{"postgres://tsio@192.168.1.10:5432/tsio", true},
		{"postgres://tsio@172.16.0.1:5432/tsio", true},
		// public-IP / DNS — must be rejected
		{"postgres://tsio@db.prod.example.com:5432/tsio", false},
		{"postgres://tsio@52.10.20.30:5432/tsio", false},
		// malformed / empty — fail closed
		{"", false},
		{"not-a-dsn", false},
		{"postgres://tsio@/tsio", false},
	}
	for _, tc := range cases {
		t.Run(tc.dsn, func(t *testing.T) {
			got := isLocalDatabaseTarget(tc.dsn)
			if got != tc.want {
				t.Errorf("isLocalDatabaseTarget(%q) = %v, want %v", tc.dsn, got, tc.want)
			}
		})
	}
}
