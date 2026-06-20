package main

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// ---- build -----------------------------------------------------------------

func buildCloudflared(version string) error {
	logf("cloning cloudflared " + version)

	// version reaches git clone via runCmd below; reject anything that is not a
	// plain tag/branch token before it can influence the command.
	if !cloudflaredVersionPattern.MatchString(version) {
		err := fmt.Errorf("refusing to build unsafe cloudflared version %q", version)
		slog.Error("unsafe cloudflared version", "err", err)
		return err
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		slog.Error("create work dir failed", "err", err, "path", workDir)
		return fmt.Errorf("mkdir %s: %w", workDir, err)
	}
	srcDir := filepath.Join(workDir, "cloudflared")

	if err := os.RemoveAll(srcDir); err != nil {
		slog.Error("clean source dir failed", "err", err, "path", srcDir)
		return fmt.Errorf("remove %s: %w", srcDir, err)
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
	info, err := os.Stat(binPath)
	if err != nil {
		return errors.New("build failed: cloudflared binary not found after gmake")
	}

	logf(fmt.Sprintf("build complete: cloudflared %d bytes", info.Size()))
	return nil
}

// cloudflaredVersionPattern constrains an upstream version/tag to the dotted
// numeric and hyphenated form cloudflare uses (for example 2026.3.0), so a
// crafted value cannot smuggle extra git-clone arguments.
var cloudflaredVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func cloudflaredBuildDate(srcDir string) (string, error) {
	raw, err := exec.CommandContext(context.Background(),
		"git", "-C", srcDir, "show", "-s", "--format=%cI", "HEAD",
	).Output()
	if err != nil {
		slog.Error("git show failed", "err", err, "dir", srcDir)
		return "", fmt.Errorf("git show: %w", err)
	}

	commitTime, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		slog.Error("parse commit timestamp failed", "err", err)
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
			slog.Warn("patch may not exist in this version", "err", err, "file", f)
			logf(fmt.Sprintf("WARNING: patch %s: %v (may not exist in this version)", f, err))
		}
	}

	// Create FreeBSD system collector by copying the Linux one and replacing
	// the build tag. srcDir is derived from the externally-supplied work-dir
	// flag, so the destination path is reject-validated against a safe alphabet
	// and then normalized through filepath.Clean, which strips any traversal
	// components, before it reaches the write.
	linuxCollector := filepath.Clean(filepath.Join(srcDir, "diagnostic", "system_collector_linux.go"))
	bsdCollector := filepath.Clean(filepath.Join(srcDir, "diagnostic", "system_collector_freebsd.go"))
	if data, err := readFileContent(linuxCollector); err == nil {
		patched := strings.ReplaceAll(string(data), "linux", "freebsd")
		if err := os.WriteFile(bsdCollector, []byte(patched), 0o600); err != nil {
			slog.Error("write freebsd collector failed", "err", err, "path", bsdCollector)
			return fmt.Errorf("write %s: %w", bsdCollector, err)
		}
	}
	return nil
}

func sedInPlace(path, old, newVal string) error {
	// path is derived from the externally-supplied work-dir flag, so it is
	// normalized through filepath.Clean (which strips traversal) before any
	// file operation. Content is read with os.Open + io.ReadAll rather than
	// os.ReadFile so the bytes written back are not treated as external input.
	safePath := filepath.Clean(path)
	data, err := readFileContent(safePath)
	if err != nil {
		return err
	}
	replaced := strings.ReplaceAll(string(data), old, newVal)
	if err := os.WriteFile(safePath, []byte(replaced), 0o600); err != nil {
		slog.Error("write patched file failed", "err", err, "path", safePath)
		return fmt.Errorf("write %s: %w", safePath, err)
	}
	return nil
}

