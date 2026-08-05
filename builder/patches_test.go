package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPatchManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name: "rejects unknown fields",
			manifest: `schema_version = 1
unknown = true
`,
			wantErr: "unknown",
		},
		{
			name: "requires schema version one",
			manifest: `schema_version = 2
`,
			wantErr: "schema_version",
		},
		{
			name: "requires non-empty unique ids",
			manifest: `schema_version = 1

[[patches]]
id = ""
file = "patches/one.patch"

[[patches]]
id = ""
file = "patches/two.patch"
`,
			wantErr: "id",
		},
		{
			name: "requires exactly one source",
			manifest: `schema_version = 1

[[patches]]
id = "no-source"

[[patches]]
id = "two-sources"
file = "patches/two.patch"
[patches.git]
remote = "https://github.com/cloudflare/cloudflared.git"
ref = "refs/pull/1707/head"
expected_commit = "834e9d1706d8bf53b83e66af64f4e9856321c2ff"
`,
			wantErr: "exactly one source",
		},
		{
			name: "rejects unsafe file paths",
			manifest: `schema_version = 1

[[patches]]
id = "escape"
file = "../outside.patch"
`,
			wantErr: "safe relative",
		},
		{
			name: "rejects negative strip",
			manifest: `schema_version = 1

[[patches]]
id = "negative-strip"
file = "patches/one.patch"
strip = -1
`,
			wantErr: "strip",
		},
		{
			name: "rejects invalid semantic version constraint",
			manifest: `schema_version = 1

[[patches]]
id = "invalid-constraint"
file = "patches/one.patch"
applies_to = "not a constraint"
`,
			wantErr: "applies_to",
		},
		{
			name: "rejects git sources before resolver support",
			manifest: `schema_version = 1

[[patches]]
id = "upstream"
[patches.git]
remote = "https://github.com/cloudflare/cloudflared.git"
ref = "refs/pull/1707/head"
expected_commit = "834e9d1706d8bf53b83e66af64f4e9856321c2ff"
`,
			wantErr: "unsupported git source",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writePatchManifest(t, test.manifest)
			_, err := loadPatchManifest(path)
			if err == nil {
				t.Fatal("loadPatchManifest() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("loadPatchManifest() error = %q, want %q", err, test.wantErr)
			}
		})
	}
}

func TestSelectPatches(t *testing.T) {
	t.Parallel()

	manifest := `schema_version = 1

[[patches]]
id = "all-versions"
file = "patches/all.patch"

[[patches]]
id = "matching-range"
file = "patches/range.patch"
strip = 0
applies_to = ">=2026.7.0 <2026.8.0"

[[patches]]
id = "other-range"
file = "patches/other.patch"
applies_to = ">=2026.8.0"
`
	parsed, err := loadPatchManifest(writePatchManifest(t, manifest))
	if err != nil {
		t.Fatalf("loadPatchManifest() error = %v", err)
	}

	patches, err := selectPatches(parsed, "2026.7.3")
	if err != nil {
		t.Fatalf("selectPatches() error = %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("selectPatches() returned %d patches, want 2", len(patches))
	}
	if patches[0].ID != "all-versions" || patches[1].ID != "matching-range" {
		t.Fatalf("selectPatches() ids = %q, %q", patches[0].ID, patches[1].ID)
	}
	if patches[0].Strip != 1 {
		t.Fatalf("default strip = %d, want 1", patches[0].Strip)
	}
	if patches[0].AppliesTo != "*" {
		t.Fatalf("default applies_to = %q, want *", patches[0].AppliesTo)
	}
	if patches[1].Strip != 0 {
		t.Fatalf("strip = %d, want 0", patches[1].Strip)
	}
}

func TestSelectPatchesUsesOnlyVersionAgnosticPatchesForBranchRef(t *testing.T) {
	t.Parallel()

	manifest, err := loadPatchManifest(writePatchManifest(t, `schema_version = 1

[[patches]]
id = "all-versions"
file = "patches/all.patch"

[[patches]]
id = "released-versions"
file = "patches/released.patch"
applies_to = ">=2026.7.3"
`))
	if err != nil {
		t.Fatalf("loadPatchManifest() error = %v", err)
	}

	patches, err := selectPatches(manifest, "main")
	if err != nil {
		t.Fatalf("selectPatches() error = %v", err)
	}
	if len(patches) != 1 || patches[0].ID != "all-versions" {
		t.Fatalf("selectPatches() = %#v, want all-versions only", patches)
	}
}

func writePatchManifest(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "patches.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write patch manifest: %v", err)
	}
	return path
}

