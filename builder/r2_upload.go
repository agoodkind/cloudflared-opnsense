package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/r2"
)

const (
	cloudflareAccountIDEnv = "CF_ACCOUNT_ID"
	cloudflareAPITokenEnv  = "CLOUDFLARE_API_TOKEN" // #nosec G101 -- environment variable name, not a token value.

	r2UploadTimeout = 5 * time.Minute

	// The R2 control-plane object upload endpoint rejects objects over 300 MB.
	maxR2ControlPlaneUploadBytes = 300 << 20
)

var r2AccountIDPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

type r2Upload struct {
	sourcePath string
	objectKey  string
}

type r2ObjectUploader interface {
	Upload(
		ctx context.Context,
		bucketName string,
		objectKey string,
		body io.Reader,
		params r2.BucketObjectUploadParams,
		opts ...option.RequestOption,
	) (*r2.BucketObjectUploadResponse, error)
}

// uploadToR2 mirrors package files and repository metadata through the
// Cloudflare SDK. CF_ACCOUNT_ID and CLOUDFLARE_API_TOKEN come from the
// environment.
func uploadToR2(pkgFiles []string, metadataDir string) error {
	accountID, err := requiredR2AccountID()
	if err != nil {
		return err
	}
	token, err := requiredCloudflareAPIToken()
	if err != nil {
		return err
	}

	client := cloudflare.NewClient(option.WithAPIToken(token))
	uploads, err := r2Uploads(pkgFiles, metadataDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r2UploadTimeout)
	defer cancel()

	return uploadR2Objects(ctx, client.R2.Buckets.Objects, accountID, r2Bucket, uploads)
}

func requiredR2AccountID() (string, error) {
	accountID := os.Getenv(cloudflareAccountIDEnv)
	if !r2AccountIDPattern.MatchString(accountID) {
		invalid := fmt.Errorf("%s is missing or malformed", cloudflareAccountIDEnv)
		slog.Error("invalid R2 account id", "err", invalid)
		return "", invalid
	}
	return accountID, nil
}

func requiredCloudflareAPIToken() (string, error) {
	token := os.Getenv(cloudflareAPITokenEnv)
	if token == "" {
		err := fmt.Errorf("%s is required for Cloudflare SDK R2 uploads", cloudflareAPITokenEnv)
		slog.Error("missing Cloudflare API token", "err", err)
		return "", err
	}
	return token, nil
}

func r2Uploads(pkgFiles []string, metadataDir string) ([]r2Upload, error) {
	metadataPaths, err := repoMetadataPaths(metadataDir)
	if err != nil {
		return nil, err
	}

	uploads := make([]r2Upload, 0, len(pkgFiles)+len(metadataPaths))
	for _, src := range pkgFiles {
		uploads = append(uploads, r2Upload{
			sourcePath: src,
			objectKey:  "All/" + filepath.Base(src),
		})
	}
	for _, src := range metadataPaths {
		uploads = append(uploads, r2Upload{
			sourcePath: src,
			objectKey:  filepath.Base(src),
		})
	}
	return uploads, nil
}

func uploadR2Objects(
	ctx context.Context,
	uploader r2ObjectUploader,
	accountID string,
	bucketName string,
	uploads []r2Upload,
) error {
	for _, upload := range uploads {
		if err := uploadR2Object(ctx, uploader, accountID, bucketName, upload); err != nil {
			return err
		}
	}
	logf("R2 upload complete")
	return nil
}

