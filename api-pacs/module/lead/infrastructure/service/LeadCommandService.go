package service

import (
	"context"

	cloudflareAPITypes "api-pacs/infrastructures/providers/api/cloudflare/types"
	mailchimpAPITypes "api-pacs/infrastructures/providers/api/mailchimp/types"
	"api-pacs/module/lead/infrastructure/service/types"
)

// LeadCommandService handles the lead command service logic
type LeadCommandService struct {
	mailchimpAPITypes.MailchimpAPIInterface
	cloudflareAPITypes.CloudflareAPIInterface
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

// Subscribe adds a subscriber to the mailchimp list
func (service *LeadCommandService) Subscribe(ctx context.Context, email string) error {
	err := service.MailchimpAPIInterface.Subscribe(ctx, email)
	if err != nil {
		return err
	}

	return nil
}

// ValidateTurnstileToken validates the turnstile token
func (service *LeadCommandService) ValidateTurnstileToken(ctx context.Context, token string) (bool, error) {
	res, err := service.CloudflareAPIInterface.ValidateTurnstileToken(ctx, token)
	if err != nil {
		return false, err
	}

	return res.Success, nil
}
