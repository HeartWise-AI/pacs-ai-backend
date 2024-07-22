package mailgun

import (
	"context"
	"time"

	"api-pacs/infrastructures/providers/sdk/mailgun/types"
)

func (m *MailgunSDK) SendEmail(ctx context.Context, data types.MailgunSendEmailRequest) error {
	mailgun := m.MailgunImpl

	message := mailgun.NewMessage(data.Sender, data.Subject, data.Body, data.Recipient)

	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	_, _, err := mailgun.Send(ctx, message)
	if err != nil {
		return err
	}

	return nil
}
