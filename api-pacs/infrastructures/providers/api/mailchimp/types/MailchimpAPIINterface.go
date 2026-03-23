package types

import "context"

// MailchimpAPIInterface is the interface for the Mailchimp API
type MailchimpAPIInterface interface {
	// Subscribe subscribes an email to the list
	Subscribe(ctx context.Context, email string) error
	// AddContactForm adds a contact form to the list
	AddContactForm(ctx context.Context, request AddContactFormRequest) error
}
