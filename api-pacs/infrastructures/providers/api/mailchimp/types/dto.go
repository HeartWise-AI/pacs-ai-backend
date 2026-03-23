package types

type MailchimpStatus string

const (
	SubscribedStatus MailchimpStatus = "subscribed"
)

type AddContactFormRequest struct {
	Name    string
	Email   string
	Message string
}

type Config struct {
	BaseURL string
	ListID  string
	APIKey  string
}
