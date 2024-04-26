package types

import (
	"context"
)

// AWSSDKInterface list of implementable methods for aws sdk
type AWSSDKInterface interface {
	SESSendEmail(ctx context.Context, data SESSendEmailRequest) error
	S3GetPresignedURL(ctx context.Context, data S3GetPresignedURLRequest) (string, error)
	S3Upload(ctx context.Context, data S3UploadRequest) error
}
