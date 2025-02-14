package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"AddContactFormRequest.Name":          "Name field is required.",
		"AddContactFormRequest.Email":         "Email is required.",
		"AddContactFormRequest.Message":       "Message field is required.",
		"SubscribeRequest.Email":              "Email is invalid.",
		"ValidateTurnstileTokenRequest.Token": "Token field is required.",
	}
)

type AddContactFormRequest struct {
	Name    string `json:"name" validate:"required"`
	Email   string `json:"email" validate:"required,email"`
	Message string `json:"message" validate:"required"`
}

type SubscribeRequest struct {
	Email string `json:"email" validate:"required,email"`
}

type ValidateTurnstileTokenRequest struct {
	Token string `json:"token" validate:"required"`
}
