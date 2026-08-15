package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCloudflaredLoadsDeclaredPatchManifest(t *testing.T) {
	upstreamDir := newPatchCheckout(t, "fixture\n")
	runGit(t, upstreamDir, "tag", "2026.7.3")
	remoteDir := filepath.Join(t.TempDir(), "cloudflared.git")
	runGit(t, filepath.Dir(remoteDir), "init", "--bare", "--initial-branch=main", remoteDir)
	runGit(t, upstreamDir, "remote", "add", "origin", remoteDir)
	runGit(t, upstreamDir, "push", "origin", "patch-tests:refs/heads/main", "refs/tags/2026.7.3")

	gitConfigPath := filepath.Join(t.TempDir(), "gitconfig")
	gitConfig := fmt.Sprintf(
		"[url %q]\n\tinsteadOf = https://github.com/cloudflare/cloudflared.git\n",
		"file://"+remoteDir,
	)
	if err := os.WriteFile(gitConfigPath, []byte(gitConfig), 0o600); err != nil {
		t.Fatalf("write Git configuration: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfigPath)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")

	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "builder"), 0o755); err != nil {
		t.Fatalf("create builder directory: %v", err)
	}
	writeManifestAt(t, repoDir, "schema_version = 2\n")

	previousWorkDir := workDir
	workDir = t.TempDir()
	t.Cleanup(func() {
		workDir = previousWorkDir
	})

	expectedCommit := strings.TrimSpace(runGit(t, upstreamDir, "rev-parse", "HEAD"))
	err := buildCloudflared("2026.7.3", expectedCommit, repoDir)
	if err == nil {
		t.Fatal("buildCloudflared() error = nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("buildCloudflared() error = %q, want schema_version", err)
	}
}

func TestBuildCloudflaredRejectsMovedTagBeforePatching(t *testing.T) {
	upstreamDir := newPatchCheckout(t, "first\n")
	expectedCommit := strings.TrimSpace(runGit(t, upstreamDir, "rev-parse", "HEAD"))
	runGit(t, upstreamDir, "tag", "2026.8.1")

	fixturePath := filepath.Join(upstreamDir, "fixture.txt")
	if err := os.WriteFile(fixturePath, []byte("moved\n"), 0o600); err != nil {
		t.Fatalf("move tag fixture: %v", err)
	}
	runGit(t, upstreamDir, "add", "fixture.txt")
	runGit(t, upstreamDir, "commit", "-m", "move release tag")
	runGit(t, upstreamDir, "tag", "-f", "2026.8.1")

	remoteDir := filepath.Join(t.TempDir(), "cloudflared.git")
	runGit(t, filepath.Dir(remoteDir), "init", "--bare", "--initial-branch=main", remoteDir)
	runGit(t, upstreamDir, "remote", "add", "origin", remoteDir)
	runGit(t, upstreamDir, "push", "origin", "patch-tests:refs/heads/main", "refs/tags/2026.8.1")

	gitConfigPath := filepath.Join(t.TempDir(), "gitconfig")
	gitConfig := fmt.Sprintf(
		"[url %q]\n\tinsteadOf = https://github.com/cloudflare/cloudflared.git\n",
		"file://"+remoteDir,
	)
	if err := os.WriteFile(gitConfigPath, []byte(gitConfig), 0o600); err != nil {
		t.Fatalf("write Git configuration: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfigPath)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")

	previousWorkDir := workDir
	workDir = t.TempDir()
	t.Cleanup(func() { workDir = previousWorkDir })

	err := buildCloudflared("2026.8.1", expectedCommit, t.TempDir())
	if err == nil {
		t.Fatal("buildCloudflared() error = nil")
	}
	if !strings.Contains(err.Error(), "cloned cloudflared tag") {
		t.Fatalf("buildCloudflared() error = %q, want moved tag rejection", err)
	}
}

func TestPatchManifestHonorsFreeBSDDiagnosticsVersionBoundary(t *testing.T) {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	manifest, err := loadPatchManifest(filepath.Join(projectRoot, "builder", "patches.toml"))
	if err != nil {
		t.Fatalf("loadPatchManifest() error = %v", err)
	}

	beforeBoundary, err := selectPatches(manifest, "2024.11.1")
	if err != nil {
		t.Fatalf("selectPatches() before boundary error = %v", err)
	}
	if len(beforeBoundary) != 0 {
		t.Fatalf("selectPatches() before boundary returned %d patches, want 0", len(beforeBoundary))
	}

	afterBoundary, err := selectPatches(manifest, "2024.11.2")
	if err != nil {
		t.Fatalf("selectPatches() after boundary error = %v", err)
	}
	if len(afterBoundary) != 1 || afterBoundary[0].ID != "freebsd-diagnostics" {
		t.Fatalf("selectPatches() after boundary = %v, want freebsd-diagnostics", afterBoundary)
	}
}

func TestFreeBSDDiagnosticsPatchRunsAndSkipsWhenAlreadyApplied(t *testing.T) {
	sourceDir := createCloudflaredDiagnosticsFixture(t)
	repoDir := createFreeBSDDiagnosticsPatchFixture(t)

	if err := applyBuildPatches(repoDir, sourceDir, "2026.7.3"); err != nil {
		t.Fatalf("applyBuildPatches() first run: %v", err)
	}
	if err := applyBuildPatches(repoDir, sourceDir, "2026.7.3"); err != nil {
		t.Fatalf("applyBuildPatches() second run: %v", err)
	}

	enableFreeBSDDiagnosticsOnHost(t, sourceDir)
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-v", "./diagnostic/...")
	command.Dir = sourceDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run patched diagnostics tests: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "TestFreeBSDSystemCollectorReportsUsedMemoryAndOpenFiles") {
		t.Fatalf("patched diagnostics test did not run:\n%s", output)
	}
}

