package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{}
)

type GetTenantResponse struct {
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Address         string                   `json:"address"`
	AvailableModels []map[string]interface{} `json:"availableModels"`
	AET             string                   `json:"aet"`
	CreatedAt       uint                     `json:"createdAt"`
	UpdatedAt       uint                     `json:"updatedAt"`
}

type GetPublicTenantResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}
