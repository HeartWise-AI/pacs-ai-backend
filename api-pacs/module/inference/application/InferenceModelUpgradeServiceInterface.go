package application

import (
	"context"

	"api-pacs/module/inference/domain/entity"
)

type StartInferenceModelUpgrade struct {
	TenantID            string
	ModelID             string
	ActorUserID         string
	ImageReference      string
	ExpectedDigest      string
	DrainTimeoutSeconds int
}

// InferenceModelUpgradeServiceInterface is kept separate from the prediction
// service so existing inference gateways and their test doubles stay stable.
type InferenceModelUpgradeServiceInterface interface {
	StartInferenceModelUpgrade(context.Context, StartInferenceModelUpgrade) (entity.InferenceModelUpgrade, error)
	GetInferenceModelUpgrade(context.Context, string, string, string) (entity.InferenceModelUpgrade, error)
	CancelInferenceModelUpgrade(context.Context, string, string, string) (entity.InferenceModelUpgrade, error)
	RecoverInferenceModelUpgrades(context.Context) error
}
