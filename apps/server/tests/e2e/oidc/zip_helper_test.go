//go:build e2e
// +build e2e

package oidce2e

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildZip produces an in-memory zip archive from (name→content) entries.
// Uses zip.Store (no compression) for reproducible sizes in size-cap tests.
func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	for name, content := range entries {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
