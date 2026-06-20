package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
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

var pkgMetadataFiles = []string{"meta.conf", "meta", "packagesite.yaml", "packagesite.pkg", "data.pkg"}

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
	uploads := r2Uploads(pkgFiles, metadataDir)
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

func r2Uploads(pkgFiles []string, metadataDir string) []r2Upload {
	uploads := make([]r2Upload, 0, len(pkgFiles)+len(pkgMetadataFiles))
	for _, src := range pkgFiles {
		uploads = append(uploads, r2Upload{
			sourcePath: src,
			objectKey:  "All/" + filepath.Base(src),
		})
	}
	for _, name := range pkgMetadataFiles {
		src := filepath.Join(metadataDir, name)
		if _, statErr := os.Stat(src); statErr != nil {
			continue
		}
		uploads = append(uploads, r2Upload{
			sourcePath: src,
			objectKey:  name,
		})
	}
	return uploads
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
