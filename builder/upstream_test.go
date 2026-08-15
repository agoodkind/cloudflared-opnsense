package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testTagObjectCommit = "1111111111111111111111111111111111111111"
	testSourceCommit    = "2222222222222222222222222222222222222222"
)

type upstreamAPIFixture struct {
	authorLogin string
	refType     string
	taggerEmail string
	targetType  string
	targetSHA   string
	compare     string
}

func validUpstreamAPIFixture() upstreamAPIFixture {
	return upstreamAPIFixture{
		authorLogin: cloudflaredReleaseAuthor,
		refType:     "tag",
		taggerEmail: "release@cloudflare.com",
		targetType:  "commit",
		targetSHA:   testSourceCommit,
		compare:     "ahead",
	}
}

func useUpstreamAPIServer(t *testing.T, fixture upstreamAPIFixture) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/cloudflare/cloudflared/releases/latest",
			"/repos/cloudflare/cloudflared/releases/tags/2026.8.1":
			_, _ = fmt.Fprintf(
				writer,
				`{"tag_name":"2026.8.1","author":{"login":%q}}`,
				fixture.authorLogin,
			)
		case "/repos/cloudflare/cloudflared/git/ref/tags/2026.8.1":
			_, _ = fmt.Fprintf(
				writer,
				`{"object":{"type":%q,"sha":%q}}`,
				fixture.refType,
				testTagObjectCommit,
			)
		case "/repos/cloudflare/cloudflared/git/tags/" + testTagObjectCommit:
			_, _ = fmt.Fprintf(
				writer,
				`{"tagger":{"email":%q},"object":{"type":%q,"sha":%q}}`,
				fixture.taggerEmail,
				fixture.targetType,
				fixture.targetSHA,
			)
		case "/repos/cloudflare/cloudflared/compare/" + fixture.targetSHA + "...master":
			_, _ = fmt.Fprintf(writer, `{"status":%q}`, fixture.compare)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	previousBaseURL := githubAPIBaseURL
	previousClient := githubHTTPClient
	githubAPIBaseURL = server.URL
	githubHTTPClient = server.Client()
	t.Cleanup(func() {
		githubAPIBaseURL = previousBaseURL
		githubHTTPClient = previousClient
	})
}

func TestResolveUpstreamReleaseAcceptsVerifiedExplicitRelease(t *testing.T) {
	useUpstreamAPIServer(t, validUpstreamAPIFixture())

	release, err := resolveUpstreamRelease("2026.8.1")
	if err != nil {
		t.Fatalf("resolveUpstreamRelease() error = %v", err)
	}
	if release.Version != "2026.8.1" {
		t.Fatalf("release.Version = %q, want 2026.8.1", release.Version)
	}
	if release.Commit != testSourceCommit {
		t.Fatalf("release.Commit = %q, want %s", release.Commit, testSourceCommit)
	}
}

func TestResolveUpstreamReleaseRejectsUntrustedMetadata(t *testing.T) {
	tests := []struct {
		name        string
		modify      func(*upstreamAPIFixture)
		wantMessage string
	}{
		{
			name: "release author",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.authorLogin = "attacker"
			},
			wantMessage: "release author",
		},
		{
			name: "lightweight tag",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.refType = "commit"
			},
			wantMessage: "annotated tag",
		},
		{
			name: "tagger email",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.taggerEmail = "attacker@example.com"
			},
			wantMessage: "tagger email",
		},
		{
			name: "tag target",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.targetType = "tree"
			},
			wantMessage: "tag target",
		},
		{
			name: "unrelated commit",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.compare = "diverged"
			},
			wantMessage: "ahead or identical",
		},
		{
			name: "malformed commit",
			modify: func(fixture *upstreamAPIFixture) {
				fixture.targetSHA = "short"
			},
			wantMessage: "40-character Git commit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := validUpstreamAPIFixture()
			test.modify(&fixture)
			useUpstreamAPIServer(t, fixture)

			_, err := resolveUpstreamRelease("2026.8.1")
			if err == nil {
				t.Fatal("resolveUpstreamRelease() error = nil")
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("resolveUpstreamRelease() error = %q, want %q", err, test.wantMessage)
			}
		})
	}
}

func TestPublishRechecksReleaseBeforeUpload(t *testing.T) {
	fixture := validUpstreamAPIFixture()
	fixture.targetSHA = "3333333333333333333333333333333333333333"
	useUpstreamAPIServer(t, fixture)
	t.Setenv(cloudflareAccountIDEnv, "")
	t.Setenv(cloudflareAPITokenEnv, "")

	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			cfg := &config{
				version:      "2026.8.1",
				revision:     1,
				sourceCommit: testSourceCommit,
				preview:      preview,
			}
			err := cmdPublish(cfg)
			if err == nil {
				t.Fatal("cmdPublish() error = nil")
			}
			if !strings.Contains(err.Error(), "now resolves") {
				t.Fatalf("cmdPublish() error = %q, want provenance mismatch", err)
			}
			if strings.Contains(err.Error(), cloudflareAccountIDEnv) {
				t.Fatalf("cmdPublish() reached R2 before provenance check: %v", err)
			}
		})
	}
}
