package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	cloudflaredRepoSlug      = "cloudflare/cloudflared"
	cloudflaredReleaseAuthor = "cloudflare-warp-bot"
)

var (
	githubAPIBaseURL = "https://api.github.com"
	githubHTTPClient = &http.Client{Timeout: 15 * time.Second}
	gitCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
}

type githubGitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type githubGitRef struct {
	Object githubGitObject `json:"object"`
}

type githubAnnotatedTag struct {
	Tagger struct {
		Email string `json:"email"`
	} `json:"tagger"`
	Object githubGitObject `json:"object"`
}

type githubCompare struct {
	Status string `json:"status"`
}

type upstreamRelease struct {
	Version string
	Commit  string
}

func resolveUpstreamRelease(version string) (upstreamRelease, error) {
	releasePath := "/repos/" + cloudflaredRepoSlug + "/releases/latest"
	if version != "" {
		if !cloudflaredVersionPattern.MatchString(version) {
			return upstreamRelease{}, fmt.Errorf("unsafe cloudflared version %q", version)
		}
		releasePath = "/repos/" + cloudflaredRepoSlug + "/releases/tags/" + url.PathEscape(version)
	}

	var release githubRelease
	releaseData, err := readGitHubAPI(releasePath)
	if err != nil {
		slog.Error("resolve cloudflared release failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("resolve cloudflared release: %w", err)
	}
	if err := json.Unmarshal(releaseData, &release); err != nil {
		slog.Error("decode cloudflared release failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("decode cloudflared release: %w", err)
	}
	if release.TagName == "" {
		return upstreamRelease{}, errors.New("cloudflared release has an empty tag_name")
	}
	if version != "" && release.TagName != version {
		return upstreamRelease{}, fmt.Errorf("cloudflared release tag is %q, want %q", release.TagName, version)
	}
	if release.Author.Login != cloudflaredReleaseAuthor {
		return upstreamRelease{}, fmt.Errorf(
			"cloudflared release author is %q, want %q",
			release.Author.Login,
			cloudflaredReleaseAuthor,
		)
	}

	var ref githubGitRef
	refPath := "/repos/" + cloudflaredRepoSlug + "/git/ref/tags/" + url.PathEscape(release.TagName)
	refData, err := readGitHubAPI(refPath)
	if err != nil {
		slog.Error("resolve cloudflared tag ref failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("resolve cloudflared tag ref: %w", err)
	}
	if err := json.Unmarshal(refData, &ref); err != nil {
		slog.Error("decode cloudflared tag ref failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("decode cloudflared tag ref: %w", err)
	}
	if ref.Object.Type != "tag" {
		return upstreamRelease{}, fmt.Errorf("cloudflared tag %q is %q, want annotated tag", release.TagName, ref.Object.Type)
	}
	if err := validateGitCommit(ref.Object.SHA, "tag object"); err != nil {
		return upstreamRelease{}, err
	}

	var tag githubAnnotatedTag
	tagPath := "/repos/" + cloudflaredRepoSlug + "/git/tags/" + ref.Object.SHA
	tagData, err := readGitHubAPI(tagPath)
	if err != nil {
		slog.Error("resolve annotated cloudflared tag failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("resolve annotated cloudflared tag: %w", err)
	}
	if err := json.Unmarshal(tagData, &tag); err != nil {
		slog.Error("decode annotated cloudflared tag failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("decode annotated cloudflared tag: %w", err)
	}
	if !strings.HasSuffix(strings.ToLower(tag.Tagger.Email), "@cloudflare.com") {
		return upstreamRelease{}, fmt.Errorf("cloudflared tagger email %q is not a cloudflare.com address", tag.Tagger.Email)
	}
	if tag.Object.Type != "commit" {
		return upstreamRelease{}, fmt.Errorf("cloudflared tag target is %q, want commit", tag.Object.Type)
	}
	if err := validateGitCommit(tag.Object.SHA, "release commit"); err != nil {
		return upstreamRelease{}, err
	}

	commit := strings.ToLower(tag.Object.SHA)
	var comparison githubCompare
	comparePath := "/repos/" + cloudflaredRepoSlug + "/compare/" + commit + "...master"
	compareData, err := readGitHubAPI(comparePath)
	if err != nil {
		slog.Error("compare cloudflared release commit failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("compare cloudflared release commit to master: %w", err)
	}
	if err := json.Unmarshal(compareData, &comparison); err != nil {
		slog.Error("decode cloudflared comparison failed", "err", err)
		return upstreamRelease{}, fmt.Errorf("decode cloudflared comparison: %w", err)
	}
	if comparison.Status != "ahead" && comparison.Status != "identical" {
		return upstreamRelease{}, fmt.Errorf(
			"cloudflared master comparison is %q, want ahead or identical",
			comparison.Status,
		)
	}

	return upstreamRelease{Version: release.TagName, Commit: commit}, nil
}

func verifyUpstreamRelease(version string, expectedCommit string) error {
	if err := validateGitCommit(expectedCommit, "source commit"); err != nil {
		return err
	}
	release, err := resolveUpstreamRelease(version)
	if err != nil {
		return err
	}
	if release.Commit != strings.ToLower(expectedCommit) {
		return fmt.Errorf(
			"cloudflared release %s now resolves to %s, want %s",
			version,
			release.Commit,
			strings.ToLower(expectedCommit),
		)
	}
	return nil
}

func validateGitCommit(commit string, name string) error {
	if !gitCommitPattern.MatchString(commit) {
		return fmt.Errorf("%s %q is not a 40-character Git commit", name, commit)
	}
	return nil
}

func readGitHubAPI(path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		strings.TrimRight(githubAPIBaseURL, "/")+path,
		nil,
	)
	if err != nil {
		slog.Error("build GitHub API request failed", "err", err, "path", path)
		return nil, fmt.Errorf("build GitHub API request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-Github-Api-Version", "2022-11-28")
	if token := os.Getenv("GH_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := githubHTTPClient.Do(request)
	if err != nil {
		slog.Error("request GitHub API failed", "err", err, "path", path)
		return nil, fmt.Errorf("request GitHub API: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for %s", response.StatusCode, path)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		slog.Error("read GitHub API response failed", "err", err, "path", path)
		return nil, fmt.Errorf("read GitHub API response for %s: %w", path, err)
	}
	return body, nil
}
