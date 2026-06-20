package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(filepath.Join(metadataDir, "packagesite.yaml"), []byte("site"), 0o644); err != nil {
		t.Fatalf("write packagesite.yaml: %v", err)
	}

	uploads := r2Uploads(
		[]string{
			filepath.Join(tempDir, "cloudflared-2026.6.0.pkg"),
			filepath.Join(tempDir, "os-cloudflared-2026.6.0_1.pkg"),
		},
		metadataDir,
	)

	want := []r2Upload{
		{sourcePath: filepath.Join(tempDir, "cloudflared-2026.6.0.pkg"), objectKey: "All/cloudflared-2026.6.0.pkg"},
		{sourcePath: filepath.Join(tempDir, "os-cloudflared-2026.6.0_1.pkg"), objectKey: "All/os-cloudflared-2026.6.0_1.pkg"},
		{sourcePath: filepath.Join(metadataDir, "meta.conf"), objectKey: "meta.conf"},
		{sourcePath: filepath.Join(metadataDir, "packagesite.yaml"), objectKey: "packagesite.yaml"},
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
