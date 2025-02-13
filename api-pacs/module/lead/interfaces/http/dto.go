package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"CreateLeadRequest.Email":       "Email field is required.",
		"AddContactFormRequest.Name":    "Name field is required.",
		"AddContactFormRequest.Email":   "Email field is required.",
		"AddContactFormRequest.Message": "Message field is required.",
	}
)

type SubscribeRequest struct {
	Email string `json:"email" validate:"required,email"`
}

type AddContactFormRequest struct {
	Name    string `json:"name" validate:"required"`
	Email   string `json:"email" validate:"required,email"`
	Message string `json:"message" validate:"required"`
}
