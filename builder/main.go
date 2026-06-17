// cloudflared-builder runs on the FreeBSD build host (freebsd-dev).
// It replaces the bash build-and-release.sh script with a deterministic,
// testable Go program.
//
// Subcommands:
//
//	check    Report latest GitHub version vs last-built version and exit 0
//	         if a build is needed, 1 if already up-to-date.
//	build    Clone cloudflared at <version>, apply FreeBSD patches, compile.
//	package  Create pkg(8) packages for the binary and the OPNsense plugin.
//	repo     Re-generate the pkg repository index (packagesite.*).
//	publish  Commit updated metadata and push; optionally create GitHub release.
//	run      check → build → package → repo → publish (one-shot pipeline).
//
// Flags common to all commands:
//
//	-force           Rebuild even if version already built.
//	-version <ver>   Override upstream version (skip GitHub check).
//	-repo-dir <dir>  Path to this repository checkout (default: auto-detect).
package main

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ---- state files -----------------------------------------------------------

const (
	pluginName  = "os-cloudflared"
	repoBaseURL = "https://cloudflared-opnsense-pkg.goodkind.io/All"
)

var (
	stateFile    = "/var/db/cloudflared-build-state"
	revisionFile = "/var/db/cloudflared-revision"
)

var (
	workDir    = "/var/tmp/cloudflared-build"
	pkgRepoDir = "/var/tmp/cloudflared-repo"
)

// ---- GitHub API types ------------------------------------------------------

type ghRelease struct {
	TagName string `json:"tag_name"`
}

// ---- CLI -------------------------------------------------------------------

// command is the subcommand selector. Modelling it as a named enum keeps the
// dispatch switch off bare strings.
type command string

const (
	commandCheck   command = "check"
	commandPlan    command = "plan"
	commandBuild   command = "build"
	commandPackage command = "package"
	commandRepo    command = "repo"
	commandPublish command = "publish"
	commandRun     command = "run"
)

func main() {
	fs := flag.NewFlagSet("cloudflared-builder", flag.ExitOnError)
	force := fs.Bool("force", false, "rebuild even if version already built")
	versionFlag := fs.String("version", "", "override cloudflared version")
	revisionFlag := fs.Int("revision", 0, "override revision number (0 = auto from state files)")
	repoDir := fs.String("repo-dir", autoRepoDir(), "path to cloudflared-opnsense repo checkout")
	workDirFlag := fs.String("work-dir", workDir, "scratch directory for builds")
	pkgRepoDirFlag := fs.String("pkg-repo-dir", pkgRepoDir, "directory for pkg repository output")
	checkOnly := fs.Bool("check-only", false, "publish: decide and emit outputs only, skip upload and release")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	args := fs.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cfg := &config{
		force:     *force,
		version:   *versionFlag,
		revision:  *revisionFlag,
		repoDir:   *repoDir,
		checkOnly: *checkOnly,
	}

	// Override package-level path vars from flags so all functions
	// pick up CI-supplied directories without needing cfg threading.
	workDir = *workDirFlag
	pkgRepoDir = *pkgRepoDirFlag

	var err error

	switch command(args[0]) {
	case commandCheck:
		err = cmdCheck(cfg)
	case commandPlan:
		err = cmdPlan(cfg)
	case commandBuild:
		err = cmdBuild(cfg)
	case commandPackage:
		err = cmdPackage(cfg)
	case commandRepo:
		err = cmdRepo(cfg)
	case commandPublish:
		err = cmdPublish(cfg)
	case commandRun:
		err = cmdRun(cfg)
	default:
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		log.Fatalf("cloudflared-builder %s: %v", args[0], err)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr,
		"usage: cloudflared-builder [-force] [-version v] [-revision n] [-repo-dir d] [-check-only]"+
			" <check|plan|build|package|repo|publish|run>")
}

func autoRepoDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	// Walk up from the binary to find the repo root (contains go.mod).
	dir := filepath.Dir(exe)
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return "."
}

// ---- config ----------------------------------------------------------------

type config struct {
	force     bool
	version   string
	revision  int // 0 = derive from state files
	repoDir   string
	checkOnly bool // publish: decide and emit outputs only, skip upload and release
}

func (c *config) resolve() (version string, revision int, err error) {
	v := c.version
	if v == "" {
		v, err = latestGitHubVersion()
		if err != nil {
			return "", 0, fmt.Errorf("fetch latest version: %w", err)
		}
	}

	// If caller supplied an explicit revision, use it directly.
	if c.revision > 0 {
		return v, c.revision, nil
	}

	last := readState()
	rev := 1
	if last == v && !c.force {
		rev, err = readRevision()
		if err != nil {
			rev = 1
		}
	} else if last == v && c.force {
		rev, err = readRevision()
		if err != nil {
			rev = 1
		} else {
			rev++
		}
	}
	return v, rev, nil
}

// ---- commands --------------------------------------------------------------

func cmdCheck(cfg *config) error {
	latest, err := latestGitHubVersion()
	if err != nil {
		return err
	}
	last := readState()
	logf("latest=%s last_built=%s", latest, emptyOr(last, "none"))
	if latest == last && !cfg.force {
		logf("already up-to-date")
		os.Exit(1)
	}
	logf("build needed")
	return nil
}

func cmdBuild(cfg *config) error {
	v, _, err := cfg.resolve()
	if err != nil {
		return err
	}
	return buildCloudflared(v)
}

func cmdPackage(cfg *config) error {
	v, rev, err := cfg.resolve()
	if err != nil {
		return err
	}
	if err := createBinaryPackage(v, cfg.repoDir); err != nil {
		return err
	}
	return createPluginPackage(v, rev, cfg.repoDir)
}

