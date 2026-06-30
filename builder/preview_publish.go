package main

import (
	"fmt"
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
// the change merges. The production decision is logged for context but does not
// gate the round-trip, so a publish-path regression is caught even when the
// built content matches the latest release.
func previewPublish(version string, revision int, cfg *config, decision publishDecision) error {
	logf(fmt.Sprintf(
		"preview publish: production decision would be %v (%s)",
		decision.shouldPublish,
		decision.reason,
	))

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
