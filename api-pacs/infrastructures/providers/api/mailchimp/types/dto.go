package types

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

type MailchimpContact struct {
	EmailAddress string     `json:"email_address"`
	Status       string     `json:"status"`
	MergeFields  MergeField `json:"merge_fields"`
	Tags         []string   `json:"tags"`
}

type MergeField struct {
	Name    string `json:"NAME"`
	Message string `json:"MESSAGE"`
}