func createFreeBSDDiagnosticsPatchFixture(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	patchDir := filepath.Join(repoDir, "builder", "patches")
	if err := os.MkdirAll(patchDir, 0o755); err != nil {
		t.Fatalf("create patch directory: %v", err)
	}
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	patchData, err := os.ReadFile(filepath.Join(projectRoot, "builder", "patches", "freebsd-diagnostics.patch"))
	if err != nil {
		t.Fatalf("read diagnostics patch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "freebsd-diagnostics.patch"), patchData, 0o600); err != nil {
		t.Fatalf("write diagnostics patch: %v", err)
	}
	writeManifestAt(t, repoDir, `schema_version = 1

[[patches]]
id = "freebsd-diagnostics"
file = "patches/freebsd-diagnostics.patch"
`)
	return repoDir
}

func createCloudflaredDiagnosticsFixture(t *testing.T) string {
	t.Helper()

	sourceDir := t.TempDir()
	files := map[string]string{
		"go.mod": "module github.com/cloudflare/cloudflared\n\ngo 1.26.5\n",
		filepath.Join("diagnostic", "network", "collector_unix.go"): `//go:build darwin || linux

package diagnostic
`,
		filepath.Join("diagnostic", "network", "collector_unix_test.go"): `//go:build darwin || linux

package diagnostic_test
`,
		filepath.Join("diagnostic", "system_collector.go"): `package diagnostic

import "context"

type MemoryInformation struct { MemoryMaximum, MemoryCurrent uint64 }
type FileDescriptorInformation struct { FileDescriptorMaximum, FileDescriptorCurrent uint64 }
type OperatingSystemInformation struct {
	OsSystem, Name, OsVersion, OsRelease, Architecture string
}
type DiskVolumeInformation struct{}
type SystemInformation struct {
	MemoryMaximum, MemoryCurrent, FileDescriptorMaximum, FileDescriptorCurrent uint64
}
type SystemInformationError struct { Err error; RawInfo string }
type SystemInformationGeneralError struct {
	MemoryInformationError SystemInformationError
	FileDescriptorsInformationError SystemInformationError
	DiskVolumeInformationError SystemInformationError
	OperatingSystemInformationError SystemInformationError
}
func (SystemInformationGeneralError) Error() string { return "system collector error" }
func NewSystemInformation(
	memoryMaximum, memoryCurrent, fileDescriptorMaximum, fileDescriptorCurrent uint64,
	osystem, name, osVersion, osRelease, architecture, cloudflaredVersion, goVersion, goArchitecture string,
	disks []DiskVolumeInformation,
) *SystemInformation {
	return &SystemInformation{
		MemoryMaximum: memoryMaximum,
		MemoryCurrent: memoryCurrent,
		FileDescriptorMaximum: fileDescriptorMaximum,
		FileDescriptorCurrent: fileDescriptorCurrent,
	}
}
func collectDiskVolumeInformationUnix(context.Context) ([]DiskVolumeInformation, string, error) {
	return nil, "", nil
}
func collectOSInformationUnix(context.Context) (*OperatingSystemInformation, string, error) {
	return &OperatingSystemInformation{}, "", nil
}
`,
		filepath.Join("diagnostic", "system_collector_linux.go"): `//go:build linux

package diagnostic
`,
	}
	for relativePath, content := range files {
		path := filepath.Join(sourceDir, relativePath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create parent directory for %s: %v", relativePath, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", relativePath, err)
		}
	}
	runGit(t, sourceDir, "init", "--initial-branch=patch-tests")
	runGit(t, sourceDir, "config", "user.email", "alex@goodkind.io")
	runGit(t, sourceDir, "config", "user.name", "Alexander Goodkind")
	runGit(t, sourceDir, "add", ".")
	runGit(t, sourceDir, "commit", "-m", "add diagnostics fixture")
	return sourceDir
}

