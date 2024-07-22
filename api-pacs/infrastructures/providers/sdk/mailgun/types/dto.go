package types

type Config struct {
	APIKey string
	Domain string
}

type MailgunSendEmailRequest struct {
	Sender    string
	Subject   string
	Body      string
	Recipient string
}
