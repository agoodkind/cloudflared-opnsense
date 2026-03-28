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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

func main() {
	fs := flag.NewFlagSet("cloudflared-builder", flag.ExitOnError)
	force := fs.Bool("force", false, "rebuild even if version already built")
	versionFlag := fs.String("version", "", "override cloudflared version")
	revisionFlag := fs.Int("revision", 0, "override revision number (0 = auto from state files)")
	repoDir := fs.String("repo-dir", autoRepoDir(), "path to cloudflared-opnsense repo checkout")
	workDirFlag := fs.String("work-dir", workDir, "scratch directory for builds")
	pkgRepoDirFlag := fs.String("pkg-repo-dir", pkgRepoDir, "directory for pkg repository output")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	args := fs.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cfg := &config{
		force:    *force,
		version:  *versionFlag,
		revision: *revisionFlag,
		repoDir:  *repoDir,
	}

	// Override package-level path vars from flags so all functions
	// pick up CI-supplied directories without needing cfg threading.
	workDir = *workDirFlag
	pkgRepoDir = *pkgRepoDirFlag

	var err error

	switch args[0] {
	case "check":
		err = cmdCheck(cfg)
	case "build":
		err = cmdBuild(cfg)
	case "package":
		err = cmdPackage(cfg)
	case "repo":
		err = cmdRepo(cfg)
	case "publish":
		err = cmdPublish(cfg)
	case "run":
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
		"usage: cloudflared-builder [-force] [-version v] [-revision n] [-repo-dir d]"+
			" <check|build|package|repo|publish|run>")
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
	force    bool
	version  string
	revision int // 0 = derive from state files
	repoDir  string
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

func cmdPublish(cfg *config) error {
	v, rev, err := cfg.resolve()
	if err != nil {
		return err
	}
	if err := createGitHubRelease(v, rev, cfg.repoDir); err != nil {
		logf("WARNING: GitHub release failed: %v (continuing)", err)
	}
	if err := publishRepositoryMetadata(cfg.repoDir); err != nil {
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

	if err := os.MkdirAll(filepath.Join(pkgRepoDir, "All"), 0755); err != nil {
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

	if err := os.MkdirAll(workDir, 0755); err != nil {
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

	logf("compiling")
	if err := runCmd(srcDir, "gmake", "cloudflared"); err != nil {
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
		if err := os.WriteFile(bsdCollector, []byte(patched), 0644); err != nil {
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
	return os.WriteFile(path, []byte(replaced), 0644)
}

// ---- packaging -------------------------------------------------------------

func createBinaryPackage(cfVersion, repoDir string) error {
	logf("creating binary package cloudflared-%s", cfVersion)

	staging := filepath.Join(workDir, "binary-staging")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}

	binDst := filepath.Join(staging, "usr", "local", "bin")
	if err := os.MkdirAll(binDst, 0755); err != nil {
		return err
	}
	if err := copyFile(
		filepath.Join(workDir, "cloudflared", "cloudflared"),
		filepath.Join(binDst, "cloudflared"),
		0755,
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
	if err := os.MkdirAll(outDir, 0755); err != nil {
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
	if err := os.MkdirAll(staging, 0755); err != nil {
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
	if err := copyFile(configdBin, binDst, 0755); err != nil {
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
	if err := os.MkdirAll(outDir, 0755); err != nil {
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
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
// UCL used by OPNsense plugin manifests (simple key: value pairs,
// single-line arrays, no nested objects).
func parseUCLManifest(ucl string) map[string]any {
	m := make(map[string]any)
	for _, line := range strings.Split(ucl, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
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
			Mode: 0644,
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
			Mode:     0755,
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
		_ = os.WriteFile(metaPath, []byte(filtered), 0644)
	}

	// Extract packagesite.yaml from the zstd-compressed packagesite.pkg.
	pkgsitePkg := filepath.Join(pkgRepoDir, "packagesite.pkg")
	pkgsiteYAML := filepath.Join(pkgRepoDir, "packagesite.yaml")
	if err := extractZstdTar(pkgsitePkg, "packagesite.yaml", pkgsiteYAML); err != nil {
		return fmt.Errorf("extract packagesite.yaml: %w", err)
	}

	pluginURL := repoBaseURL + "/" + pluginPkgName + ".pkg"
	binaryURL := repoBaseURL + "/" + binaryPkgName + ".pkg"
	logf("package URLs -- plugin: %s  binary: %s", pluginURL, binaryURL)

	// Patch each NDJSON line.
	updated, err := patchPackageSite(pkgsiteYAML, pkgVersion, cfVersion, pluginURL, binaryURL)
	if err != nil {
		return fmt.Errorf("patch packagesite.yaml: %w", err)
	}
	if err := os.WriteFile(pkgsiteYAML, []byte(updated), 0644); err != nil {
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
	if err := os.MkdirAll(pkgDst, 0755); err != nil {
		return err
	}

	for _, f := range []string{"meta.conf", "meta", "packagesite.yaml", "packagesite.pkg", "data.pkg"} {
		src := filepath.Join(pkgRepoDir, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(pkgDst, f), 0644); err != nil {
			return err
		}
	}

	buildDate := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	headers := fmt.Sprintf("/*\n  Last-Modified: %s\n", buildDate)
	if err := os.WriteFile(filepath.Join(pkgDst, "_headers"), []byte(headers), 0644); err != nil {
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
	_ = os.WriteFile(stateFile, []byte(version+"\n"), 0644)
	_ = os.WriteFile(revisionFile, []byte(strconv.Itoa(revision)+"\n"), 0644)
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
		Mode:    0644,
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

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
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
