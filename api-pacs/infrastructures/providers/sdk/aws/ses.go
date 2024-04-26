package aws

import (
	"context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ses"

	"api-pacs/infrastructures/providers/sdk/aws/types"
)

const sourceEmail string = "no-reply@heartwise.ai"

// SESSendEmail send a new email
func (a *AWSSDK) SESSendEmail(ctx context.Context, data types.SESSendEmailRequest) error {
	sesClient := ses.New(a.Session)

	if len(data.HTMLMessage) == 0 {
		data.HTMLMessage = data.PlainTextMessage
	}

	if len(data.SourceEmail) == 0 {
		data.SourceEmail = sourceEmail
	}

	// validate addresses
	var ccAddresses []*string
	var toAddresses []*string

	for _, ccAddress := range data.CCAddresses {
		ccAddresses = append(ccAddresses, &ccAddress)
	}

	for _, toAddress := range data.ToAddresses {
		toAddresses = append(toAddresses, aws.String(toAddress))
	}

	input := &ses.SendEmailInput{
		Destination: &ses.Destination{
			CcAddresses: ccAddresses,
			ToAddresses: toAddresses,
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Html: &ses.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(data.HTMLMessage),
				},
				Text: &ses.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(data.PlainTextMessage),
				},
			},
			Subject: &ses.Content{
				Charset: aws.String("UTF-8"),
				Data:    aws.String(data.Subject),
			},
		},
		Source: aws.String(data.SourceEmail),
	}

	_, err := sesClient.SendEmail(input)
	if err != nil {
		return err
	}

	return nil
}
