package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"PredictionRequest.QueryID": "Query ID is required.",
	}
)

type PredictionRequest struct {
	QueryID string `json:"queryId" validate:"required"`
}

type PredictionResponse struct {
	Vessel string  `json:"vessel"`
	LVEF   float64 `json:"LVEF"`
	Age    int     `json:"age"`
}
