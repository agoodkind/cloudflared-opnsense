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
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
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
