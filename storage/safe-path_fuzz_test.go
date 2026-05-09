package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzToSafeFilePath(f *testing.F) {
	for _, seed := range [][2]string{
		{"a", "b"},
		{"a", filepath.FromSlash("b/../../..")},
		{filepath.FromSlash("/tmp/escape"), ""},
		{"", ".."},
		{"..", "file"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, first, second string) {
		got, err := ToSafeFilePath(first, second)
		if err != nil {
			return
		}
		if filepath.IsAbs(got) {
			t.Fatalf("safe path is absolute: %q", got)
		}
		cleaned := filepath.Clean(got)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			t.Fatalf("safe path escapes root: %q", got)
		}
	})
}
