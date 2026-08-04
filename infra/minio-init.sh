#!/bin/sh
set -eu
umask 077

mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
mc mb --ignore-existing "local/$IM_S3_BUCKET"
mc anonymous set none "local/$IM_S3_BUCKET"
mc version enable "local/$IM_S3_BUCKET"

cat > /tmp/linli-im-app-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetBucketLocation", "s3:ListBucket", "s3:ListBucketMultipartUploads"],
      "Resource": ["arn:aws:s3:::$IM_S3_BUCKET"]
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
      "Resource": ["arn:aws:s3:::$IM_S3_BUCKET/*"]
    }
  ]
}
EOF

mc admin user add local "$MINIO_APP_USER" "$MINIO_APP_PASSWORD"
mc admin policy create local linli-im-app /tmp/linli-im-app-policy.json
mc admin policy attach local linli-im-app --user "$MINIO_APP_USER"
