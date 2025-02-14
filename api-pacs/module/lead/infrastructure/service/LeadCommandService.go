package service

import (
	"context"
	"errors"

	cloudflareAPITypes "api-pacs/infrastructures/providers/api/cloudflare/types"
	mailchimpAPITypes "api-pacs/infrastructures/providers/api/mailchimp/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/lead/infrastructure/service/types"
)

// LeadCommandService handles the lead command service logic
type LeadCommandService struct {
	mailchimpAPITypes.MailchimpAPIInterface
	cloudflareAPITypes.CloudflareAPIInterface
}

// AddContactForm adds a contact form to the mailchimp list
func (service *LeadCommandService) AddContactForm(ctx context.Context, data types.AddContactForm) error {
	res, err := service.CloudflareAPIInterface.ValidateTurnstileToken(ctx, data.Token)
	if err != nil {
		return err
	}

	if !res.Success {
		return errors.New(apiError.UnauthorizedAccess)
	}

	err = service.MailchimpAPIInterface.AddContactForm(ctx, mailchimpAPITypes.AddContactFormRequest{
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
func (service *LeadCommandService) Subscribe(ctx context.Context, data types.Subscribe) error {
	res, err := service.CloudflareAPIInterface.ValidateTurnstileToken(ctx, data.Token)
	if err != nil {
		return err
	}

	if !res.Success {
		return errors.New(apiError.UnauthorizedAccess)
	}

	err = service.MailchimpAPIInterface.Subscribe(ctx, data.Email)
	if err != nil {
		return err
	}

	return nil
}
