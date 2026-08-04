package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
)

const patchManifestSchemaVersion = 1

type patchManifest struct {
	SchemaVersion int         `toml:"schema_version"`
	Patches       []patchSpec `toml:"patches"`
}

type patchSpec struct {
	ID        string          `toml:"id"`
	File      string          `toml:"file"`
	Git       *gitPatchSource `toml:"git"`
	Strip     int             `toml:"strip"`
	AppliesTo string          `toml:"applies_to"`
}

type gitPatchSource struct {
	Remote         string `toml:"remote"`
	Ref            string `toml:"ref"`
	ExpectedCommit string `toml:"expected_commit"`
}

type patchDisposition string

const (
	patchApplied        patchDisposition = "applied"
	patchAlreadyApplied patchDisposition = "already_applied"
	patchNotApplicable  patchDisposition = "not_applicable"
)

type patchManifestTOML struct {
	SchemaVersion int             `toml:"schema_version"`
	Patches       []patchSpecTOML `toml:"patches"`
}

type patchSpecTOML struct {
	ID        string          `toml:"id"`
	File      string          `toml:"file"`
	Git       *gitPatchSource `toml:"git"`
	Strip     *int            `toml:"strip"`
	AppliesTo string          `toml:"applies_to"`
}

func loadPatchManifest(path string) (patchManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		slog.Error("open patch manifest failed", "err", err, "path", path)
		return patchManifest{}, fmt.Errorf("open patch manifest %s: %w", path, err)
	}
	defer file.Close()

	decoded := patchManifestTOML{}
	decoder := toml.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		slog.Error("decode patch manifest failed", "err", err, "path", path)
		return patchManifest{}, fmt.Errorf("decode patch manifest %s: %w", path, err)
	}

	manifest := patchManifestFromTOML(decoded)

	if err := validatePatchManifest(manifest); err != nil {
		slog.Error("validate patch manifest failed", "err", err, "path", path)
		return patchManifest{}, err
	}
	return manifest, nil
}

func patchManifestFromTOML(decoded patchManifestTOML) patchManifest {
	manifest := patchManifest{
		SchemaVersion: decoded.SchemaVersion,
		Patches:       make([]patchSpec, 0, len(decoded.Patches)),
	}
	for _, decodedPatch := range decoded.Patches {
		strip := 1
		if decodedPatch.Strip != nil {
			strip = *decodedPatch.Strip
		}
		appliesTo := decodedPatch.AppliesTo
		if appliesTo == "" {
			appliesTo = "*"
		}
		manifest.Patches = append(manifest.Patches, patchSpec{
			ID:        decodedPatch.ID,
			File:      decodedPatch.File,
			Git:       decodedPatch.Git,
			Strip:     strip,
			AppliesTo: appliesTo,
		})
	}
	return manifest
}

func selectPatches(manifest patchManifest, upstreamVersion string) ([]patchSpec, error) {
	version, versionErr := semver.NewVersion(upstreamVersion)

	patches := make([]patchSpec, 0, len(manifest.Patches))
	for _, patch := range manifest.Patches {
		if patch.AppliesTo == "*" {
			patches = append(patches, patch)
			continue
		}
		if versionErr != nil {
			continue
		}
		constraint, err := semver.NewConstraint(patch.AppliesTo)
		if err != nil {
			slog.Error("parse patch version constraint failed", "err", err, "id", patch.ID)
			return nil, fmt.Errorf("parse patch %q applies_to %q: %w", patch.ID, patch.AppliesTo, err)
		}
		if constraint.Check(version) {
			patches = append(patches, patch)
		}
	}
	return patches, nil
}

func validatePatchManifest(manifest patchManifest) error {
	if manifest.SchemaVersion != patchManifestSchemaVersion {
		return fmt.Errorf(
			"patch manifest schema_version = %d, want %d",
			manifest.SchemaVersion,
			patchManifestSchemaVersion,
		)
	}

	ids := make(map[string]struct{}, len(manifest.Patches))
	for _, patch := range manifest.Patches {
		if patch.ID == "" {
			return fmt.Errorf("patch id must not be empty")
		}
		if _, exists := ids[patch.ID]; exists {
			return fmt.Errorf("patch id %q is duplicated", patch.ID)
		}
		ids[patch.ID] = struct{}{}

		if err := validatePatchSource(patch); err != nil {
			return err
		}
		if patch.Strip < 0 {
			return fmt.Errorf("patch %q strip must not be negative", patch.ID)
		}
		if _, err := semver.NewConstraint(patch.AppliesTo); err != nil {
			slog.Error("parse patch version constraint failed", "err", err, "id", patch.ID)
			return fmt.Errorf("parse patch %q applies_to %q: %w", patch.ID, patch.AppliesTo, err)
		}
	}
	return nil
}