func enableFreeBSDDiagnosticsOnHost(t *testing.T, sourceDir string) {
	t.Helper()

	files := map[string]string{
		"system_collector_freebsd.go":      "system_collector_host.go",
		"system_collector_freebsd_test.go": "system_collector_host_test.go",
	}
	for sourceName, hostName := range files {
		sourcePath := filepath.Join(sourceDir, "diagnostic", sourceName)
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatalf("read %s: %v", sourceName, err)
		}
		content := strings.Replace(string(data), "//go:build freebsd\n\n", "", 1)
		hostPath := filepath.Join(sourceDir, "diagnostic", hostName)
		if err := os.WriteFile(hostPath, []byte(content), 0o600); err != nil {
			t.Fatalf("enable %s on host: %v", sourceName, err)
		}
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("remove %s: %v", sourceName, err)
		}
	}
}

func TestPatchManifestSelectsTokenPatchAtSupportedVersions(t *testing.T) {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	manifest, err := loadPatchManifest(filepath.Join(projectRoot, "builder", "patches.toml"))
	if err != nil {
		t.Fatalf("loadPatchManifest() error = %v", err)
	}

	tests := []struct {
		version string
		wantIDs []string
	}{
		{version: "2026.7.2", wantIDs: []string{"freebsd-diagnostics"}},
		{version: "2026.7.3", wantIDs: []string{"freebsd-diagnostics", "freebsd-token-file"}},
		{version: "2026.8.0", wantIDs: []string{"freebsd-diagnostics", "freebsd-token-file"}},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			patches, err := selectPatches(manifest, test.version)
			if err != nil {
				t.Fatalf("selectPatches() error = %v", err)
			}
			if len(patches) != len(test.wantIDs) {
				t.Fatalf("selectPatches() returned %d patches, want %d", len(patches), len(test.wantIDs))
			}
			for i, wantID := range test.wantIDs {
				if patches[i].ID != wantID {
					t.Fatalf("selectPatches()[%d].ID = %q, want %q", i, patches[i].ID, wantID)
				}
			}
		})
	}
}

func TestTokenFilePatchCompilesFreeBSDFixtureAndSkipsWhenAlreadyApplied(t *testing.T) {
	sourceDir := createTokenFileFixture(t)
	remoteDir, expectedCommit := createTokenFilePatchRemote(t, sourceDir)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "builder"), 0o755); err != nil {
		t.Fatalf("create builder directory: %v", err)
	}
	writeManifestAt(t, repoDir, fmt.Sprintf(`schema_version = 1

[[patches]]
id = "freebsd-token-file"
[patches.git]
remote = %q
ref = "refs/heads/freebsd-token-file"
expected_commit = %q
`, remoteDir, expectedCommit))

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

func createTokenFileFixture(t *testing.T) string {
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
	for relativePath, content := range files {
		path := filepath.Join(sourceDir, relativePath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create parent directory for %s: %v", relativePath, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", relativePath, err)
		}
	}
	runGit(t, sourceDir, "init", "--initial-branch=patch-tests")
	runGit(t, sourceDir, "config", "user.email", "alex@goodkind.io")
	runGit(t, sourceDir, "config", "user.name", "Alexander Goodkind")
	runGit(t, sourceDir, "add", ".")
	runGit(t, sourceDir, "commit", "-m", "add token fixture")
	return sourceDir
}

func createTokenFilePatchRemote(t *testing.T, sourceDir string) (string, string) {
	t.Helper()

	remoteDir := filepath.Join(t.TempDir(), "cloudflared.git")
	runGit(t, filepath.Dir(remoteDir), "init", "--bare", "--initial-branch=main", remoteDir)
	runGit(t, sourceDir, "remote", "add", "origin", remoteDir)
	runGit(t, sourceDir, "push", "origin", "patch-tests:refs/heads/main")

	upstreamDir := filepath.Join(t.TempDir(), "upstream")
	runGit(t, filepath.Dir(upstreamDir), "clone", remoteDir, upstreamDir)
	runGit(t, upstreamDir, "config", "user.email", "alex@goodkind.io")
	runGit(t, upstreamDir, "config", "user.name", "Alexander Goodkind")
	runGit(t, upstreamDir, "checkout", "-b", "freebsd-token-file")
	servicePath := filepath.Join(upstreamDir, "cmd", "cloudflared", "freebsd_service.go")
	service := "//go:build freebsd\n\npackage main\n\nvar createTokenFile = createTokenFileUnix\n"
	if err := os.WriteFile(servicePath, []byte(service), 0o600); err != nil {
		t.Fatalf("write FreeBSD service: %v", err)
	}
	runGit(t, upstreamDir, "add", "cmd/cloudflared/freebsd_service.go")
	runGit(t, upstreamDir, "commit", "-m", "add FreeBSD token binding")
	expectedCommit := strings.TrimSpace(runGit(t, upstreamDir, "rev-parse", "HEAD"))
	runGit(t, upstreamDir, "push", "origin", "HEAD:refs/heads/freebsd-token-file")
	return remoteDir, expectedCommit
}
