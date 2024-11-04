package rest

import (
	"api-pacs/module/inference/application"
)

// InferenceQueryController request controller for inference query
type InferenceQueryController struct {
	application.InferenceQueryServiceInterface
}