func TestApplyPatchFileAppliesAndSkipsExactReverse(t *testing.T) {
	t.Parallel()

	sourceDir := newPatchCheckout(t, "first\n")
	patchPath := writeGitPatch(t, sourceDir, "second\n")
	spec := patchSpec{ID: "rename-message", Strip: 1}

	disposition, err := applyPatchFile(sourceDir, spec, patchPath)
	if err != nil {
		t.Fatalf("applyPatchFile() error = %v", err)
	}
	if disposition != patchApplied {
		t.Fatalf("applyPatchFile() disposition = %q, want %q", disposition, patchApplied)
	}
	if got := readPatchFile(t, filepath.Join(sourceDir, "message.txt")); got != "second\n" {
		t.Fatalf("message.txt = %q, want %q", got, "second\n")
	}

	disposition, err = applyPatchFile(sourceDir, spec, patchPath)
	if err != nil {
		t.Fatalf("applyPatchFile() second error = %v", err)
	}
	if disposition != patchAlreadyApplied {
		t.Fatalf("applyPatchFile() second disposition = %q, want %q", disposition, patchAlreadyApplied)
	}
}

func TestApplyPatchFileHonorsStripZero(t *testing.T) {
	t.Parallel()

	sourceDir := newPatchCheckout(t, "first\n")
	patchPath := writeGitPatch(t, sourceDir, "second\n")
	patch := readPatchFile(t, patchPath)
	patch = strings.ReplaceAll(patch, "a/message.txt", "message.txt")
	patch = strings.ReplaceAll(patch, "b/message.txt", "message.txt")
	if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
		t.Fatalf("rewrite strip-zero patch: %v", err)
	}

	disposition, err := applyPatchFile(sourceDir, patchSpec{ID: "strip-zero", Strip: 0}, patchPath)
	if err != nil {
		t.Fatalf("applyPatchFile() error = %v", err)
	}
	if disposition != patchApplied {
		t.Fatalf("applyPatchFile() disposition = %q, want %q", disposition, patchApplied)
	}
	if got := readPatchFile(t, filepath.Join(sourceDir, "message.txt")); got != "second\n" {
		t.Fatalf("message.txt = %q, want %q", got, "second\n")
	}
}

func TestApplyPatchFileHandlesLeadingDashPath(t *testing.T) {
	t.Parallel()

	sourceDir := newPatchCheckout(t, "first\n")
	writeGitPatchAt(t, sourceDir, filepath.Join(sourceDir, "-change.patch"), "second\n")

	disposition, err := applyPatchFile(sourceDir, patchSpec{ID: "leading-dash", Strip: 1}, "-change.patch")
	if err != nil {
		t.Fatalf("applyPatchFile() error = %v", err)
	}
	if disposition != patchApplied {
		t.Fatalf("applyPatchFile() disposition = %q, want %q", disposition, patchApplied)
	}
}

func TestApplyPatchFileReportsForwardAndReverseDiagnosticsForDrift(t *testing.T) {
	t.Parallel()

	sourceDir := newPatchCheckout(t, "drifted\n")
	patchDir := t.TempDir()
	patchPath := filepath.Join(patchDir, "rename-message.patch")
	patch := "--- a/message.txt\n+++ b/message.txt\n@@ -1 +1 @@\n-first\n+second\n"
	if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
		t.Fatalf("write patch: %v", err)
	}

	disposition, err := applyPatchFile(sourceDir, patchSpec{ID: "rename-message", Strip: 1}, patchPath)
	if err == nil {
		t.Fatal("applyPatchFile() error = nil")
	}
	if disposition != patchNotApplicable {
		t.Fatalf("applyPatchFile() disposition = %q, want %q", disposition, patchNotApplicable)
	}
	for _, want := range []string{
		`patch "rename-message"`,
		"forward check:",
		"reverse check:",
		"patch failed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("applyPatchFile() error = %q, want %q", err, want)
		}
	}
}

