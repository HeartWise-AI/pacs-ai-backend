package types

type Config struct {
	APIKey string
	Domain string
}

type MailgunSendEmailRequest struct {
	Subject       string
	HTMLBody      string
	PlainTextBody string
	Recipient     string
}