func uploadR2Object(
	ctx context.Context,
	uploader r2ObjectUploader,
	accountID string,
	bucketName string,
	upload r2Upload,
) error {
	file, err := os.Open(upload.sourcePath)
	if err != nil {
		slog.ErrorContext(ctx, "open R2 upload source failed", "err", err, "path", upload.sourcePath)
		return fmt.Errorf("open %s: %w", upload.sourcePath, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		slog.ErrorContext(ctx, "stat R2 upload source failed", "err", err, "path", upload.sourcePath)
		return fmt.Errorf("stat %s: %w", upload.sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("upload %s: source is a directory", upload.sourcePath)
	}
	if info.Size() > maxR2ControlPlaneUploadBytes {
		return fmt.Errorf(
			"upload %s: object is %d bytes, above Cloudflare R2 control-plane limit %d",
			upload.sourcePath,
			info.Size(),
			maxR2ControlPlaneUploadBytes,
		)
	}

	_, err = uploader.Upload(ctx, bucketName, upload.objectKey, file, r2.BucketObjectUploadParams{
		AccountID: cloudflare.F(accountID),
	})
	if err != nil {
		return fmt.Errorf("upload %s to R2 key %s: %w", upload.sourcePath, upload.objectKey, err)
	}
	return nil
}

// ---- preview round-trip ----------------------------------------------------

type r2ObjectGetter interface {
	Get(
		ctx context.Context,
		bucketName string,
		objectKey string,
		params r2.BucketObjectGetParams,
		opts ...option.RequestOption,
	) (*http.Response, error)
}

type r2ObjectDeleter interface {
	Delete(
		ctx context.Context,
		bucketName string,
		objectKey string,
		params r2.BucketObjectDeleteParams,
		opts ...option.RequestOption,
	) (*r2.BucketObjectDeleteResponse, error)
}

// r2ObjectRoundTripper is the subset of the Cloudflare R2 object API needed to
// upload, verify, and delete preview objects.
type r2ObjectRoundTripper interface {
	r2ObjectUploader
	r2ObjectGetter
	r2ObjectDeleter
}

// previewPublishToR2 uploads each package and metadata file under a throwaway
// key prefix, verifies it is readable, then deletes it. It exercises the same
// credentials and upload path as a real publish without creating a release or
// leaving objects behind, so a pull request can confirm publishing still works.
func previewPublishToR2(pkgFiles []string, metadataDir, keyPrefix string) error {
	accountID, err := requiredR2AccountID()
	if err != nil {
		return err
	}
	token, err := requiredCloudflareAPIToken()
	if err != nil {
		return err
	}

	client := cloudflare.NewClient(option.WithAPIToken(token))
	uploads, err := r2Uploads(pkgFiles, metadataDir)
	if err != nil {
		return err
	}
	uploads = prefixedUploads(uploads, keyPrefix)

	ctx, cancel := context.WithTimeout(context.Background(), r2UploadTimeout)
	defer cancel()

	return roundTripR2Objects(ctx, client.R2.Buckets.Objects, accountID, r2Bucket, uploads)
}

// prefixedUploads returns copies of uploads with keyPrefix prepended to each
// object key, so preview objects never collide with the production layout.
func prefixedUploads(uploads []r2Upload, keyPrefix string) []r2Upload {
	trimmed := strings.Trim(keyPrefix, "/")
	if trimmed == "" {
		return uploads
	}
	prefixed := make([]r2Upload, len(uploads))
	for i, upload := range uploads {
		prefixed[i] = r2Upload{
			sourcePath: upload.sourcePath,
			objectKey:  trimmed + "/" + upload.objectKey,
		}
	}
	return prefixed
}

// roundTripR2Objects uploads and verifies each object, then deletes every
// uploaded key in a deferred cleanup that runs on return, including the partial
// set when a later step fails. Delete failures are logged rather than returned,
// so a preview never intentionally leaves objects in the bucket.
func roundTripR2Objects(
	ctx context.Context,
	client r2ObjectRoundTripper,
	accountID string,
	bucketName string,
	uploads []r2Upload,
) error {
	uploadedKeys := make([]string, 0, len(uploads))
	defer func() {
		if len(uploadedKeys) == 0 {
			return
		}
		// Detach from the caller's cancellation for cleanup: ctx may already be
		// canceled or timed out (a likely failure mode), and reusing it would
		// make the deletes fail immediately and leave preview objects behind.
		// WithoutCancel keeps context values while dropping that cancellation.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r2UploadTimeout)
		defer cancel()
		for _, key := range uploadedKeys {
			_, err := client.Delete(cleanupCtx, bucketName, key, r2.BucketObjectDeleteParams{
				AccountID: cloudflare.F(accountID),
			})
			if err != nil {
				slog.WarnContext(cleanupCtx, "preview cleanup delete failed", "err", err, "key", key)
			}
		}
	}()

	for _, upload := range uploads {
		if err := uploadR2Object(ctx, client, accountID, bucketName, upload); err != nil {
			return err
		}
		uploadedKeys = append(uploadedKeys, upload.objectKey)
		if err := verifyR2Object(ctx, client, accountID, bucketName, upload.objectKey); err != nil {
			return err
		}
	}

	logf(fmt.Sprintf(
		"R2 preview round-trip: %d objects uploaded and verified; deleting on return",
		len(uploads),
	))
	return nil
}

// verifyR2Object confirms a freshly uploaded preview object is readable.
func verifyR2Object(
	ctx context.Context,
	getter r2ObjectGetter,
	accountID string,
	bucketName string,
	objectKey string,
) error {
	resp, err := getter.Get(ctx, bucketName, objectKey, r2.BucketObjectGetParams{
		AccountID: cloudflare.F(accountID),
	})
	if err != nil {
		slog.ErrorContext(ctx, "read back preview object failed", "err", err, "key", objectKey)
		return fmt.Errorf("verify preview object %s: %w", objectKey, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("verify preview object %s: status %d", objectKey, resp.StatusCode)
		slog.ErrorContext(ctx, "preview object read back non-OK status", "err", statusErr, "key", objectKey)
		return statusErr
	}
	return nil
}