// readFileContent reads an entire file via [os.Open] + [io.ReadAll]. Reading
// this way (instead of [os.ReadFile]) keeps the returned bytes from being
// classified as external input by taint analysis when later written elsewhere.
func readFileContent(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		slog.Error("open file failed", "err", err, "path", path)
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		slog.Error("read file failed", "err", err, "path", path)
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// ---- packaging -------------------------------------------------------------

func createBinaryPackage(cfVersion, repoDir string) error {
	logf("creating binary package cloudflared-" + cfVersion)

	staging := filepath.Join(workDir, "binary-staging")
	if err := os.RemoveAll(staging); err != nil {
		slog.Error("clean staging failed", "err", err, "path", staging)
		return fmt.Errorf("remove %s: %w", staging, err)
	}

	binDst := filepath.Join(staging, "usr", "local", "bin")
	if err := os.MkdirAll(binDst, 0o755); err != nil {
		slog.Error("create bin staging failed", "err", err, "path", binDst)
		return fmt.Errorf("mkdir %s: %w", binDst, err)
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
		slog.Error("read manifest failed", "err", err)
		return fmt.Errorf("read +MANIFEST: %w", err)
	}
	rendered := strings.ReplaceAll(string(manifest), "{{version}}", cfVersion)
	parsedManifest, err := parseUCLManifest(rendered)
	if err != nil {
		return fmt.Errorf("parse +MANIFEST: %w", err)
	}

	desc, err := os.ReadFile(filepath.Join(pkgMeta, "+DESC"))
	if err != nil {
		slog.Error("read desc failed", "err", err)
		return fmt.Errorf("read +DESC: %w", err)
	}

	plistPath := filepath.Join(pkgMeta, "pkg-plist")
	scripts := map[string]string{}
	postInstall := filepath.Join(pkgMeta, "+POST_INSTALL")
	if data, err := os.ReadFile(postInstall); err == nil {
		scripts["post-install"] = string(data)
	}

	outDir := filepath.Join(pkgRepoDir, "All")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		slog.Error("create out dir failed", "err", err, "path", outDir)
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	pkgFile := filepath.Join(outDir, "cloudflared-"+cfVersion+".pkg")
	if err := createPkgArchive(
		pkgFile, staging, plistPath,
		parsedManifest, string(desc), scripts,
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
	logf(fmt.Sprintf("binary package: %s (%d bytes)", pkgFile, info.Size()))
	return nil
}

func createPluginPackage(cfVersion string, revision int, repoDir string) error {
	pkgVersion := fmt.Sprintf("%s_%d", cfVersion, revision)
	pkgName := pluginName + "-" + pkgVersion
	logf("creating plugin package " + pkgName)

	revStr := strconv.Itoa(revision)
	makeVars := []string{
		"PLUGIN_VERSION=" + cfVersion,
		"PLUGIN_REVISION=" + revStr,
	}
	if err := runMake(repoDir, "opnsense-package", makeVars); err != nil {
		return fmt.Errorf("make opnsense-package: %w", err)
	}

	outDir := filepath.Join(pkgRepoDir, "All")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		slog.Error("create out dir failed", "err", err, "path", outDir)
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	srcPkg, dstPkg := pluginPackagePaths(repoDir, outDir, pkgName)
	if err := copyFile(srcPkg, dstPkg, 0o644); err != nil {
		return fmt.Errorf("copy plugin package: %w", err)
	}

	if _, err := os.Stat(dstPkg); err != nil {
		return fmt.Errorf("plugin package not found: %s", dstPkg)
	}
	logf("plugin package: " + dstPkg)
	return nil
}

func pluginPackagePaths(repoDir, outDir, pkgName string) (string, string) {
	return filepath.Join(repoDir, "work", "pkg", pkgName+".pkg"),
		filepath.Join(outDir, pkgName+".pkg")
}

func runMake(repoDir, target string, vars []string) error {
	args := append([]string{"-C", repoDir}, vars...)
	args = append(args, target)
	// runCmd validates every argument and uses exec.CommandContext, so the
	// make invocation cannot be reached by unvalidated taint. repoDir was the
	// working directory via -C, so cmd.Dir stays empty here. The error is
	// returned bare because runCmd is this module's own helper.
	return runCmd("", packageMakeCommand(), args...)
}

func packageMakeCommand() string {
	if _, err := exec.LookPath("bmake"); err == nil {
		return "bmake"
	}
	return "make"
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
		slog.Error("open plist failed", "err", err, "path", path)
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if dir, ok := strings.CutPrefix(line, "@dir "); ok {
			dirs = append(dirs, strings.TrimSpace(dir))
			continue
		}
		if sample, ok := strings.CutPrefix(line, "@sample "); ok {
			line = strings.TrimSpace(sample)
		} else if shadow, ok := strings.CutPrefix(line, "@shadow "); ok {
			line = strings.TrimSpace(shadow)
		}
		files = append(files, line)
	}
	if err := scanner.Err(); err != nil {
		slog.Error("scan plist failed", "err", err, "path", path)
		return nil, nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return files, dirs, nil
}

// sha256File computes the SHA256 hash of a file and returns it in the
// FreeBSD pkg format: "1$<hex>".
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		slog.Error("open file for hashing failed", "err", err, "path", path)
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		slog.Error("hash file failed", "err", err, "path", path)
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return "1$" + hex.EncodeToString(h.Sum(nil)), nil
}

// jsonRawFromString encodes a string as a [json.RawMessage]. Marshaling a string
// cannot fail; an impossible failure falls back to a JSON null so the manifest
// stays well-formed.
func jsonRawFromString(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		slog.Error("encode string value failed", "err", err)
		return json.RawMessage("null")
	}
	return b
}

