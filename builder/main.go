// cloudflared-builder runs on the FreeBSD build host (freebsd-dev).
// It replaces the bash build-and-release.sh script with a deterministic,
// testable Go program.
//
// Subcommands:
//
//	check    Report latest GitHub version vs last-built version and exit 0
//	         if a build is needed, 1 if already up-to-date.
//	build    Clone cloudflared at <version>, apply declared patches, compile.
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
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---- state files -----------------------------------------------------------

const (
	pluginName = "os-cloudflared"
)

var (
	stateFile    = "/var/db/cloudflared-build-state"
	revisionFile = "/var/db/cloudflared-revision"
)

var (
	workDir    = "/var/tmp/cloudflared-build"
	pkgRepoDir = "/var/tmp/cloudflared-repo"
)

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
	sourceCommitFlag := fs.String("source-commit", "", "verified 40-character upstream commit")
	revisionFlag := fs.Int("revision", 0, "override revision number (0 = auto from state files)")
	repoDir := fs.String("repo-dir", autoRepoDir(), "path to cloudflared-opnsense repo checkout")
	workDirFlag := fs.String("work-dir", workDir, "scratch directory for builds")
	pkgRepoDirFlag := fs.String("pkg-repo-dir", pkgRepoDir, "directory for pkg repository output")
	checkOnly := fs.Bool("check-only", false, "publish: decide and emit outputs only, skip upload and release")
	preview := fs.Bool("preview", false, "publish: round-trip packages through a throwaway prefix (upload, verify, delete); never release")
	previewPrefix := fs.String("preview-prefix", "", "publish: key prefix for -preview objects (for example previews/pr-12)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	args := fs.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cfg := &config{
		force:         *force,
		version:       *versionFlag,
		sourceCommit:  *sourceCommitFlag,
		revision:      *revisionFlag,
		repoDir:       *repoDir,
		checkOnly:     *checkOnly,
		preview:       *preview,
		previewPrefix: *previewPrefix,
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

	if errors.Is(err, errUpToDate) {
		// `check` signals "no build needed" through the process exit code so the
		// workflow can gate later steps; this is not a failure.
		os.Exit(1)
	}
	if err != nil {
		slog.Error("command failed", "err", err, "command", args[0])
		fmt.Fprintf(os.Stderr, "cloudflared-builder %s: %v\n", args[0], err)
		os.Exit(1)
	}
}

// errUpToDate is returned by cmdCheck when the latest upstream version is
// already built. main translates it into a non-failure exit code so the
// [os.Exit] stays in main rather than in a command function.
var errUpToDate = errors.New("already up-to-date")

func printUsage() {
	fmt.Fprintln(os.Stderr,
		"usage: cloudflared-builder [-force] [-version v] [-source-commit sha] [-revision n] [-repo-dir d]"+
			" [-check-only] [-preview] [-preview-prefix p]"+
			" <check|plan|build|package|repo|publish|run>")
}

func autoRepoDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return findRepoDir(filepath.Dir(exe))
}