func cmdRepo(cfg *config) error {
	v, rev, err := cfg.resolve()
	if err != nil {
		return err
	}
	return updatePkgRepository(v, rev)
}

// cmdPublish decides per package whether the built content differs from the
// latest release for the same upstream version. When it does, it uploads the
// packages and repository metadata to R2 and creates the GitHub release. With
// -check-only it emits the decision to GITHUB_OUTPUT and stops, so the workflow
// can gate later steps without performing any publish.
func cmdPublish(cfg *config) error {
	v, rev, err := cfg.resolve()
	if err != nil {
		return err
	}

	decision, err := decidePublish(v, rev, cfg.repoDir)
	if err != nil {
		return err
	}

	writeGitHubOutput("should_publish", strconv.FormatBool(decision.shouldPublish))
	writeGitHubOutput("publish_reason", decision.reason)
	writeGitHubOutput("target_tag", v+"-freebsd-r"+strconv.Itoa(rev))
	writeGitHubOutput("latest_tag", decision.latestTag)
	logf("publish decision: %v (%s)", decision.shouldPublish, decision.reason)

	if cfg.checkOnly {
		return nil
	}
	if !decision.shouldPublish {
		logf("skipping publish: %s", decision.reason)
		return nil
	}

	pkgVersion := v + "_" + strconv.Itoa(rev)
	pkgFiles := []string{
		filepath.Join(pkgRepoDir, "All", "cloudflared-"+v+".pkg"),
		filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg"),
	}
	metadataDir := filepath.Join(filepath.Dir(pkgRepoDir), "pkg")
	if err := uploadToR2(pkgFiles, metadataDir); err != nil {
		return err
	}
	if err := createGitHubRelease(v, rev, cfg.repoDir); err != nil {
		return err
	}
	saveState(v, rev)
	return nil
}

func cmdRun(cfg *config) error {
	v, _, err := cfg.resolve()
	if err != nil {
		return err
	}

	last := readState()
	if v == last && !cfg.force {
		logf("already at latest version %s, nothing to do (use -force to rebuild)", v)
		return nil
	}

	// Sync repo first.
	if err := runCmd(cfg.repoDir, "git", "fetch", "origin", "main"); err != nil {
		return err
	}
	if err := runCmd(cfg.repoDir, "git", "reset", "--hard", "origin/main"); err != nil {
		return err
	}

	rev := 1
	if v == last {
		if r, err := readRevision(); err == nil {
			rev = r + 1
		}
	}

	if err := os.MkdirAll(filepath.Join(pkgRepoDir, "All"), 0o755); err != nil {
		return err
	}

	logf("building cloudflared %s revision %d", v, rev)

	if err := buildCloudflared(v); err != nil {
		return err
	}
	if err := createBinaryPackage(v, cfg.repoDir); err != nil {
		return err
	}
	if err := createPluginPackage(v, rev, cfg.repoDir); err != nil {
		return err
	}
	if err := createGitHubRelease(v, rev, cfg.repoDir); err != nil {
		logf("WARNING: GitHub release failed: %v (continuing)", err)
	}
	if err := updatePkgRepository(v, rev); err != nil {
		return err
	}
	if err := publishRepositoryMetadata(cfg.repoDir); err != nil {
		return err
	}
	saveState(v, rev)
	logf("build and release complete (%s_%d)", v, rev)
	return nil
}

// ---- build -----------------------------------------------------------------

func buildCloudflared(version string) error {
	logf("cloning cloudflared %s", version)

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	srcDir := filepath.Join(workDir, "cloudflared")

	if err := os.RemoveAll(srcDir); err != nil {
		return err
	}

	if err := runCmd(workDir, "git", "clone",
		"--depth", "1", "--branch", version,
		"https://github.com/cloudflare/cloudflared.git",
	); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	logf("applying FreeBSD patches")
	if err := patchFreeBSD(srcDir); err != nil {
		return fmt.Errorf("patch: %w", err)
	}

	buildDate, err := cloudflaredBuildDate(srcDir)
	if err != nil {
		return fmt.Errorf("derive build date: %w", err)
	}

	logf("compiling")
	if err := runCmdWithEnv(srcDir, map[string]string{}, "gmake", "DATE="+buildDate, "cloudflared"); err != nil {
		return fmt.Errorf("gmake: %w", err)
	}

	binPath := filepath.Join(srcDir, "cloudflared")
	if _, err := os.Stat(binPath); err != nil {
		return errors.New("build failed: cloudflared binary not found after gmake")
	}

	info, _ := os.Stat(binPath)
	logf("build complete: cloudflared %d bytes", info.Size())
	return nil
}

func cloudflaredBuildDate(srcDir string) (string, error) {
	raw, err := exec.Command(
		"git", "-C", srcDir, "show", "-s", "--format=%cI", "HEAD",
	).Output()
	if err != nil {
		return "", fmt.Errorf("git show: %w", err)
	}

	commitTime, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return "", fmt.Errorf("parse commit timestamp: %w", err)
	}

	return commitTime.UTC().Format("2006-01-02-15:04 UTC"), nil
}