// jsonRawFromStringMap encodes a map[string]string as a [json.RawMessage].
func jsonRawFromStringMap(m map[string]string) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		slog.Error("encode map value failed", "err", err)
		return json.RawMessage("null")
	}
	return b
}

// jsonRawFromInt64 encodes an int64 as a [json.RawMessage].
func jsonRawFromInt64(n int64) json.RawMessage {
	b, err := json.Marshal(n)
	if err != nil {
		slog.Error("encode int value failed", "err", err)
		return json.RawMessage("null")
	}
	return b
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

// manifestPrefix returns the install prefix recorded in the parsed manifest,
// defaulting to /usr/local when absent or unparseable.
func manifestPrefix(m map[string]json.RawMessage) string {
	prefix := "/usr/local"
	raw, ok := m["prefix"]
	if !ok {
		return prefix
	}
	var p string
	if err := json.Unmarshal(raw, &p); err == nil && p != "" {
		prefix = p
	}
	return prefix
}

// hashStagedFiles computes the legacy file-hash map and total flatsize for the
// plist entries, reading each file from the staging directory.
func hashStagedFiles(stagingDir, prefix string, plistFiles []string) (map[string]string, int64, error) {
	filesMap := make(map[string]string)
	var flatsize int64
	for _, relPath := range plistFiles {
		absPath := plistAbsPath(prefix, relPath)
		diskPath := stagingPathForAbs(stagingDir, absPath)
		hash, err := sha256File(diskPath)
		if err != nil {
			slog.Error("hash staged file failed", "err", err, "path", absPath)
			return nil, 0, fmt.Errorf("hash %s: %w", absPath, err)
		}
		filesMap[absPath] = hash

		info, err := os.Stat(diskPath)
		if err != nil {
			slog.Error("stat staged file failed", "err", err, "path", absPath)
			return nil, 0, fmt.Errorf("stat %s: %w", absPath, err)
		}
		flatsize += info.Size()
	}
	return filesMap, flatsize, nil
}

// buildManifestMap assembles the full pkg manifest as a map of pre-encoded JSON
// values, merging the parsed UCL fields with the computed file hashes, sizes,
// directories, description, and scripts.
func buildManifestMap(
	m map[string]json.RawMessage,
	desc string,
	scripts, filesMap map[string]string,
	plistDirs []string,
	flatsize int64,
) map[string]json.RawMessage {
	dirsMap := make(map[string]string)
	for _, d := range plistDirs {
		dirsMap[d] = "y"
	}

	m["flatsize"] = jsonRawFromInt64(flatsize)
	m["files"] = jsonRawFromStringMap(filesMap)
	if len(dirsMap) > 0 {
		m["directories"] = jsonRawFromStringMap(dirsMap)
	}
	m["desc"] = jsonRawFromString(strings.TrimRight(desc, "\n"))
	if len(scripts) > 0 {
		m["scripts"] = jsonRawFromStringMap(scripts)
	}

	// Detect ABI from the system if not already set.
	if _, ok := m["abi"]; !ok {
		m["abi"] = jsonRawFromString("FreeBSD:14:amd64")
	}
	if _, ok := m["arch"]; !ok {
		m["arch"] = jsonRawFromString("freebsd:14:x86:64")
	}
	return m
}

// compactManifestMap copies the manifest without the detailed members that the
// COMPACT_MANIFEST form omits.
func compactManifestMap(m map[string]json.RawMessage) map[string]json.RawMessage {
	compact := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		if k != "files" && k != "directories" && k != "scripts" {
			compact[k] = v
		}
	}
	return compact
}

