package aws

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"

	"api-pacs/infrastructures/providers/sdk/aws/types"
)

// S3GetPresignedURL get presigned url from s3
func (a *AWSSDK) S3GetPresignedURL(ctx context.Context, data types.S3GetPresignedURLRequest) (string, error) {
	s3Client := s3.New(a.Session)

	params := &s3.GetObjectInput{
		Bucket: aws.String(data.Bucket),
		Key:    aws.String(data.Key),
	}

	req, _ := s3Client.GetObjectRequest(params)

	url, err := req.Presign(time.Duration(data.ExpirationInMinutes) * time.Minute)
	if err != nil {
		return "", err
	}

	return url, nil
}

// S3Upload upload to s3
func (a *AWSSDK) S3Upload(ctx context.Context, data types.S3UploadRequest) error {
	s3Client := s3.New(a.Session)

	params := &s3.PutObjectInput{
		Bucket:        aws.String(data.Bucket),
		Key:           aws.String(data.Key),
		Body:          bytes.NewReader(data.Body),
		ContentLength: aws.Int64(data.ContentLength),
		ContentType:   aws.String(http.DetectContentType(data.Body)),
		Metadata:      data.Metadata,
	}

	// check if public read is enabled
	if data.PublicRead {
		params.ACL = aws.String("public-read")
	}

	_, err := s3Client.PutObject(params)
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			log.Println("S3 upload error:", awsErr.Code(), awsErr.Message(), awsErr.OrigErr())
			if reqErr, ok := err.(awserr.RequestFailure); ok {
				log.Println("S3 upload error:", reqErr.Code(), reqErr.Message(), reqErr.StatusCode(), reqErr.RequestID())
			}
			return err
		} else {
			log.Println("S3 upload error:", err)
			return err
		}
	}

	return nil
}