// patchFreeBSD applies the minimal sed substitutions that make cloudflared
// compile on FreeBSD.  The patches are deliberately minimal to stay mergeable
// with upstream.
func patchFreeBSD(srcDir string) error {
	files := []string{
		filepath.Join(srcDir, "diagnostic", "network", "collector_unix.go"),
		filepath.Join(srcDir, "diagnostic", "network", "collector_unix_test.go"),
	}
	for _, f := range files {
		if err := sedInPlace(f, "darwin || linux", "darwin || linux || freebsd"); err != nil {
			logf("WARNING: patch %s: %v (may not exist in this version)", f, err)
		}
	}

	// Create FreeBSD system collector by copying the Linux one and replacing
	// the build tag.
	linuxCollector := filepath.Join(srcDir, "diagnostic", "system_collector_linux.go")
	bsdCollector := filepath.Join(srcDir, "diagnostic", "system_collector_freebsd.go")
	if data, err := os.ReadFile(linuxCollector); err == nil {
		patched := strings.ReplaceAll(string(data), "linux", "freebsd")
		if err := os.WriteFile(bsdCollector, []byte(patched), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func sedInPlace(path, old, newVal string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	replaced := strings.ReplaceAll(string(data), old, newVal)
	return os.WriteFile(path, []byte(replaced), 0o644)
}

// ---- packaging -------------------------------------------------------------

func createBinaryPackage(cfVersion, repoDir string) error {
	logf("creating binary package cloudflared-%s", cfVersion)

	staging := filepath.Join(workDir, "binary-staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}

	binDst := filepath.Join(staging, "usr", "local", "bin")
	if err := os.MkdirAll(binDst, 0o755); err != nil {
		return err
	}
	if err := copyFile(
		filepath.Join(workDir, "cloudflared", "cloudflared"),
		filepath.Join(binDst, "cloudflared"),
		0o755,
	); err != nil {
		return err
	}

	pkgMeta := filepath.Join(repoDir, "packages", "cloudflared")

	manifest, err := os.ReadFile(filepath.Join(pkgMeta, "+MANIFEST"))
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(manifest), "{{version}}", cfVersion)

	desc, err := os.ReadFile(filepath.Join(pkgMeta, "+DESC"))
	if err != nil {
		return err
	}

	plistPath := filepath.Join(pkgMeta, "pkg-plist")
	scripts := map[string]string{}
	postInstall := filepath.Join(pkgMeta, "+POST_INSTALL")
	if data, err := os.ReadFile(postInstall); err == nil {
		scripts["post-install"] = string(data)
	}

	outDir := filepath.Join(pkgRepoDir, "All")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	pkgFile := filepath.Join(outDir, "cloudflared-"+cfVersion+".pkg")
	if err := createPkgArchive(
		pkgFile, staging, plistPath,
		rendered, string(desc), scripts,
	); err != nil {
		return fmt.Errorf("create binary package: %w", err)
	}

	info, err := os.Stat(pkgFile)
	if err != nil {
		return fmt.Errorf("binary package not found: %s", pkgFile)
	}
	if info.Size() < 10_000_000 {
		return fmt.Errorf("binary package too small (%d bytes)", info.Size())
	}
	logf("binary package: %s (%d bytes)", pkgFile, info.Size())
	return nil
}

func createPluginPackage(cfVersion string, revision int, repoDir string) error {
	pkgVersion := fmt.Sprintf("%s_%d", cfVersion, revision)
	pkgName := pluginName + "-" + pkgVersion
	logf("creating plugin package %s", pkgName)

	staging := filepath.Join(workDir, "plugin-staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}

	revStr := strconv.Itoa(revision)
	makeVars := []string{
		"DESTDIR=" + staging,
		"WRKSRC=" + staging,
		"PLUGIN_VERSION=" + cfVersion,
		"PLUGIN_REVISION=" + revStr,
	}
	if err := runMake(repoDir, "install", makeVars); err != nil {
		return fmt.Errorf("make install: %w", err)
	}

	configdBin := filepath.Join(repoDir, "dist", "cloudflared-configd")
	binDst := filepath.Join(staging, "usr", "local", "bin", "cloudflared-configd")
	if err := copyFile(configdBin, binDst, 0o755); err != nil {
		return fmt.Errorf("copy cloudflared-configd: %w", err)
	}

	if err := runMake(repoDir, "metadata", makeVars); err != nil {
		return fmt.Errorf("make metadata: %w", err)
	}

	plistPath := filepath.Join(staging, "plist")
	extraPlist := []string{
		"/usr/local/bin/cloudflared-configd",
		"@dir /var/log/cloudflared",
		"@dir /usr/local/etc/cloudflared",
	}
	if err := appendPlistLines(plistPath, extraPlist); err != nil {
		return err
	}

	manifestUCL, err := os.ReadFile(filepath.Join(staging, "+MANIFEST"))
	if err != nil {
		return err
	}

	desc, err := os.ReadFile(filepath.Join(staging, "+DESC"))
	if err != nil {
		return err
	}

	scripts := map[string]string{}
	for _, s := range []struct{ file, key string }{
		{"+POST_INSTALL", "post-install"},
		{"+POST_DEINSTALL", "post-deinstall"},
	} {
		data, err := os.ReadFile(filepath.Join(staging, s.file))
		if err != nil {
			return fmt.Errorf("read %s: %w", s.file, err)
		}
		scripts[s.key] = string(data)
	}

	outDir := filepath.Join(pkgRepoDir, "All")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	pkgFile := filepath.Join(outDir, pkgName+".pkg")
	if err := createPkgArchive(
		pkgFile, staging, plistPath,
		string(manifestUCL), string(desc), scripts,
	); err != nil {
		return fmt.Errorf("create plugin package: %w", err)
	}

	if _, err := os.Stat(pkgFile); err != nil {
		return fmt.Errorf("plugin package not found: %s", pkgFile)
	}
	logf("plugin package: %s", pkgFile)
	return nil
}

func runMake(repoDir, target string, vars []string) error {
	args := append([]string{"-C", repoDir}, vars...)
	args = append(args, target)
	cmd := exec.Command("make", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func appendPlistLines(path string, lines []string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// ---- pkg archive builder ---------------------------------------------------
//
// Builds FreeBSD .pkg archives directly in Go, producing the legacy
// manifest format (simple hash strings for files, "y" for directories)
// that is compatible with all pkg versions including OPNsense's pkg 2.x.
// This avoids depending on the host's `pkg create`, which on newer
// FreeBSD produces a detailed object format that older pkg segfaults on.

// parsePlist reads a pkg-plist file and returns regular file paths and @dir
// paths. File paths may be absolute (/usr/local/...) as produced by OPNsense
// Mk/plugins.mk, or relative to prefix (legacy packages/cloudflared layouts).
func parsePlist(path string) (files []string, dirs []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@dir ") {
			dirs = append(dirs, strings.TrimSpace(strings.TrimPrefix(line, "@dir ")))
			continue
		}
		if strings.HasPrefix(line, "@sample ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "@sample "))
		} else if strings.HasPrefix(line, "@shadow ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "@shadow "))
		}
		files = append(files, line)
	}
	return files, dirs, scanner.Err()
}

// sha256File computes the SHA256 hash of a file and returns it in the
// FreeBSD pkg format: "1$<hex>".
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "1$" + hex.EncodeToString(h.Sum(nil)), nil
}

// parseUCLManifest does a minimal parse of UCL-format +MANIFEST files
// into a map suitable for JSON serialization. It handles the subset of
// UCL used by OPNsense plugin manifests: simple key/value pairs,
// single-line arrays, and the generated annotations JSON object.
func parseUCLManifest(ucl string) map[string]any {
	m := make(map[string]any)
	lines := strings.Split(ucl, "\n")
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		line := strings.TrimSpace(lines[lineIndex])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "annotations") {
			annotations, nextLineIndex, ok := parseAnnotationsBlock(lines, lineIndex)
			if ok {
				m["annotations"] = annotations
				lineIndex = nextLineIndex
				continue
			}
		}

		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		key = strings.Trim(key, "\"")
		val := strings.TrimSpace(line[idx+1:])
		val = strings.TrimSuffix(val, ",")
		val = strings.TrimSpace(val)
		val = strings.Trim(val, "\"")

		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := val[1 : len(val)-1]
			var arr []string
			for _, item := range strings.Split(inner, ",") {
				item = strings.TrimSpace(item)
				item = strings.Trim(item, "\"")
				if item != "" {
					arr = append(arr, item)
				}
			}
			m[key] = arr
		} else {
			m[key] = val
		}
	}
	return m
}

func parseAnnotationsBlock(lines []string, startLineIndex int) (map[string]string, int, bool) {
	line := strings.TrimSpace(lines[startLineIndex])
	bodyStart := strings.TrimSpace(strings.TrimPrefix(line, "annotations"))
	if !strings.HasPrefix(bodyStart, "{") {
		return nil, startLineIndex, false
	}

	var body strings.Builder
	braceDepth := 0
	for lineIndex := startLineIndex; lineIndex < len(lines); lineIndex++ {
		blockLine := strings.TrimSpace(lines[lineIndex])
		if lineIndex == startLineIndex {
			blockLine = bodyStart
		}
		body.WriteString(blockLine)
		body.WriteString("\n")
		braceDepth += strings.Count(blockLine, "{")
		braceDepth -= strings.Count(blockLine, "}")
		if braceDepth == 0 {
			annotations := make(map[string]string)
			if err := json.Unmarshal([]byte(body.String()), &annotations); err != nil {
				return nil, startLineIndex, false
			}
			return annotations, lineIndex, true
		}
	}

	return nil, startLineIndex, false
}

func plistAbsPath(prefix, entry string) string {
	if strings.HasPrefix(entry, "/") {
		return entry
	}
	return prefix + "/" + entry
}

func stagingPathForAbs(stagingDir, absPath string) string {
	trim := strings.TrimPrefix(absPath, "/")
	return filepath.Join(stagingDir, filepath.FromSlash(trim))
}

// createPkgArchive builds a FreeBSD .pkg file (zstd-compressed tar)
// from the staging directory and plist, using the legacy manifest format
// compatible with OPNsense's pkg 2.x.
func createPkgArchive(
	outputPath string,
	stagingDir string,
	plistPath string,
	manifestUCL string,
	desc string,
	scripts map[string]string,
) error {
	prefix := "/usr/local"
	plistFiles, plistDirs, err := parsePlist(plistPath)
	if err != nil {
		return fmt.Errorf("parse plist: %w", err)
	}

	m := parseUCLManifest(manifestUCL)
	if p, ok := m["prefix"].(string); ok && p != "" {
		prefix = p
	}

	// Compute file hashes and total flatsize.
	filesMap := make(map[string]string)
	var flatsize int64
	for _, relPath := range plistFiles {
		absPath := plistAbsPath(prefix, relPath)
		diskPath := stagingPathForAbs(stagingDir, absPath)
		hash, err := sha256File(diskPath)
		if err != nil {
			return fmt.Errorf("hash %s: %w", absPath, err)
		}
		filesMap[absPath] = hash

		info, err := os.Stat(diskPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", absPath, err)
		}
		flatsize += info.Size()
	}

	dirsMap := make(map[string]string)
	for _, d := range plistDirs {
		dirsMap[d] = "y"
	}

	m["flatsize"] = flatsize
	m["files"] = filesMap
	if len(dirsMap) > 0 {
		m["directories"] = dirsMap
	}
	m["desc"] = strings.TrimRight(desc, "\n")
	if len(scripts) > 0 {
		m["scripts"] = scripts
	}

	// Detect ABI from the system if not already set.
	if _, ok := m["abi"]; !ok {
		m["abi"] = "FreeBSD:14:amd64"
	}
	if _, ok := m["arch"]; !ok {
		m["arch"] = "freebsd:14:x86:64"
	}

	fullManifest, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// COMPACT_MANIFEST excludes files, directories, and scripts.
	compact := make(map[string]any)
	for k, v := range m {
		if k != "files" && k != "directories" && k != "scripts" {
			compact[k] = v
		}
	}
	compactManifest, err := json.Marshal(compact)
	if err != nil {
		return fmt.Errorf("marshal compact manifest: %w", err)
	}

	// Build the tar archive.
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return err
	}
	defer zw.Close()

	tw := tar.NewWriter(zw)
	defer tw.Close()

	writeMeta := func(name string, data []byte) error {
		hdr := &tar.Header{
			Name: name,
			Size: int64(len(data)),
			Mode: 0o644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	if err := writeMeta("+COMPACT_MANIFEST", compactManifest); err != nil {
		return err
	}
	if err := writeMeta("+MANIFEST", fullManifest); err != nil {
		return err
	}

	// Add files in sorted order (matching pkg create behavior).
	sort.Strings(plistFiles)
	for _, relPath := range plistFiles {
		absPath := plistAbsPath(prefix, relPath)
		diskPath := stagingPathForAbs(stagingDir, absPath)

		info, err := os.Stat(diskPath)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name: filepath.ToSlash(absPath),
			Size: info.Size(),
			Mode: int64(info.Mode()),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(diskPath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}

	// Add @dir entries as empty directory entries.
	sort.Strings(plistDirs)
	for _, d := range plistDirs {
		hdr := &tar.Header{
			Name:     d + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
	}

	return nil
}

// ---- pkg repository --------------------------------------------------------

func updatePkgRepository(cfVersion string, revision int) error {
	pkgVersion := fmt.Sprintf("%s_%d", cfVersion, revision)
	pluginPkgName := pluginName + "-" + pkgVersion
	binaryPkgName := "cloudflared-" + cfVersion

	logf("updating pkg repository metadata")

	allDir := filepath.Join(pkgRepoDir, "All")

	// Remove stale packages so the index stays clean.
	entries, _ := os.ReadDir(allDir)
	for _, e := range entries {
		if e.Name() == pluginPkgName+".pkg" || e.Name() == binaryPkgName+".pkg" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".pkg") {
			_ = os.Remove(filepath.Join(allDir, e.Name()))
		}
	}

	if err := runCmd(pkgRepoDir, "pkg", "repo", "."); err != nil {
		return fmt.Errorf("pkg repo: %w", err)
	}

	// Remove the data= line from meta.conf so pkg doesn't look in /data/.
	metaPath := filepath.Join(pkgRepoDir, "meta.conf")
	if data, err := os.ReadFile(metaPath); err == nil {
		filtered := filterLines(string(data), func(l string) bool {
			return !strings.HasPrefix(strings.TrimSpace(l), "data =")
		})
		_ = os.WriteFile(metaPath, []byte(filtered), 0o644)
	}

	// Extract packagesite.yaml from the zstd-compressed packagesite.pkg.
	pkgsitePkg := filepath.Join(pkgRepoDir, "packagesite.pkg")
	pkgsiteYAML := filepath.Join(pkgRepoDir, "packagesite.yaml")
	if err := extractZstdTar(pkgsitePkg, "packagesite.yaml", pkgsiteYAML); err != nil {
		return fmt.Errorf("extract packagesite.yaml: %w", err)
	}

	pluginURL := repoBaseURL + "/" + pluginPkgName + ".pkg"
	binaryURL := repoBaseURL + "/" + binaryPkgName + ".pkg"
	logf("package URLs: plugin: %s  binary: %s", pluginURL, binaryURL)

	// Patch each NDJSON line.
	updated, err := patchPackageSite(pkgsiteYAML, pkgVersion, cfVersion, pluginURL, binaryURL)
	if err != nil {
		return fmt.Errorf("patch packagesite.yaml: %w", err)
	}
	if err := os.WriteFile(pkgsiteYAML, []byte(updated), 0o644); err != nil {
		return err
	}

	// Recompress.
	if err := os.Remove(pkgsitePkg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := createZstdTar(pkgsitePkg, pkgsiteYAML, "packagesite.yaml"); err != nil {
		return fmt.Errorf("recompress packagesite.pkg: %w", err)
	}

	logf("repository metadata updated")
	return nil
}

// patchPackageSite rewrites the NDJSON packagesite.yaml so that os-cloudflared
// and cloudflared entries carry absolute Cloudflare Tunnel URLs.
func patchPackageSite(path, pluginVer, binaryVer, pluginURL, binaryURL string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			out.WriteString(line + "\n")
			continue
		}

		name, _ := obj["name"].(string)
		ver, _ := obj["version"].(string)

		switch {
		case name == "os-cloudflared" && ver == pluginVer:
			obj["path"] = pluginURL
			obj["repopath"] = pluginURL
		case name == "cloudflared" && ver == binaryVer:
			obj["path"] = binaryURL
			obj["repopath"] = binaryURL
		}

		b, _ := json.Marshal(obj)
		out.Write(b)
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// ---- publish ---------------------------------------------------------------

func createGitHubRelease(version string, revision int, repoDir string) error {
	pkgVersion := fmt.Sprintf("%s_%d", version, revision)
	tag := version + "-freebsd-r" + strconv.Itoa(revision)
	pluginPkg := filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg")
	binaryPkg := filepath.Join(pkgRepoDir, "All", "cloudflared-"+version+".pkg")

	logf("creating GitHub release %s", tag)

	// Delete existing release/tag if present.
	_ = runCmd(repoDir, "gh", "release", "delete", tag, "-y")
	_ = runCmd(repoDir, "git", "push", "--delete", "origin", tag)
	_ = runCmd(repoDir, "git", "tag", "-d", tag)

	notes := fmt.Sprintf(
		"Cloudflared %s packages for FreeBSD\n\n"+
			"- cloudflared-%s.pkg: Binary package\n"+
			"- %s-%s.pkg: OPNsense plugin package",
		version, version, pluginName, pkgVersion,
	)

	return runCmd(repoDir, "gh", "release", "create", tag,
		"--title", fmt.Sprintf("Cloudflared %s packages for FreeBSD (revision %d)", version, revision),
		"--notes", notes,
		binaryPkg,
		pluginPkg,
	)
}

func publishRepositoryMetadata(repoDir string) error {
	logf("publishing repository metadata")

	pkgDst := filepath.Join(repoDir, "pkg")
	if err := os.RemoveAll(pkgDst); err != nil {
		return err
	}
	if err := os.MkdirAll(pkgDst, 0o755); err != nil {
		return err
	}

	for _, f := range []string{"meta.conf", "meta", "packagesite.yaml", "packagesite.pkg", "data.pkg"} {
		src := filepath.Join(pkgRepoDir, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(pkgDst, f), 0o644); err != nil {
			return err
		}
	}

	buildDate := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	headers := fmt.Sprintf("/*\n  Last-Modified: %s\n", buildDate)
	if err := os.WriteFile(filepath.Join(pkgDst, "_headers"), []byte(headers), 0o644); err != nil {
		return err
	}

	_ = runCmd(repoDir, "git", "add", "pkg/")
	if err := runCmd(repoDir, "git", "diff", "--cached", "--quiet"); err == nil {
		logf("no metadata changes to publish")
		return nil
	}
	if err := runCmd(repoDir, "git", "commit", "-m", "Update pkg repository"); err != nil {
		return err
	}
	if err := runCmd(repoDir, "git", "push", "origin", "main"); err != nil {
		return err
	}
	logf("pushed metadata to main branch")
	return nil
}

// ---- state helpers ---------------------------------------------------------

func readState() string {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readRevision() (int, error) {
	data, err := os.ReadFile(revisionFile)
	if err != nil {
		return 1, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func saveState(version string, revision int) {
	_ = os.WriteFile(stateFile, []byte(version+"\n"), 0o644)
	_ = os.WriteFile(revisionFile, []byte(strconv.Itoa(revision)+"\n"), 0o644)
}

// ---- GitHub ----------------------------------------------------------------

func latestGitHubVersion() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/cloudflare/cloudflared/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", errors.New("empty tag_name from GitHub API")
	}
	return rel.TagName, nil
}

// ---- archive helpers -------------------------------------------------------

// extractZstdTar extracts a single named file from a zstd-compressed tar.
func extractZstdTar(archivePath, targetName, outputPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name == targetName || filepath.Base(hdr.Name) == targetName {
			out, err := os.Create(outputPath)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			return err
		}
	}
	return fmt.Errorf("file %q not found in archive %s", targetName, archivePath)
}

// createZstdTar creates a zstd-compressed tar containing a single file.
func createZstdTar(archivePath, filePath, nameInArchive string) error {
	out, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw, err := zstd.NewWriter(out)
	if err != nil {
		return err
	}
	defer zw.Close()

	tw := tar.NewWriter(zw)
	defer tw.Close()

	in, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name:    nameInArchive,
		Size:    info.Size(),
		Mode:    0o644,
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, in)
	return err
}

// ---- fs helpers ------------------------------------------------------------

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func filterLines(s string, keep func(string) bool) string {
	var out strings.Builder
	for _, l := range strings.Split(s, "\n") {
		if keep(l) {
			out.WriteString(l)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// ---- exec helper -----------------------------------------------------------

func runCmd(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdWithEnv(dir string, env map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), envToPairs(env)...)
	return cmd.Run()
}

func envToPairs(values map[string]string) []string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, value))
	}
	return pairs
}

func logf(format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05 MST")
	fmt.Printf("[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

func emptyOr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

// ---- publish decision ------------------------------------------------------

// repoSlug is the GitHub repository whose releases the publish decision compares
// against. r2Bucket is the Cloudflare R2 bucket that serves the pkg repository.
const (
	repoSlug = "agoodkind/cloudflared-opnsense"
	r2Bucket = "cloudflared-opnsense-pkg"

	// maxManifestBytes caps +MANIFEST reads so a crafted package cannot drive an
	// unbounded allocation when decompressed.
	maxManifestBytes = 8 << 20
)

// r2AccountIDPattern confines the environment-supplied Cloudflare account id to
// an alphabet that is safe to interpolate into a command argument.
var r2AccountIDPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// pkgKind selects which version-identity fields a package's manifest sheds
// before fingerprinting.
type pkgKind int

const (
	binaryPkg pkgKind = iota
	pluginPkg
)

// ghReleaseListItem matches the JSON shape from `gh release list --json tagName`
// (camelCase), distinct from the api.github.com tag_name field.
type ghReleaseListItem struct {
	TagName string `json:"tagName"`
}

// publishDecision is the outcome of comparing freshly built packages against the
// latest release for the same upstream version.
type publishDecision struct {
	shouldPublish bool
	reason        string
	latestTag     string
}

// cmdPlan resolves the upstream version and the next revision (highest released
// revision for that version, plus one) and writes them to GITHUB_OUTPUT. It
// replaces the inline workflow shell that derived the same values with curl, gh,
// jq, and sed.
func cmdPlan(cfg *config) error {
	v := cfg.version
	if v == "" {
		latest, err := latestGitHubVersion()
		if err != nil {
			slog.Error("resolve upstream version failed", "err", err)
			return fmt.Errorf("fetch latest version: %w", err)
		}
		v = latest
	}

	highest, err := highestExistingRevision(v, cfg.repoDir)
	if err != nil {
		return err
	}
	rev := highest + 1

	writeGitHubOutput("version", v)
	writeGitHubOutput("revision", strconv.Itoa(rev))
	logf("upstream=%s highest_published=r%d building=r%d", v, highest, rev)
	return nil
}

// readPackageManifest extracts and decodes the +MANIFEST member of a pkg(8)
// archive (zstd-compressed tar). Packages built by this tool carry a JSON
// manifest. The generic map[string]json.RawMessage keeps every field without
// resorting to `any`.
func readPackageManifest(pkgPath string) (map[string]json.RawMessage, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		slog.Error("open package failed", "err", err, "path", pkgPath)
		return nil, fmt.Errorf("open %s: %w", pkgPath, err)
	}
	defer func() { _ = f.Close() }()

	zr, err := zstd.NewReader(f)
	if err != nil {
		slog.Error("open zstd reader failed", "err", err, "path", pkgPath)
		return nil, fmt.Errorf("zstd reader %s: %w", pkgPath, err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			slog.Error("read tar entry failed", "err", err, "path", pkgPath)
			return nil, fmt.Errorf("tar read %s: %w", pkgPath, err)
		}
		if hdr.Name != "+MANIFEST" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
		if err != nil {
			slog.Error("read manifest body failed", "err", err, "path", pkgPath)
			return nil, fmt.Errorf("read +MANIFEST %s: %w", pkgPath, err)
		}
		manifest := map[string]json.RawMessage{}
		if err := json.Unmarshal(data, &manifest); err != nil {
			slog.Error("decode manifest failed", "err", err, "path", pkgPath)
			return nil, fmt.Errorf("decode +MANIFEST %s: %w", pkgPath, err)
		}
		return manifest, nil
	}
	return nil, fmt.Errorf("+MANIFEST not found in %s", pkgPath)
}

// normalizeManifest removes the version- and revision-identity fields that
// change on every build without reflecting a content change. The binary package
// sheds only the top-level version; the plugin package also sheds the OPNsense
// product annotations and the version-stamp file.
func normalizeManifest(manifest map[string]json.RawMessage, kind pkgKind) error {
	delete(manifest, "version")
	if kind != pluginPkg {
		return nil
	}

	if annRaw, ok := manifest["annotations"]; ok {
		annotations := map[string]json.RawMessage{}
		if err := json.Unmarshal(annRaw, &annotations); err != nil {
			slog.Error("decode annotations failed", "err", err)
			return fmt.Errorf("decode annotations: %w", err)
		}
		delete(annotations, "product_version")
		delete(annotations, "product_hash")
		reencoded, err := json.Marshal(annotations)
		if err != nil {
			slog.Error("encode annotations failed", "err", err)
			return fmt.Errorf("encode annotations: %w", err)
		}
		manifest["annotations"] = reencoded
	}

	if filesRaw, ok := manifest["files"]; ok {
		files := map[string]json.RawMessage{}
		if err := json.Unmarshal(filesRaw, &files); err != nil {
			slog.Error("decode files failed", "err", err)
			return fmt.Errorf("decode files: %w", err)
		}
		delete(files, "/usr/local/opnsense/version/cloudflared")
		reencoded, err := json.Marshal(files)
		if err != nil {
			slog.Error("encode files failed", "err", err)
			return fmt.Errorf("encode files: %w", err)
		}
		manifest["files"] = reencoded
	}
	return nil
}

// fingerprintManifest hashes the canonical JSON form of a manifest. encoding/json
// sorts map keys, so the form is deterministic across builds.
func fingerprintManifest(manifest map[string]json.RawMessage) (string, error) {
	canonical, err := json.Marshal(manifest)
	if err != nil {
		slog.Error("marshal manifest failed", "err", err)
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// normalizedManifestFingerprint returns the sha256 of a package's installed
// content with version- and revision-identity fields removed, so byte-equal
// rebuilds of the same source produce the same fingerprint.
func normalizedManifestFingerprint(pkgPath string, kind pkgKind) (string, error) {
	manifest, err := readPackageManifest(pkgPath)
	if err != nil {
		return "", err
	}
	if err := normalizeManifest(manifest, kind); err != nil {
		return "", err
	}
	return fingerprintManifest(manifest)
}

// highestExistingRevision returns the largest revision already released for the
// given upstream version, or 0 if none exists. GitHub release tags are the
// authoritative source of truth.
func highestExistingRevision(version, repoDir string) (int, error) {
	out, err := runCmdOutput(repoDir, "gh", "release", "list",
		"--repo", repoSlug, "--limit", "100", "--json", "tagName")
	if err != nil {
		return 0, err
	}

	var releases []ghReleaseListItem
	if err := json.Unmarshal([]byte(out), &releases); err != nil {
		slog.Error("parse release list failed", "err", err)
		return 0, fmt.Errorf("parse release list: %w", err)
	}

	prefix := version + "-freebsd-r"
	highest := 0
	for _, release := range releases {
		if !strings.HasPrefix(release.TagName, prefix) {
			continue
		}
		revision, convErr := strconv.Atoi(strings.TrimPrefix(release.TagName, prefix))
		if convErr != nil {
			continue
		}
		if revision > highest {
			highest = revision
		}
	}
	return highest, nil
}

// releaseManifestFingerprint downloads one asset from a release and returns its
// normalized manifest fingerprint.
func releaseManifestFingerprint(tag, filename string, kind pkgKind, repoDir string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "cf-prev-release-")
	if err != nil {
		slog.Error("create temp dir failed", "err", err)
		return "", fmt.Errorf("mkdir temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := runCmd(repoDir, "gh", "release", "download", tag,
		"--repo", repoSlug, "--pattern", filename, "--dir", tmpDir); err != nil {
		slog.Error("download release asset failed", "err", err, "tag", tag, "file", filename)
		return "", fmt.Errorf("download %s from %s: %w", filename, tag, err)
	}
	return normalizedManifestFingerprint(filepath.Join(tmpDir, filename), kind)
}

// decidePublish compares the freshly built binary and plugin packages against
// the latest release for the same upstream version. It publishes when there is
// no prior release for the version, or when either package's normalized content
// changed. It fails closed: a prior release whose assets cannot be downloaded or
// hashed is an error rather than a silent publish.
func decidePublish(version string, revision int, repoDir string) (publishDecision, error) {
	var decision publishDecision

	pkgVersion := version + "_" + strconv.Itoa(revision)
	newBinaryPkg := filepath.Join(pkgRepoDir, "All", "cloudflared-"+version+".pkg")
	newPluginPkg := filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg")

	if _, err := os.Stat(newBinaryPkg); err != nil {
		slog.Error("binary package missing", "err", err, "path", newBinaryPkg)
		return decision, fmt.Errorf("binary package not found: %w", err)
	}
	if _, err := os.Stat(newPluginPkg); err != nil {
		slog.Error("plugin package missing", "err", err, "path", newPluginPkg)
		return decision, fmt.Errorf("plugin package not found: %w", err)
	}

	highest, err := highestExistingRevision(version, repoDir)
	if err != nil {
		return decision, err
	}
	if highest > revision {
		staleErr := fmt.Errorf("r%d already published for %s; refusing to publish older r%d",
			highest, version, revision)
		slog.Error("stale revision", "err", staleErr)
		return decision, staleErr
	}
	if highest == 0 {
		decision.shouldPublish = true
		decision.reason = "new_version"
		return decision, nil
	}

	decision.latestTag = version + "-freebsd-r" + strconv.Itoa(highest)
	prevPkgVersion := version + "_" + strconv.Itoa(highest)
	logf("comparing against latest release %s", decision.latestTag)

	newBinaryFP, err := normalizedManifestFingerprint(newBinaryPkg, binaryPkg)
	if err != nil {
		return decision, err
	}
	newPluginFP, err := normalizedManifestFingerprint(newPluginPkg, pluginPkg)
	if err != nil {
		return decision, err
	}
	prevBinaryFP, err := releaseManifestFingerprint(
		decision.latestTag, "cloudflared-"+version+".pkg", binaryPkg, repoDir)
	if err != nil {
		return decision, err
	}
	prevPluginFP, err := releaseManifestFingerprint(
		decision.latestTag, pluginName+"-"+prevPkgVersion+".pkg", pluginPkg, repoDir)
	if err != nil {
		return decision, err
	}

	binaryChanged := newBinaryFP != prevBinaryFP
	pluginChanged := newPluginFP != prevPluginFP
	switch {
	case binaryChanged && pluginChanged:
		decision.shouldPublish = true
		decision.reason = "binary_and_plugin_content_changed"
	case binaryChanged:
		decision.shouldPublish = true
		decision.reason = "binary_content_changed"
	case pluginChanged:
		decision.shouldPublish = true
		decision.reason = "plugin_content_changed"
	default:
		decision.shouldPublish = false
		decision.reason = "no_meaningful_change"
	}
	return decision, nil
}

// uploadToR2 mirrors the given package files and the repository metadata to the
// R2 bucket by invoking the aws CLI, the same way this tool already invokes gh
// and git. The aws credentials and CF_ACCOUNT_ID come from the environment.
func uploadToR2(pkgFiles []string, metadataDir string) error {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	// Validate before interpolating into a command argument: the account id is
	// an external (environment) value, and the regexp guard both rejects bad
	// input and confines it to an injection-safe alphabet.
	if !r2AccountIDPattern.MatchString(accountID) {
		invalid := fmt.Errorf("CF_ACCOUNT_ID is missing or malformed: %q", accountID)
		slog.Error("invalid R2 account id", "err", invalid)
		return invalid
	}
	endpoint := "https://" + accountID + ".r2.cloudflarestorage.com"

	for _, src := range pkgFiles {
		dst := "s3://" + r2Bucket + "/All/" + filepath.Base(src)
		if err := runCmd("", "aws", "s3", "cp", src, dst, "--endpoint-url", endpoint); err != nil {
			return fmt.Errorf("upload %s: %w", src, err)
		}
	}

	for _, name := range []string{"meta.conf", "meta", "packagesite.yaml", "packagesite.pkg", "data.pkg"} {
		src := filepath.Join(metadataDir, name)
		if _, statErr := os.Stat(src); statErr != nil {
			continue
		}
		dst := "s3://" + r2Bucket + "/" + name
		if err := runCmd("", "aws", "s3", "cp", src, dst, "--endpoint-url", endpoint); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
	}

	logf("R2 upload complete")
	return nil
}

// runCmdOutput runs a command and captures its stdout, mirroring runCmd but for
// callers that need the output (for example parsing `gh release list`).
func runCmdOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		slog.Error("command failed", "err", err, "command", name)
		return "", fmt.Errorf("run %s: %w", name, err)
	}
	return string(out), nil
}

// writeGitHubOutput appends a key/value pair to the GITHUB_OUTPUT file when run
// under GitHub Actions; it is a no-op otherwise.
func writeGitHubOutput(key, value string) {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return
	}
	// The path is an external (environment) value. Clean it and require an
	// absolute path so a relative or traversal-laden value cannot redirect the
	// write outside the runner-provided location.
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		logf("WARNING: ignoring non-absolute GITHUB_OUTPUT path: %q", path)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		logf("WARNING: could not write GITHUB_OUTPUT: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(key + "=" + value + "\n"); err != nil {
		logf("WARNING: could not write GITHUB_OUTPUT: %v", err)
	}
}