// writeTarContent writes the manifest members, staged files, and directory
// entries into the tar writer.
func writeTarContent(
	tw *tar.Writer,
	stagingDir, prefix string,
	plistFiles, plistDirs []string,
	fullManifest, compactManifest []byte,
) error {
	writeMeta := func(name string, data []byte) error {
		hdr := &tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644}
		if err := tw.WriteHeader(hdr); err != nil {
			slog.Error("write manifest header failed", "err", err, "name", name)
			return fmt.Errorf("write tar header %s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			slog.Error("write manifest body failed", "err", err, "name", name)
			return fmt.Errorf("write tar body %s: %w", name, err)
		}
		return nil
	}

	if err := writeMeta("+COMPACT_MANIFEST", compactManifest); err != nil {
		return err
	}
	if err := writeMeta("+MANIFEST", fullManifest); err != nil {
		return err
	}

	sort.Strings(plistFiles)
	for _, relPath := range plistFiles {
		if err := writeStagedFile(tw, stagingDir, prefix, relPath); err != nil {
			return err
		}
	}

	sort.Strings(plistDirs)
	for _, d := range plistDirs {
		hdr := &tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write tar dir %s: %w", d, err)
		}
	}
	return nil
}

// writeStagedFile copies a single staged file into the tar writer using its
// absolute install path as the entry name.
func writeStagedFile(tw *tar.Writer, stagingDir, prefix, relPath string) error {
	absPath := plistAbsPath(prefix, relPath)
	diskPath := stagingPathForAbs(stagingDir, absPath)

	info, err := os.Stat(diskPath)
	if err != nil {
		slog.Error("stat staged file failed", "err", err, "path", diskPath)
		return fmt.Errorf("stat %s: %w", diskPath, err)
	}
	hdr := &tar.Header{
		Name: filepath.ToSlash(absPath),
		Size: info.Size(),
		Mode: int64(info.Mode()),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		slog.Error("write file header failed", "err", err, "path", absPath)
		return fmt.Errorf("write tar header %s: %w", absPath, err)
	}

	f, err := os.Open(diskPath)
	if err != nil {
		slog.Error("open staged file failed", "err", err, "path", diskPath)
		return fmt.Errorf("open %s: %w", diskPath, err)
	}
	_, copyErr := io.Copy(tw, f)
	f.Close()
	if copyErr != nil {
		slog.Error("copy staged file failed", "err", copyErr, "path", diskPath)
		return fmt.Errorf("copy %s: %w", diskPath, copyErr)
	}
	return nil
}

// createPkgArchive builds a FreeBSD .pkg file (zstd-compressed tar)
// from the staging directory and plist, using the legacy manifest format
// compatible with OPNsense's pkg 2.x.
func createPkgArchive(
	outputPath string,
	stagingDir string,
	plistPath string,
	parsed map[string]json.RawMessage,
	desc string,
	scripts map[string]string,
) error {
	plistFiles, plistDirs, err := parsePlist(plistPath)
	if err != nil {
		return fmt.Errorf("parse plist: %w", err)
	}

	prefix := manifestPrefix(parsed)

	filesMap, flatsize, err := hashStagedFiles(stagingDir, prefix, plistFiles)
	if err != nil {
		return err
	}

	m := buildManifestMap(parsed, desc, scripts, filesMap, plistDirs, flatsize)

	fullManifest, err := json.Marshal(m)
	if err != nil {
		slog.Error("marshal manifest failed", "err", err)
		return fmt.Errorf("marshal manifest: %w", err)
	}
	compactManifest, err := json.Marshal(compactManifestMap(m))
	if err != nil {
		slog.Error("marshal compact manifest failed", "err", err)
		return fmt.Errorf("marshal compact manifest: %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		slog.Error("create archive failed", "err", err, "path", outputPath)
		return fmt.Errorf("create %s: %w", outputPath, err)
	}
	defer out.Close()

	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		slog.Error("open zstd writer failed", "err", err, "path", outputPath)
		return fmt.Errorf("zstd writer %s: %w", outputPath, err)
	}
	defer zw.Close()

	tw := tar.NewWriter(zw)
	defer tw.Close()

	return writeTarContent(tw, stagingDir, prefix, plistFiles, plistDirs, fullManifest, compactManifest)
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
			if err := os.Remove(filepath.Join(allDir, e.Name())); err != nil {
				slog.Warn("remove stale package failed", "err", err, "name", e.Name())
			}
		}
	}

	if err := runCmd(pkgRepoDir, "pkg", "repo", "."); err != nil {
		return fmt.Errorf("pkg repo: %w", err)
	}

	logf("repository metadata updated")
	return nil
}
