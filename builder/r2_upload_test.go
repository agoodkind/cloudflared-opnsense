package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/r2"
)

type recordedR2Upload struct {
	bucketName string
	objectKey  string
	body       string
	accountID  string
}

type recordingR2Uploader struct {
	uploads []recordedR2Upload
}

func (u *recordingR2Uploader) Upload(
	_ context.Context,
	bucketName string,
	objectKey string,
	body io.Reader,
	params r2.BucketObjectUploadParams,
	_ ...option.RequestOption,
) (*r2.BucketObjectUploadResponse, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	u.uploads = append(u.uploads, recordedR2Upload{
		bucketName: bucketName,
		objectKey:  objectKey,
		body:       string(data),
		accountID:  params.AccountID.Value,
	})
	return &r2.BucketObjectUploadResponse{}, nil
}

// recordingR2Client records uploads, verification reads, and deletes so the
// preview round-trip can be asserted without a live Cloudflare account.
type recordingR2Client struct {
	recordingR2Uploader
	gets      []string
	deletes   []string
	getStatus int
}

func (c *recordingR2Client) Get(
	_ context.Context,
	_ string,
	objectKey string,
	_ r2.BucketObjectGetParams,
	_ ...option.RequestOption,
) (*http.Response, error) {
	c.gets = append(c.gets, objectKey)
	status := c.getStatus
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func (c *recordingR2Client) Delete(
	_ context.Context,
	_ string,
	objectKey string,
	_ r2.BucketObjectDeleteParams,
	_ ...option.RequestOption,
) (*r2.BucketObjectDeleteResponse, error) {
	c.deletes = append(c.deletes, objectKey)
	return &r2.BucketObjectDeleteResponse{}, nil
}

// fakeR2Pruner records deletes and lets a test list a fixed set of keys, force a
// list error, or fail specific deletes.
type fakeR2Pruner struct {
	keys    []string
	listErr error
	failOn  map[string]bool
	deleted []string
}

func (f *fakeR2Pruner) listObjectKeys(_ context.Context, _, _, _ string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys, nil
}

func (f *fakeR2Pruner) deleteObject(_ context.Context, _, _, objectKey string) error {
	if f.failOn[objectKey] {
		return fmt.Errorf("delete failed for %s", objectKey)
	}
	f.deleted = append(f.deleted, objectKey)
	return nil
}

func TestPruneStaleR2ObjectsDeletesOnlyStale(t *testing.T) {
	t.Parallel()

	pruner := &fakeR2Pruner{
		keys: []string{
			"All/cloudflared-2026.7.2.pkg",
			"All/os-cloudflared-2026.7.2_5.pkg",
			"All/cloudflared-2026.6.0.pkg",
			"All/os-cloudflared-2026.6.0_1.pkg",
			// Foreign objects under the prefix must never be deleted.
			"All/README.txt",                       // not a .pkg
			"All/some-other-tool-1.0.pkg",          // different prefix
			"All/cloudflared-archive/2026.7.2.pkg", // nested path (contains a slash)
		},
	}
	keep := map[string]struct{}{
		"All/cloudflared-2026.7.2.pkg":      {},
		"All/os-cloudflared-2026.7.2_5.pkg": {},
	}

	if err := pruneStaleR2Objects(context.Background(), pruner, "account123", "bucket-name", keep); err != nil {
		t.Fatalf("pruneStaleR2Objects: %v", err)
	}

	want := []string{
		"All/cloudflared-2026.6.0.pkg",
		"All/os-cloudflared-2026.6.0_1.pkg",
	}
	if len(pruner.deleted) != len(want) {
		t.Fatalf("deleted = %#v, want %#v", pruner.deleted, want)
	}
	for i := range want {
		if pruner.deleted[i] != want[i] {
			t.Fatalf("deleted[%d] = %q, want %q", i, pruner.deleted[i], want[i])
		}
	}
}

func TestPruneStaleR2ObjectsAggregatesDeleteErrors(t *testing.T) {
	t.Parallel()

	pruner := &fakeR2Pruner{
		keys: []string{
			"All/cloudflared-2026.7.2.pkg",
			"All/os-cloudflared-2026.6.0_1.pkg",
			"All/cloudflared-2026.6.0.pkg",
		},
		failOn: map[string]bool{"All/os-cloudflared-2026.6.0_1.pkg": true},
	}
	keep := map[string]struct{}{
		"All/cloudflared-2026.7.2.pkg":      {},
		"All/os-cloudflared-2026.7.2_5.pkg": {},
	}

	err := pruneStaleR2Objects(context.Background(), pruner, "account123", "bucket-name", keep)
	if err == nil {
		t.Fatal("pruneStaleR2Objects succeeded despite a failed delete")
	}
	if !strings.Contains(err.Error(), "All/os-cloudflared-2026.6.0_1.pkg") {
		t.Fatalf("error = %v, want it to name the failed key", err)
	}
	if len(pruner.deleted) != 1 || pruner.deleted[0] != "All/cloudflared-2026.6.0.pkg" {
		t.Fatalf("deleted = %#v, want the other stale object still removed", pruner.deleted)
	}
}

func TestPruneStaleR2ObjectsSkipsWhenKeepHasNoManagedPackage(t *testing.T) {
	t.Parallel()

	pruner := &fakeR2Pruner{
		keys: []string{
			"All/cloudflared-2026.6.0.pkg",
			"All/os-cloudflared-2026.6.0_1.pkg",
		},
	}
	// keep contains only metadata keys, no managed package: the upload list was
	// malformed, so prune must not delete the live packages.
	keep := map[string]struct{}{"packagesite.pkg": {}, "meta.conf": {}}

	if err := pruneStaleR2Objects(context.Background(), pruner, "account123", "bucket-name", keep); err != nil {
		t.Fatalf("pruneStaleR2Objects: %v", err)
	}
	if len(pruner.deleted) != 0 {
		t.Fatalf("deleted = %#v, want no deletes when keep names no managed package", pruner.deleted)
	}
}

func TestPruneStaleR2ObjectsSkipsWhenKeepMissingOneFamily(t *testing.T) {
	t.Parallel()

	pruner := &fakeR2Pruner{
		keys: []string{
			"All/cloudflared-2026.6.0.pkg",
			"All/os-cloudflared-2026.6.0_1.pkg",
		},
	}
	// keep names the binary package but no plugin package, so the upload list is
	// incomplete: prune must not run.
	keep := map[string]struct{}{"All/cloudflared-2026.7.2.pkg": {}}

	if err := pruneStaleR2Objects(context.Background(), pruner, "account123", "bucket-name", keep); err != nil {
		t.Fatalf("pruneStaleR2Objects: %v", err)
	}
	if len(pruner.deleted) != 0 {
		t.Fatalf("deleted = %#v, want no deletes when a package family is missing", pruner.deleted)
	}
}

func TestPackageFamilyForKey(t *testing.T) {
	t.Parallel()

	binaries := []string{
		"All/cloudflared-2026.7.2.pkg",
		"All/cloudflared-2026.7.2-beta1.pkg",
	}
	for _, key := range binaries {
		if got := packageFamilyForKey(key); got != familyBinary {
			t.Errorf("packageFamilyForKey(%q) = %v, want familyBinary", key, got)
		}
	}

	plugins := []string{
		"All/os-cloudflared-2026.7.2_5.pkg",
		"All/os-cloudflared-2026.7.2_10.pkg",
	}
	for _, key := range plugins {
		if got := packageFamilyForKey(key); got != familyPlugin {
			t.Errorf("packageFamilyForKey(%q) = %v, want familyPlugin", key, got)
		}
	}

	// Not this repo's packages: wrong prefix, not a .pkg, a nested path, or
	// missing the All/ prefix entirely.
	foreign := []string{
		"All/README.txt",
		"All/some-other-tool-1.0.pkg",
		"All/cloudflared-archive/2026.7.2.pkg",
		"cloudflared-2026.7.2.pkg",
		"All/sub/cloudflared-2026.7.2.pkg",
	}
	for _, key := range foreign {
		if got := packageFamilyForKey(key); got != familyNone {
			t.Errorf("packageFamilyForKey(%q) = %v, want familyNone", key, got)
		}
		if isManagedPackageKey(key) {
			t.Errorf("isManagedPackageKey(%q) = true, want false", key)
		}
	}
}

func TestPruneStaleR2ObjectsReturnsListError(t *testing.T) {
	t.Parallel()

	pruner := &fakeR2Pruner{listErr: errors.New("list boom")}
	keep := map[string]struct{}{
		"All/cloudflared-2026.7.2.pkg":      {},
		"All/os-cloudflared-2026.7.2_5.pkg": {},
	}
	err := pruneStaleR2Objects(context.Background(), pruner, "account123", "bucket-name", keep)
	if err == nil {
		t.Fatal("pruneStaleR2Objects succeeded despite a list error")
	}
	if len(pruner.deleted) != 0 {
		t.Fatalf("deleted = %#v, want no deletes when listing fails", pruner.deleted)
	}
}

func TestPrefixedUploadsPrependsPrefix(t *testing.T) {
	t.Parallel()

	uploads := []r2Upload{
		{sourcePath: "/tmp/a.pkg", objectKey: "All/a.pkg"},
		{sourcePath: "/tmp/meta.conf", objectKey: "meta.conf"},
	}

	got := prefixedUploads(uploads, "/previews/pr-12/")
	want := []r2Upload{
		{sourcePath: "/tmp/a.pkg", objectKey: "previews/pr-12/All/a.pkg"},
		{sourcePath: "/tmp/meta.conf", objectKey: "previews/pr-12/meta.conf"},
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("prefixedUploads[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}

	if same := prefixedUploads(uploads, ""); same[0] != uploads[0] {
		t.Fatalf("empty prefix changed uploads: %#v", same)
	}
}

func TestRoundTripR2ObjectsUploadsVerifiesDeletes(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "pkg")
	if err := os.WriteFile(src, []byte("contents"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	client := &recordingR2Client{}
	key := "previews/pr-1/All/pkg"
	err := roundTripR2Objects(context.Background(), client, "account123", "bucket-name", []r2Upload{
		{sourcePath: src, objectKey: key},
	})
	if err != nil {
		t.Fatalf("roundTripR2Objects: %v", err)
	}

	if len(client.uploads) != 1 || client.uploads[0].objectKey != key {
		t.Fatalf("uploads = %#v, want one upload of %q", client.uploads, key)
	}
	if len(client.gets) != 1 || client.gets[0] != key {
		t.Fatalf("gets = %#v, want [%q]", client.gets, key)
	}
	if len(client.deletes) != 1 || client.deletes[0] != key {
		t.Fatalf("deletes = %#v, want [%q]", client.deletes, key)
	}
}

func TestRoundTripR2ObjectsDeletesAfterVerifyFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "pkg")
	if err := os.WriteFile(src, []byte("contents"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	client := &recordingR2Client{getStatus: http.StatusNotFound}
	key := "previews/pr-1/All/pkg"
	err := roundTripR2Objects(context.Background(), client, "account123", "bucket-name", []r2Upload{
		{sourcePath: src, objectKey: key},
	})
	if err == nil {
		t.Fatal("roundTripR2Objects succeeded despite a failed verification")
	}
	if len(client.deletes) != 1 || client.deletes[0] != key {
		t.Fatalf("deletes = %#v, want the uploaded object cleaned up", client.deletes)
	}
}

func TestR2UploadsMapsPackagesAndMetadata(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	metadataDir := filepath.Join(tempDir, "metadata")
	if err := os.MkdirAll(metadataDir, 0o755); err != nil {
		t.Fatalf("mkdir metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "meta.conf"), []byte("meta"), 0o644); err != nil {
		t.Fatalf("write meta.conf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "meta.pkg"), []byte("meta-pkg"), 0o644); err != nil {
		t.Fatalf("write meta.pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "meta.txz"), []byte("meta-txz"), 0o644); err != nil {
		t.Fatalf("write meta.txz: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "packagesite.pkg"), []byte("site-pkg"), 0o644); err != nil {
		t.Fatalf("write packagesite.pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "packagesite.txz"), []byte("site-txz"), 0o644); err != nil {
		t.Fatalf("write packagesite.txz: %v", err)
	}

	uploads, err := r2Uploads(
		[]string{
			filepath.Join(tempDir, "cloudflared-2026.6.0.pkg"),
			filepath.Join(tempDir, "os-cloudflared-2026.6.0_1.pkg"),
		},
		metadataDir,
	)
	if err != nil {
		t.Fatalf("r2Uploads: %v", err)
	}

	want := []r2Upload{
		{sourcePath: filepath.Join(tempDir, "cloudflared-2026.6.0.pkg"), objectKey: "All/cloudflared-2026.6.0.pkg"},
		{sourcePath: filepath.Join(tempDir, "os-cloudflared-2026.6.0_1.pkg"), objectKey: "All/os-cloudflared-2026.6.0_1.pkg"},
		{sourcePath: filepath.Join(metadataDir, "meta.conf"), objectKey: "meta.conf"},
		{sourcePath: filepath.Join(metadataDir, "meta.pkg"), objectKey: "meta.pkg"},
		{sourcePath: filepath.Join(metadataDir, "meta.txz"), objectKey: "meta.txz"},
		{sourcePath: filepath.Join(metadataDir, "packagesite.pkg"), objectKey: "packagesite.pkg"},
		{sourcePath: filepath.Join(metadataDir, "packagesite.txz"), objectKey: "packagesite.txz"},
	}
	if len(uploads) != len(want) {
		t.Fatalf("len(uploads) = %d, want %d: %#v", len(uploads), len(want), uploads)
	}
	for i := range uploads {
		if uploads[i] != want[i] {
			t.Fatalf("uploads[%d] = %#v, want %#v", i, uploads[i], want[i])
		}
	}
}

func TestUploadR2ObjectsUsesCloudflareParams(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "meta")
	if err := os.WriteFile(src, []byte("metadata"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	uploader := &recordingR2Uploader{}
	err := uploadR2Objects(context.Background(), uploader, "account123", "bucket-name", []r2Upload{
		{sourcePath: src, objectKey: "meta"},
	})
	if err != nil {
		t.Fatalf("uploadR2Objects: %v", err)
	}

	want := []recordedR2Upload{{
		bucketName: "bucket-name",
		objectKey:  "meta",
		body:       "metadata",
		accountID:  "account123",
	}}
	if len(uploader.uploads) != len(want) {
		t.Fatalf("len(uploads) = %d, want %d", len(uploader.uploads), len(want))
	}
	if uploader.uploads[0] != want[0] {
		t.Fatalf("upload = %#v, want %#v", uploader.uploads[0], want[0])
	}
}

func TestUploadR2ObjectRejectsOversizedControlPlaneObject(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "too-large.pkg")
	file, err := os.Create(src)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := file.Truncate(maxR2ControlPlaneUploadBytes + 1); err != nil {
		t.Fatalf("truncate source: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	uploader := &recordingR2Uploader{}
	err = uploadR2Object(context.Background(), uploader, "account123", "bucket-name", r2Upload{
		sourcePath: src,
		objectKey:  "All/too-large.pkg",
	})
	if err == nil {
		t.Fatal("uploadR2Object succeeded for an oversized object")
	}
	if len(uploader.uploads) != 0 {
		t.Fatalf("len(uploads) = %d, want 0", len(uploader.uploads))
	}
}

func TestRequiredCloudflareCredentials(t *testing.T) {
	t.Setenv(cloudflareAccountIDEnv, "account123")
	t.Setenv(cloudflareAPITokenEnv, "token")

	accountID, err := requiredR2AccountID()
	if err != nil {
		t.Fatalf("requiredR2AccountID: %v", err)
	}
	if accountID != "account123" {
		t.Fatalf("accountID = %q, want account123", accountID)
	}

	token, err := requiredCloudflareAPIToken()
	if err != nil {
		t.Fatalf("requiredCloudflareAPIToken: %v", err)
	}
	if token != "token" {
		t.Fatalf("token = %q, want token", token)
	}
}

func TestRequiredCloudflareCredentialsRejectMissingValues(t *testing.T) {
	t.Setenv(cloudflareAccountIDEnv, "")
	t.Setenv(cloudflareAPITokenEnv, "")

	if _, err := requiredR2AccountID(); err == nil {
		t.Fatal("requiredR2AccountID succeeded with empty account id")
	}
	if _, err := requiredCloudflareAPIToken(); err == nil {
		t.Fatal("requiredCloudflareAPIToken succeeded with empty token")
	}
}

func TestCloudflareSDKR2UploadRequestShape(t *testing.T) {
	t.Parallel()

	var requestBody bytes.Buffer
	server := newCloudflareR2UploadServer(t, &requestBody)
	client := cloudflare.NewClient(
		option.WithBaseURL(server.URL),
		option.WithAPIToken("token"),
	)

	_, err := client.R2.Buckets.Objects.Upload(
		context.Background(),
		"bucket-name",
		"All/example.pkg",
		bytes.NewBufferString("pkg"),
		r2.BucketObjectUploadParams{
			AccountID: cloudflare.F("account123"),
		},
	)
	if err != nil {
		t.Fatalf("SDK upload: %v", err)
	}
	if requestBody.String() != "pkg" {
		t.Fatalf("request body = %q, want pkg", requestBody.String())
	}
}

func newCloudflareR2UploadServer(t *testing.T, requestBody *bytes.Buffer) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		wantPath := "/accounts/account123/r2/buckets/bucket-name/objects/All/example.pkg"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("Authorization = %q, want Bearer token", r.Header.Get("Authorization"))
			http.Error(w, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		if _, err := io.Copy(requestBody, r.Body); err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "read body failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"result": {
				"etag": "etag",
				"key": "All/example.pkg",
				"size": "3",
				"storage_class": "Standard",
				"uploaded": "2026-01-01T00:00:00Z",
				"version": "00000000-0000-0000-0000-000000000000"
			},
			"success": true,
			"errors": [],
			"messages": []
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}
