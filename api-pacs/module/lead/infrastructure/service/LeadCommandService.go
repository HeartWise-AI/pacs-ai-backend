package service

import (
	"context"

	mailchimpAPITypes "api-pacs/infrastructures/providers/api/mailchimp/types"
	"api-pacs/module/lead/infrastructure/service/types"
)

// LeadCommandService handles the lead command service logic
// LeadCommandService handles the lead command service logic
type LeadCommandService struct {
	mailchimpAPITypes.MailchimpAPIInterface
}

// Subscribe adds a subscriber to the mailchimp list
func (service *LeadCommandService) Subscribe(ctx context.Context, email string) error {
	err := service.MailchimpAPIInterface.Subscribe(ctx, email)
	if err != nil {
		return err
	}

	return nil
}

// AddContactForm adds a contact form to the mailchimp list
func (service *LeadCommandService) AddContactForm(ctx context.Context, data types.AddContactForm) error {
	err := service.MailchimpAPIInterface.AddContactForm(ctx, mailchimpAPITypes.AddContactFormRequest{
		Name:    data.Name,
		Email:   data.Email,
		Message: data.Message,
	})
	if err != nil {
		return err
	}

	return nil
}