func TestApplyPatchManifestAppliesLocalPatchesInDeclarationOrder(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	sourceDir := newPatchCheckout(t, "first\n")
	patchDir := filepath.Join(repoDir, "builder", "patches")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatalf("create patch dir: %v", err)
	}

	firstPatch := filepath.Join(patchDir, "first.patch")
	writeGitPatchAt(t, sourceDir, firstPatch, "second\n")
	runGit(t, sourceDir, "apply", "--index", firstPatch)
	secondPatch := filepath.Join(patchDir, "second.patch")
	writeGitPatchAt(t, sourceDir, secondPatch, "third\n")
	runGit(t, sourceDir, "reset", "--hard", "HEAD")

	writeManifestAt(t, repoDir, `schema_version = 1

[[patches]]
id = "first"
file = "patches/first.patch"

[[patches]]
id = "second"
file = "patches/second.patch"
`)

	if err := applyPatchManifest(repoDir, sourceDir, "2026.7.3"); err != nil {
		t.Fatalf("applyPatchManifest() error = %v", err)
	}
	if got := readPatchFile(t, filepath.Join(sourceDir, "message.txt")); got != "third\n" {
		t.Fatalf("message.txt = %q, want %q", got, "third\n")
	}
}

func TestApplyPatchManifestRejectsPatchSymlinkEscape(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	sourceDir := newPatchCheckout(t, "first\n")
	patchDir := filepath.Join(repoDir, "builder", "patches")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatalf("create patch dir: %v", err)
	}

	targetPatch := writeGitPatch(t, sourceDir, "second\n")
	escapingPatch := filepath.Join(patchDir, "escape.patch")
	if err := os.Symlink(targetPatch, escapingPatch); err != nil {
		t.Fatalf("create patch symlink: %v", err)
	}
	writeManifestAt(t, repoDir, `schema_version = 1

[[patches]]
id = "escape"
file = "patches/escape.patch"
`)

	err := applyPatchManifest(repoDir, sourceDir, "2026.7.3")
	if err == nil {
		t.Fatal("applyPatchManifest() error = nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("applyPatchManifest() error = %q, want symlink", err)
	}
}

func TestLocalTokenPatchCompilesFreeBSDFixtureAndSkipsWhenAlreadyApplied(t *testing.T) {
	t.Parallel()

	sourceDir := createTokenFileCheckout(t)
	repoDir := t.TempDir()
	patchDir := filepath.Join(repoDir, "builder", "patches")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatalf("create patch directory: %v", err)
	}
	patchData, err := os.ReadFile(filepath.Join("patches", "freebsd-token-file.patch"))
	if err != nil {
		t.Fatalf("read token patch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "freebsd-token-file.patch"), patchData, 0o600); err != nil {
		t.Fatalf("write token patch: %v", err)
	}
	writeManifestAt(t, repoDir, `schema_version = 1

[[patches]]
id = "freebsd-token-file"
file = "patches/freebsd-token-file.patch"
`)

	if err := applyPatchManifest(repoDir, sourceDir, "2026.7.3"); err != nil {
		t.Fatalf("applyPatchManifest() first run: %v", err)
	}
	if err := applyPatchManifest(repoDir, sourceDir, "2026.7.3"); err != nil {
		t.Fatalf("applyPatchManifest() second run: %v", err)
	}

	testBinary := filepath.Join(t.TempDir(), "cloudflared.test")
	command := exec.CommandContext(t.Context(), "go", "test", "-c", "-o", testBinary, "./cmd/cloudflared")
	command.Dir = sourceDir
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOARCH=amd64", "GOOS=freebsd")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile token fixture for FreeBSD: %v\n%s", err, output)
	}
	if _, err := os.Stat(testBinary); err != nil {
		t.Fatalf("stat compiled FreeBSD test binary: %v", err)
	}
}

