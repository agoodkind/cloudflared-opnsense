package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoMetadataPaths(t *testing.T) {
	t.Parallel()

	t.Run("returns sorted regular files at repo root", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tempDir, "All"), 0o755); err != nil {
			t.Fatalf("mkdir All: %v", err)
		}
		for _, name := range []string{"packagesite.pkg", "meta.txz", "meta.conf"} {
			if err := os.WriteFile(filepath.Join(tempDir, name), []byte(name), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}

		got, err := repoMetadataPaths(tempDir)
		if err != nil {
			t.Fatalf("repoMetadataPaths: %v", err)
		}

		want := []string{
			filepath.Join(tempDir, "meta.conf"),
			filepath.Join(tempDir, "meta.txz"),
			filepath.Join(tempDir, "packagesite.pkg"),
		}
		if len(got) != len(want) {
			t.Fatalf("len(got) = %d, want %d: %#v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("missing directory returns error", func(t *testing.T) {
		t.Parallel()

		_, err := repoMetadataPaths(filepath.Join(t.TempDir(), "missing"))
		if err == nil {
			t.Fatal("repoMetadataPaths succeeded for a missing directory")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want ErrNotExist", err)
		}
	})
}
