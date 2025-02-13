package application

import (
	"context"

	serviceTypes "api-pacs/module/lead/infrastructure/service/types"
)

// LeadCommandServiceInterface holds the implementable methods for the lead command service
type LeadCommandServiceInterface interface {
	// Subscribe adds a subscriber to the mailchimp list
	Subscribe(ctx context.Context, email string) error
	// AddContactForm adds a contact form to the mailchimp list
	AddContactForm(ctx context.Context, data serviceTypes.AddContactForm) error
}