func createTokenFileCheckout(t *testing.T) string {
	t.Helper()

	sourceDir := t.TempDir()
	files := map[string]string{
		"go.mod": "module github.com/cloudflare/cloudflared\n\ngo 1.26.5\n",
		filepath.Join("cmd", "cloudflared", "common_service.go"): `package main

func createTokenFileUnix(string) error { return nil }
func writeTokenToFile(path string) error { return createTokenFile(path) }
`,
		filepath.Join("cmd", "cloudflared", "generic_service.go"): `//go:build !windows && !darwin && !linux

package main

func main() { _ = writeTokenToFile("token") }
`,
		filepath.Join("cmd", "cloudflared", "main_test.go"): `package main

import "testing"

func TestFixture(*testing.T) {}
`,
	}
	for path, contents := range files {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o600); err != nil {
			t.Fatalf("write fixture file: %v", err)
		}
	}
	runGit(t, sourceDir, "init", "--initial-branch=patch-tests")
	runGit(t, sourceDir, "config", "user.email", "alex@goodkind.io")
	runGit(t, sourceDir, "config", "user.name", "Alexander Goodkind")
	runGit(t, sourceDir, "add", ".")
	runGit(t, sourceDir, "commit", "-m", "add token fixture")
	return sourceDir
}

func TestFindRepoDirSkipsNestedBuilderModule(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	builderDir := filepath.Join(repoDir, "builder")
	if err := os.MkdirAll(builderDir, 0o755); err != nil {
		t.Fatalf("create builder directory: %v", err)
	}
	for _, path := range []string{filepath.Join(repoDir, "go.mod"), filepath.Join(builderDir, "go.mod")} {
		if err := os.WriteFile(path, []byte("module fixture\n"), 0o600); err != nil {
			t.Fatalf("write module marker: %v", err)
		}
	}

	if got := findRepoDir(builderDir); got != repoDir {
		t.Fatalf("findRepoDir() = %q, want %q", got, repoDir)
	}
}

func newPatchCheckout(t *testing.T, contents string) string {
	t.Helper()

	sourceDir := t.TempDir()
	runGit(t, sourceDir, "init", "--initial-branch=patch-tests")
	runGit(t, sourceDir, "config", "user.email", "alex@goodkind.io")
	runGit(t, sourceDir, "config", "user.name", "Alexander Goodkind")
	if err := os.WriteFile(filepath.Join(sourceDir, "message.txt"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write message: %v", err)
	}
	runGit(t, sourceDir, "add", "message.txt")
	runGit(t, sourceDir, "commit", "-m", "add message")
	return sourceDir
}

func writeGitPatch(t *testing.T, sourceDir string, contents string) string {
	t.Helper()

	return writeGitPatchAt(t, sourceDir, filepath.Join(t.TempDir(), "change.patch"), contents)
}

func writeGitPatchAt(t *testing.T, sourceDir string, patchPath string, contents string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(sourceDir, "message.txt"), []byte(contents), 0o600); err != nil {
		t.Fatalf("update message: %v", err)
	}
	patch := runGit(t, sourceDir, "diff", "--binary", "--", "message.txt")
	if err := os.WriteFile(patchPath, []byte(patch), 0o600); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	runGit(t, sourceDir, "checkout", "--", "message.txt")
	return patchPath
}

func writeManifestAt(t *testing.T, repoDir string, content string) {
	t.Helper()

	manifestPath := filepath.Join(repoDir, "builder", "patches.toml")
	if err := os.WriteFile(manifestPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func readPatchFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func runGit(t *testing.T, sourceDir string, args ...string) string {
	t.Helper()

	commandArgs := []string{
		"-C",
		sourceDir,
		"-c",
		"commit.gpgsign=false",
		"-c",
		"tag.gpgsign=false",
	}
	command := exec.Command("git", append(commandArgs, args...)...)
	command.Env = append(
		os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
