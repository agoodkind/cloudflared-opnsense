# Declarative cloudflared Patch System Implementation Plan

Execute each task in order. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace hard-coded FreeBSD source rewrites with a strict TOML patch manifest and one deterministic unified-diff engine.

**Architecture:** The builder loads ordered patch declarations from `builder/patches.toml`. Local files are read from the repository; remote refs are fetched and checked against a pinned commit. Both sources are materialized as unified diffs and pass through forward, reverse, and apply checks before `gmake` runs.

**Tech Stack:** Go 1.26.5, `github.com/pelletier/go-toml/v2`, `github.com/Masterminds/semver/v3`, Git unified diffs, FreeBSD 14.2 CI.

## Global Constraints

- Use strict TDD and preserve the observed red output for each new behavior.
- Sign every new commit and include `Co-authored-by: Codex <noreply@openai.com>`.
- TOML entries apply in declaration order and contain exactly one source: `file` or `git`.
- Mutable refs require a full expected commit SHA and fail when the ref moves.
- A remote entry represents one non-merge commit. Multi-commit changes use ordered entries.
- Skip only an exact reverse-applicable patch. Every other mismatch fails closed.
- Do not change OPNsense runtime scripts or tunnel configuration.
- Preserve both package artifacts, preview R2 verification, and skipped production publishing.

---

### Task 1: Parse and validate the patch manifest

**Files:**
- Create: `builder/patches.go`
- Create: `builder/patches_test.go`
- Modify: `builder/go.mod`
- Modify: `builder/go.sum`

**Interfaces:**
- Produce `patchManifest`, `patchSpec`, and `gitPatchSource` TOML models.
- Produce `loadPatchManifest(path string) (patchManifest, error)`.
- Produce `selectPatches(manifest patchManifest, upstreamVersion string) ([]patchSpec, error)`.

- [ ] Write failing behavior tests for strict decoding, schema version 1, unique non-empty IDs, exactly one source, safe relative file paths, non-negative strip values, semantic-version constraints, full hexadecimal expected SHAs, and valid Git refs.
- [ ] Run `go test -run 'TestLoadPatchManifest|TestSelectPatches' -count=1 ./...` and confirm failures name the missing parser or validation behavior.
- [ ] Add the two dependencies and implement strict typed decoding. Default `strip` to 1 and omitted `applies_to` to `*`.
- [ ] Re-run the focused tests and `make check`.
- [ ] Commit the task as `Add declarative patch manifest parser` with a signed commit and the required trailer.

### Task 2: Apply local unified-diff patches

**Files:**
- Modify: `builder/patches.go`
- Modify: `builder/patches_test.go`

**Interfaces:**
- Produce `patchDisposition` values `applied`, `already_applied`, and `not_applicable`.
- Produce `applyPatchFile(sourceDir string, spec patchSpec, patchPath string) (patchDisposition, error)`.
- Produce `applyPatchManifest(repoDir string, sourceDir string, upstreamVersion string) error` for local entries.

- [ ] Write failing tests that apply a real unified diff to a temporary Git checkout, skip the exact same patch on a second run, preserve declaration order, honor strip 0 and 1, and return both forward and reverse diagnostics when source drift matches neither direction.
- [ ] Run the focused tests and confirm the local application behaviors fail before implementation.
- [ ] Implement `git apply --check`, exact reverse checking, and `git apply --index` with separate argv and visible stderr. Resolve paths relative to the manifest and reject symlink or traversal escapes.
- [ ] Re-run focused tests and `make check`.
- [ ] Commit the task as `Add unified local patch application` with a signed commit and the required trailer.

### Task 3: Resolve guarded upstream refs

**Files:**
- Modify: `builder/patches.go`
- Modify: `builder/patches_test.go`

**Interfaces:**
- Produce `materializeGitPatch(sourceDir string, spec patchSpec) (string, func(), error)`.
- Extend `applyPatchManifest` to process `git` entries through the same application engine.

- [ ] Build a temporary local Git remote in tests and write failing cases for a matching mutable ref, a moved ref, a merge commit, fetch failure, and cleanup of the temporary diff.
- [ ] Run the focused tests and confirm the missing remote resolver causes the expected failures.
- [ ] Fetch each ref with depth two, compare `FETCH_HEAD` to `expected_commit`, reject merge commits, export the commit against its sole parent with `git diff --binary`, and remove the temporary diff on every return path.
- [ ] Re-run focused tests and `make check`.
- [ ] Commit the task as `Add guarded upstream patch references` with a signed commit and the required trailer.

### Task 4: Migrate FreeBSD patches and build wiring

**Files:**
- Create: `builder/patches.toml`
- Create: `builder/patches/freebsd-diagnostics.patch`
- Modify: `builder/build_pkg.go`
- Modify: `builder/build_pkg_test.go`
- Modify: `builder/main.go`

**Interfaces:**
- Change `buildCloudflared` to `buildCloudflared(version string, repoDir string) error`.
- Call `applyPatchManifest(repoDir, srcDir, version)` after clone and before build-date derivation.

- [ ] Write failing manifest-driven fixture tests that cross-build FreeBSD with local diagnostics plus the token binding, skip an already-upstream token patch, and propagate manifest, local patch, fetch, and drift errors.
- [ ] Run `go test -run 'TestApplyPatchManifest|TestBuildCloudflaredPatches' -count=1 ./...` and preserve the expected failures.
- [ ] Add the ordered manifest. Use a committed FreeBSD-native diagnostic patch derived from FreeBSD Ports, including runtime collector tests, and use PR 1707 ref `refs/pull/1707/head` pinned to `834e9d1706d8bf53b83e66af64f4e9856321c2ff` for the token binding.
- [ ] Remove `patchFreeBSD`, `patchFreeBSDTokenFile`, `sedInPlace`, Linux collector copying, source-string checks, and helpers that become unused.
- [ ] Re-run the focused tests, `make check`, and `go test -race -count=1 ./...` in both Go modules.
- [ ] Build cloudflared 2026.7.3 through the real builder and confirm the output is a FreeBSD amd64 ELF.
- [ ] Commit the task as `Migrate FreeBSD fixes to patch manifest` with a signed commit and the required trailer.

### Task 5: Validate FreeBSD diagnostics and hosted publishing

**Files:**
- Modify: `.github/workflows/build.yml`

**Interfaces:**
- After the build stage, run the patched upstream diagnostic tests inside the existing FreeBSD VM before packaging continues.

- [ ] Add a failing workflow-equivalent local check where practical, then add `go test ./diagnostic/...` against `${WORK_DIR}/cloudflared` in the FreeBSD VM job.
- [ ] Run YAML and repository checks locally without rewriting files.
- [ ] Commit the task as `Test patched diagnostics in FreeBSD CI` with a signed commit and the required trailer.
- [ ] Submit the stack with Graphite and wait for all checks.
- [ ] Verify cloudflared 2026.7.3 compiles in FreeBSD, both packages exist in the downloaded artifact, the preview publishes and verifies its R2 objects, and production `publish` is skipped.
- [ ] Verify every commit in `origin/main..HEAD` has a good signature and a raw `gpgsig` header.
