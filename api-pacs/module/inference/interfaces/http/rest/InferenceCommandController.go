package rest

import (
	"api-pacs/module/inference/application"
)

// InferenceCommandController request controller for inference command
type InferenceCommandController struct {
	application.InferenceCommandServiceInterface
}
