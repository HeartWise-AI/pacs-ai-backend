package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	viewmodels "api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/lead/application"
	serviceTypes "api-pacs/module/lead/infrastructure/service/types"
	types "api-pacs/module/lead/interfaces/http"
)

// LeadCommandController request controller for lead command
type LeadCommandController struct {
	application.LeadCommandServiceInterface
}

// Subscribe request handler to subscribe to the mailchimp list
func (controller *LeadCommandController) Subscribe(w http.ResponseWriter, r *http.Request) {
	var request types.SubscribeRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.LeadCommandServiceInterface.Subscribe(context.TODO(), request.Email)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case errors.MailchimpAPIError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Mailchimp API error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully subscribed to the list.",
	}

	response.JSON(w)
}

// ValidateTurnstileToken request handler to validate the turnstile token
func (controller *LeadCommandController) ValidateTurnstileToken(w http.ResponseWriter, r *http.Request) {
	var request types.ValidateTurnstileTokenRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	success, err := controller.LeadCommandServiceInterface.ValidateTurnstileToken(context.TODO(), request.Token)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case errors.CloudflareAPIError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Cloudflare API error."
		default:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	if !success {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Unauthorized access.",
			ErrorCode: apiError.UnauthorizedAccess,
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: success,
		Message: "Successfully validated turnstile token.",
	}

	response.JSON(w)
}

// AddContactForm request handler to add contact form
func (controller *LeadCommandController) AddContactForm(w http.ResponseWriter, r *http.Request) {
	var request types.AddContactFormRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.LeadCommandServiceInterface.AddContactForm(context.TODO(), serviceTypes.AddContactForm{
		Name:    request.Name,
		Email:   request.Email,
		Message: request.Message,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case errors.MailchimpAPIError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Mailchimp API error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully added contact form.",
	}

	response.JSON(w)
}
