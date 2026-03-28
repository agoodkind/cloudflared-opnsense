#!/usr/bin/env bash
# Upload built pkg files and repository metadata to Cloudflare R2.
#
# Required env vars:
#   CF_ACCOUNT_ID        Cloudflare account ID
#   AWS_ACCESS_KEY_ID    R2 access key (set by workflow env)
#   AWS_SECRET_ACCESS_KEY  R2 secret key (set by workflow env)
#   AWS_DEFAULT_REGION   Must be "auto" for R2
#   ARTIFACT_DIR         Path to the downloaded artifact directory
set -euo pipefail

ENDPOINT="https://${CF_ACCOUNT_ID}.r2.cloudflarestorage.com"
BUCKET="cloudflared-opnsense-pkg"

echo "Uploading .pkg files to R2..."
for f in "${ARTIFACT_DIR}/pkg-repo-build/All/"*.pkg; do
    [[ -f "$f" ]] || continue
    aws s3 cp "$f" "s3://${BUCKET}/All/$(basename "$f")" \
        --endpoint-url "${ENDPOINT}"
done

echo "Uploading repository metadata to R2..."
for name in meta.conf meta packagesite.yaml packagesite.pkg data.pkg; do
    f="${ARTIFACT_DIR}/pkg/${name}"
    if [[ -f "$f" ]]; then
        aws s3 cp "$f" "s3://${BUCKET}/${name}" \
            --endpoint-url "${ENDPOINT}"
    fi
done

echo "R2 upload complete."
