package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

func repoMetadataPaths(metadataDir string) ([]string, error) {
	entries, err := os.ReadDir(metadataDir)
	if err != nil {
		slog.Error("read repo metadata dir failed", "err", err, "path", metadataDir)
		return nil, fmt.Errorf("read repo metadata dir %s: %w", metadataDir, err)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			slog.Error("stat repo metadata entry failed", "err", err, "name", entry.Name())
			return nil, fmt.Errorf("stat repo metadata entry %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}

		paths = append(paths, filepath.Join(metadataDir, entry.Name()))
	}

	sort.Strings(paths)
	return paths, nil
}
