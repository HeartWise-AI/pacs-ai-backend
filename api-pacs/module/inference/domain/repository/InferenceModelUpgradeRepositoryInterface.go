package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
)

// InferenceModelUpgradeRepositoryInterface owns the Firestore transaction
// boundary between an upgrade audit record and its model registration.
type InferenceModelUpgradeRepositoryInterface interface {
	BeginInferenceModelUpgrade(context.Context, entity.InferenceModelUpgrade) (entity.InferenceModelUpgrade, entity.InferenceModel, error)
	GetInferenceModelUpgrade(context.Context, string, string, string) (entity.InferenceModelUpgrade, error)
	GetInferenceModelForUpgrade(context.Context, string, string) (entity.InferenceModel, error)
	HasOtherInferenceModelRegistrations(context.Context, string, string) (bool, error)
	ListRecoverableInferenceModelUpgrades(context.Context) ([]entity.InferenceModelUpgrade, error)
	SaveInferenceModelUpgrade(context.Context, entity.InferenceModelUpgrade) error
	ActivateInferenceModelUpgrade(context.Context, entity.InferenceModelUpgrade, entity.InferenceModelDeployment, []string) error
	ActivateAndRetargetInferenceModelUpgrade(context.Context, entity.InferenceModelUpgrade, entity.InferenceModelDeployment, []string, string, []string) error
	FinishInferenceModelUpgrade(context.Context, entity.InferenceModelUpgrade, *entity.InferenceModelDeployment, []string) error
	RetargetInferenceIngestionJobs(context.Context, string, string, string, string) error
	HasActiveInferenceProcessingJobs(context.Context, string, string, string) (bool, error)
}