func findRepoDir(startDir string) string {
	dir := startDir
	for range 6 {
		_, rootModuleErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, builderModuleErr := os.Stat(filepath.Join(dir, "builder", "go.mod"))
		if rootModuleErr == nil && builderModuleErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return "."
}

// ---- config ----------------------------------------------------------------

type config struct {
	force         bool
	version       string
	sourceCommit  string
	revision      int // 0 = derive from state files
	repoDir       string
	checkOnly     bool   // publish: decide and emit outputs only, skip upload and release
	preview       bool   // publish: round-trip through a throwaway prefix, never release
	previewPrefix string // publish: key prefix for preview objects
}

func (c *config) resolve() (version string, revision int, err error) {
	v := c.version
	if v == "" {
		v, err = latestGitHubVersion()
		if err != nil {
			slog.Error("resolve upstream version failed", "err", err)
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

// validatePublishFlags rejects flag combinations that would silently change the
// publish behavior, so a misconfigured CI step or local run fails loudly.
func (c *config) validatePublishFlags() error {
	if c.preview && c.checkOnly {
		return errors.New("publish: -preview and -check-only are mutually exclusive")
	}
	if c.previewPrefix != "" && !c.preview {
		return errors.New("publish: -preview-prefix requires -preview")
	}
	return nil
}

// ---- commands --------------------------------------------------------------

func cmdCheck(cfg *config) error {
	latest, err := latestGitHubVersion()
	if err != nil {
		return err
	}
	last := readState()
	logf(fmt.Sprintf("latest=%s last_built=%s", latest, emptyOr(last, "none")))
	if latest == last && !cfg.force {
		logf("already up-to-date")
		return errUpToDate
	}
	logf("build needed")
	return nil
}

func cmdBuild(cfg *config) error {
	v, _, err := cfg.resolve()
	if err != nil {
		return err
	}
	if err := validateGitCommit(cfg.sourceCommit, "source commit"); err != nil {
		slog.Error("validate build source commit failed", "err", err)
		return fmt.Errorf("build: %w", err)
	}
	return buildCloudflared(v, cfg.sourceCommit, cfg.repoDir)
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
	if err := cfg.validatePublishFlags(); err != nil {
		return err
	}

	v, rev, err := cfg.resolve()
	if err != nil {
		return err
	}
	if cfg.preview {
		if err := verifyUpstreamRelease(v, cfg.sourceCommit); err != nil {
			slog.Error("preview publish provenance check failed", "err", err)
			return fmt.Errorf("publish provenance check: %w", err)
		}
		return previewPublish(v, rev, cfg)
	}
	if err := verifyUpstreamRelease(v, cfg.sourceCommit); err != nil {
		slog.Error("publish provenance check failed", "err", err)
		return fmt.Errorf("publish provenance check: %w", err)
	}

	decision, err := decidePublish(v, rev, cfg.repoDir)
	if err != nil {
		return err
	}

	writeGitHubOutput("should_publish", strconv.FormatBool(decision.shouldPublish))
	writeGitHubOutput("publish_reason", decision.reason)
	writeGitHubOutput("target_tag", v+"-freebsd-r"+strconv.Itoa(rev))
	writeGitHubOutput("latest_tag", decision.latestTag)
	logf(fmt.Sprintf("publish decision: %v (%s)", decision.shouldPublish, decision.reason))

	if cfg.checkOnly {
		return nil
	}
	if !decision.shouldPublish {
		logf("skipping publish: " + decision.reason)
		return nil
	}

	pkgVersion := v + "_" + strconv.Itoa(rev)
	pkgFiles := []string{
		filepath.Join(pkgRepoDir, "All", "cloudflared-"+v+".pkg"),
		filepath.Join(pkgRepoDir, "All", pluginName+"-"+pkgVersion+".pkg"),
	}
	// Repository metadata (meta.conf, packagesite.*) is generated by the repo
	// step into pkgRepoDir itself, alongside All/.
	if err := verifyUpstreamRelease(v, cfg.sourceCommit); err != nil {
		slog.Error("pre-upload provenance check failed", "err", err)
		return fmt.Errorf("publish provenance recheck: %w", err)
	}
	if err := uploadToR2(pkgFiles, pkgRepoDir); err != nil {
		return err
	}
	if err := createGitHubRelease(v, rev, cfg.sourceCommit, cfg.repoDir); err != nil {
		return err
	}
	saveState(v, rev)
	return nil
}

func cmdRun(cfg *config) error {
	release, err := resolveUpstreamRelease(cfg.version)
	if err != nil {
		return err
	}
	v := release.Version
	sourceCommit := release.Commit

	last := readState()
	if v == last && !cfg.force {
		logf(fmt.Sprintf("already at latest version %s, nothing to do (use -force to rebuild)", v))
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
		slog.Error("create repo output dir failed", "err", err)
		return fmt.Errorf("mkdir repo output: %w", err)
	}

	logf(fmt.Sprintf("building cloudflared %s revision %d", v, rev))

	if err := buildCloudflared(v, sourceCommit, cfg.repoDir); err != nil {
		return err
	}
	if err := createBinaryPackage(v, cfg.repoDir); err != nil {
		return err
	}
	if err := createPluginPackage(v, rev, cfg.repoDir); err != nil {
		return err
	}
	if err := verifyUpstreamRelease(v, sourceCommit); err != nil {
		return fmt.Errorf("publish provenance check: %w", err)
	}
	if err := createGitHubRelease(v, rev, sourceCommit, cfg.repoDir); err != nil {
		logf(fmt.Sprintf("WARNING: GitHub release failed: %v (continuing)", err))
	}
	if err := updatePkgRepository(v, rev); err != nil {
		return err
	}
	if err := publishRepositoryMetadata(cfg.repoDir); err != nil {
		return err
	}
	saveState(v, rev)
	logf(fmt.Sprintf("build and release complete (%s_%d)", v, rev))
	return nil
}

func normalizedSourceCommit(commit string) string {
	return strings.ToLower(commit)
}