func validatePatchSource(patch patchSpec) error {
	hasFile := patch.File != ""
	hasGit := patch.Git != nil
	if hasFile == hasGit {
		return fmt.Errorf("patch %q must declare exactly one source", patch.ID)
	}
	if hasGit {
		return fmt.Errorf("patch %q uses an unsupported git source", patch.ID)
	}
	if hasFile {
		if !safeRelativePath(patch.File) {
			return fmt.Errorf("patch %q file %q is not a safe relative path", patch.ID, patch.File)
		}
		return nil
	}
	return fmt.Errorf("patch %q has no supported source", patch.ID)
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || !filepath.IsLocal(path) {
		return false
	}
	return filepath.Clean(path) != "."
}

func applyPatchManifest(repoDir string, sourceDir string, upstreamVersion string) error {
	manifestPath := filepath.Join(repoDir, "builder", "patches.toml")
	manifest, err := loadPatchManifest(manifestPath)
	if err != nil {
		return err
	}

	patches, err := selectPatches(manifest, upstreamVersion)
	if err != nil {
		return err
	}

	for _, patch := range patches {
		if patch.Git != nil {
			return fmt.Errorf("patch %q uses an unsupported git source", patch.ID)
		}
		patchPath, err := resolveLocalPatchPath(manifestPath, patch)
		if err != nil {
			return err
		}
		disposition, err := applyPatchFile(sourceDir, patch, patchPath)
		if err != nil {
			return err
		}
		logf(fmt.Sprintf("patch %s: %s", patch.ID, disposition))
	}
	return nil
}

func resolveLocalPatchPath(manifestPath string, patch patchSpec) (string, error) {
	manifestDir, err := filepath.EvalSymlinks(filepath.Dir(manifestPath))
	if err != nil {
		slog.Error("resolve patch manifest directory failed", "err", err, "id", patch.ID)
		return "", fmt.Errorf("resolve patch %q manifest directory: %w", patch.ID, err)
	}
	configuredPath := filepath.Join(manifestDir, patch.File)
	patchPath, err := filepath.EvalSymlinks(configuredPath)
	if err != nil {
		slog.Error("resolve patch file failed", "err", err, "id", patch.ID, "path", configuredPath)
		return "", fmt.Errorf("resolve patch %q file %q: %w", patch.ID, patch.File, err)
	}
	relativePath, err := filepath.Rel(manifestDir, patchPath)
	if err != nil {
		slog.Error("resolve patch path failed", "err", err, "id", patch.ID)
		return "", fmt.Errorf("resolve patch %q path: %w", patch.ID, err)
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		err := fmt.Errorf("patch %q file %q escapes the manifest directory through a symlink", patch.ID, patch.File)
		slog.Error("resolve patch path rejected", "err", err, "id", patch.ID, "path", configuredPath)
		return "", err
	}
	return patchPath, nil
}

func applyPatchFile(sourceDir string, spec patchSpec, patchPath string) (patchDisposition, error) {
	strip := fmt.Sprintf("-p%d", spec.Strip)
	forwardOutput, forwardErr := runPatchGit(sourceDir, "apply", "--check", strip, "--", patchPath)
	if forwardErr == nil {
		applyOutput, applyErr := runPatchGit(sourceDir, "apply", "--index", strip, "--", patchPath)
		if applyErr != nil {
			slog.Error("apply patch failed", "err", applyErr, "id", spec.ID, "path", patchPath)
			return patchNotApplicable, fmt.Errorf("apply patch %q: %w\napply output:\n%s", spec.ID, applyErr, applyOutput)
		}
		return patchApplied, nil
	}

	reverseOutput, reverseErr := runPatchGit(sourceDir, "apply", "--reverse", "--check", strip, "--", patchPath)
	if reverseErr == nil {
		return patchAlreadyApplied, nil
	}

	return patchNotApplicable, fmt.Errorf(
		"patch %q is neither applicable nor already applied:\nforward check:\n%s\nreverse check:\n%s",
		spec.ID,
		forwardOutput,
		reverseOutput,
	)
}

func runPatchGit(sourceDir string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", sourceDir}, args...)
	command := exec.CommandContext(context.Background(), "git", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		commandError := fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		slog.Warn("patch git command failed", "err", commandError, "source_dir", sourceDir)
		return string(output), commandError
	}
	return string(output), nil
}
