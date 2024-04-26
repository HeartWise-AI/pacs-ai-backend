package aws

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"

	"api-pacs/infrastructures/providers/sdk/aws/types"
)

// AWSSDK aws sdk
type AWSSDK struct {
	Session *session.Session
}

// NewSession create new aws session
func NewSession(config types.Config) (*AWSSDK, error) {
	session, err := session.NewSession(&aws.Config{Region: aws.String(config.Region),
		S3ForcePathStyle: aws.Bool(true), LogLevel: aws.LogLevel(aws.LogOff),
		Credentials: credentials.NewStaticCredentials(config.AccessID, config.SecretKey, "")})
	if err != nil {
		return nil, err
	}

	return &AWSSDK{
		Session: session,
	}, nil
}
