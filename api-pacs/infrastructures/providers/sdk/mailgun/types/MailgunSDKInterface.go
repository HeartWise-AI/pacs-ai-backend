package types

import "context"

type MailgunSDKInterface interface {
	// SendEmail sends an email using mailgun
	SendEmail(ctx context.Context, data MailgunSendEmailRequest) error
}
