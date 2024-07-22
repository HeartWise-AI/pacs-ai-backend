package mailgun

import (
	"github.com/mailgun/mailgun-go/v4"

	"api-pacs/infrastructures/providers/sdk/mailgun/types"
)

// MailgunSDK mailgun sdk
type MailgunSDK struct {
	MailgunImpl *mailgun.MailgunImpl
}

func NewMailgunImpl(config types.Config) (*MailgunSDK, error) {
	mailgunImpl := mailgun.NewMailgun(config.Domain, config.APIKey)

	return &MailgunSDK{
		MailgunImpl: mailgunImpl,
	}, nil
}
