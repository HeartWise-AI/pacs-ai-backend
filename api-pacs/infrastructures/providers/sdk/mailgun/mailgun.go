package mailgun

import (
	"context"
	"time"

	"github.com/mailgun/mailgun-go/v4"

	"api-pacs/infrastructures/providers/sdk/mailgun/types"
)

// MailgunSDK mailgun sdk
type MailgunSDK struct {
	MailgunImpl *mailgun.MailgunImpl
}

// NewMailgun creates a new mailgun client instance
func NewMailgun(config types.Config) (*MailgunSDK, error) {
	mailgunImpl := mailgun.NewMailgun(config.Domain, config.APIKey)

	return &MailgunSDK{
		MailgunImpl: mailgunImpl,
	}, nil
}

// SendEmail sends an email using mailgun
func (m *MailgunSDK) SendEmail(ctx context.Context, data types.MailgunSendEmailRequest) error {
	message := m.MailgunImpl.NewMessage(data.Sender, data.Subject, data.Body, data.Recipient)

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	_, _, err := m.MailgunImpl.Send(ctx, message)
	if err != nil {
		return err
	}

	return nil
}
