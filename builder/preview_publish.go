package main

import (
	"path/filepath"
	"strconv"
)

// defaultPreviewPrefix is used when -preview-prefix is not supplied, so a local
// run still writes preview objects to an isolated location.
const defaultPreviewPrefix = "previews/local"

// previewPublish validates the publish path from a pull request. It uploads the
// freshly built packages and repository metadata to a throwaway key prefix,
// verifies each object is readable, then deletes them. It never creates a
// GitHub release or saves build state, so it is safe to run on every PR and it
// catches a broken publish (for example a missing or invalid R2 token) before
// the change merges.
func previewPublish(version string, revision int, cfg *config) error {
	pkgVersion := version + "_" + strconv.Itoa(revision)
	pkgFiles := []string{
		filepath.Join(pkgRepoDir, "All", "cloudflared-"+version+".pkg"),
		filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg"),
	}

	keyPrefix := cfg.previewPrefix
	if keyPrefix == "" {
		keyPrefix = defaultPreviewPrefix
	}
	return previewPublishToR2(pkgFiles, pkgRepoDir, keyPrefix)
}
