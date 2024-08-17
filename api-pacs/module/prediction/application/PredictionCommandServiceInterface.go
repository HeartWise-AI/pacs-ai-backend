package application

import (
	"api-pacs/module/prediction/infrastructure/service/types"
)

// TenantCommandServiceInterface holds the implementable methods for the tenant command service
type PredictionCommandServiceInterface interface {
	CreatePrediction(id string) (types.DicomPrediction, error)
}
