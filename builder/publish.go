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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

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
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://api.github.com/repos/cloudflare/cloudflared/releases/latest", nil)
	if err != nil {
		slog.Error("build latest-version request failed", "err", err)
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// Authenticate when a token is present to avoid unauthenticated rate limits.
	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("fetch latest version failed", "err", err)
		return "", fmt.Errorf("request latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("github API returned status %d", resp.StatusCode)
		slog.Error("unexpected github status", "err", statusErr)
		return "", statusErr
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		slog.Error("decode latest release failed", "err", err)
		return "", fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return "", errors.New("empty tag_name from GitHub API")
	}
	return rel.TagName, nil
}

// ---- archive helpers -------------------------------------------------------

// maxExtractBytes caps a single extracted metadata member so a crafted or
// corrupt archive cannot drive unbounded disk use. Repository metadata files
// are well under this.
const maxExtractBytes = 64 << 20

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
			_, err = io.Copy(out, io.LimitReader(tr, maxExtractBytes))
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
