package main

import (
	"archive/tar"
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
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ---- publish ---------------------------------------------------------------

func createGitHubRelease(version string, revision int, sourceCommit string, repoDir string) error {
	pkgVersion := fmt.Sprintf("%s_%d", version, revision)
	tag := version + "-freebsd-r" + strconv.Itoa(revision)
	pluginPkg := filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg")
	binaryPkg := filepath.Join(pkgRepoDir, "All", "cloudflared-"+version+".pkg")

	logf("creating GitHub release " + tag)

	// Delete existing release/tag if present.
	_ = runCmd(repoDir, "gh", "release", "delete", tag, "-y")
	_ = runCmd(repoDir, "git", "push", "--delete", "origin", tag)
	_ = runCmd(repoDir, "git", "tag", "-d", tag)

	notes := fmt.Sprintf(
		"Cloudflared %s packages for FreeBSD\n\n"+
			"Upstream commit: `%s`\n\n"+
			"- cloudflared-%s.pkg: Binary package\n"+
			"- %s-%s.pkg: OPNsense plugin package",
		version, sourceCommit, version, pluginName, pkgVersion,
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
		slog.Error("remove pkg dst failed", "err", err, "path", pkgDst)
		return fmt.Errorf("remove %s: %w", pkgDst, err)
	}
	if err := os.MkdirAll(pkgDst, 0o755); err != nil {
		slog.Error("create pkg dst failed", "err", err, "path", pkgDst)
		return fmt.Errorf("mkdir %s: %w", pkgDst, err)
	}

	metadataPaths, err := repoMetadataPaths(pkgRepoDir)
	if err != nil {
		return err
	}

	for _, src := range metadataPaths {
		if err := copyFile(src, filepath.Join(pkgDst, filepath.Base(src)), 0o644); err != nil {
			return err
		}
	}

	buildDate := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
	headers := fmt.Sprintf("/*\n  Last-Modified: %s\n", buildDate)
	if err := os.WriteFile(filepath.Join(pkgDst, "_headers"), []byte(headers), 0o600); err != nil {
		slog.Error("write headers failed", "err", err)
		return fmt.Errorf("write _headers: %w", err)
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
		// A missing revision file is the normal "not yet built" case, so this
		// is a warning rather than an error.
		slog.Warn("read revision file failed", "err", err, "path", revisionFile)
		return 1, fmt.Errorf("read revision file: %w", err)
	}
	rev, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		slog.Warn("parse revision failed", "err", err, "path", revisionFile)
		return 1, fmt.Errorf("parse revision: %w", err)
	}
	return rev, nil
}

func saveState(version string, revision int) {
	if err := os.WriteFile(stateFile, []byte(version+"\n"), 0o600); err != nil {
		slog.Error("write state file failed", "err", err, "path", stateFile)
	}
	if err := os.WriteFile(revisionFile, []byte(strconv.Itoa(revision)+"\n"), 0o600); err != nil {
		slog.Error("write revision file failed", "err", err, "path", revisionFile)
	}
}

// ---- GitHub ----------------------------------------------------------------

func latestGitHubVersion() (string, error) {
	release, err := resolveUpstreamRelease("")
	if err != nil {
		return "", err
	}
	return release.Version, nil
}

// ---- fs helpers ------------------------------------------------------------

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		slog.Error("open source failed", "err", err, "path", src)
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		slog.Error("create destination dir failed", "err", err, "path", dst)
		return fmt.Errorf("mkdir for %s: %w", dst, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		slog.Error("open destination failed", "err", err, "path", dst)
		return fmt.Errorf("open %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		slog.Error("copy file contents failed", "err", err, "src", src, "dst", dst)
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// ---- exec helper -----------------------------------------------------------

// runCmd and runCmdWithEnv invoke trusted build tools (gh, git, aws, gmake,
// make, pkg) by name with separate argv, never through a shell, so argument
// content cannot inject a command. gosec's G702 taint pass still flags these
// because the argv carries flag- and environment-derived values (version
// strings, the R2 account id, file paths). Those legitimately include spaces,
// newlines, and parentheses (for example the release title and notes), so an
// alphabet-restricting gate would corrupt them; the G702 findings are baselined
// as false positives instead.

func runCmd(dir string, name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		slog.Error("command failed", "err", err, "command", name)
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}

func runCmdWithEnv(dir string, env map[string]string, name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), envToPairs(env)...)
	if err := cmd.Run(); err != nil {
		slog.Error("command failed", "err", err, "command", name)
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}

func envToPairs(values map[string]string) []string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, value))
	}
	return pairs
}

// logf prints a timestamped, preformatted line to stdout. Callers that need
// interpolation preformat with [fmt.Sprintf]; this is user-facing CLI output,
// which package main may emit through fmt.
func logf(msg string) {
	ts := time.Now().Format("2006-01-02 15:04:05 MST")
	fmt.Printf("[%s] %s\n", ts, msg)
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
	release, err := resolveUpstreamRelease(cfg.version)
	if err != nil {
		slog.Error("resolve upstream release failed", "err", err)
		return fmt.Errorf("resolve upstream release: %w", err)
	}
	v := release.Version

	highest, err := highestExistingRevision(v, cfg.repoDir)
	if err != nil {
		return err
	}
	rev := highest + 1

	writeGitHubOutput("version", v)
	writeGitHubOutput("revision", strconv.Itoa(rev))
	writeGitHubOutput("source_commit", release.Commit)
	logf(fmt.Sprintf("upstream=%s highest_published=r%d building=r%d", v, highest, rev))
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
	out, err := listPublishedReleases(repoDir)
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

var listPublishedReleases = func(repoDir string) (string, error) {
	return runCmdOutput(repoDir, "gh", "release", "list",
		"--repo", repoSlug, "--limit", "100", "--json", "tagName")
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
	logf("comparing against latest release " + decision.latestTag)

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
		logf(fmt.Sprintf("WARNING: ignoring non-absolute GITHUB_OUTPUT path: %q", path))
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		logf(fmt.Sprintf("WARNING: could not write GITHUB_OUTPUT: %v", err))
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(key + "=" + value + "\n"); err != nil {
		logf(fmt.Sprintf("WARNING: could not write GITHUB_OUTPUT: %v", err))
	}
}
