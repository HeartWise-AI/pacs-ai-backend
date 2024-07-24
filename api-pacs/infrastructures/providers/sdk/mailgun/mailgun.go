package mailgun

import (
	"context"
	"log"
	"os"
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
	if len(data.HTMLBody) == 0 {
		data.HTMLBody = data.PlainTextBody
	}

	message := m.MailgunImpl.NewMessage(os.Getenv("MAILGUN_SENDER_EMAIL"), data.Subject, "", data.Recipient)
	message.SetHtml(data.HTMLBody)

	ctx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	resp, _, err := m.MailgunImpl.Send(ctx, message)
	if err != nil {
		return err
	}

	log.Printf("Email Resp: %s\n", resp)

	return nil
}
