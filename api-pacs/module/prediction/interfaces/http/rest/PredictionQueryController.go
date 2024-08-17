package rest

import (
	"api-pacs/module/prediction/application"
)

// PredictionQueryController request controller for record command
type PredictionQueryController struct {
	application.PredictionCommandServiceInterface
}
