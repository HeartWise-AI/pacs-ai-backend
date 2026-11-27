package service

import (
	"context"
	"errors"
	"strings"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// UpgradeInferenceIngestionJob retargets a stopped job to the current durable
// deployment of the same tenant model. The client supplies only its observed
// version; container and model identity always come from the server registry.
func (service *InferenceCommandService) UpgradeInferenceIngestionJob(ctx context.Context, data types.UpgradeInferenceIngestionJob) error {
	data.ID = strings.TrimSpace(data.ID)
	data.TenantID = strings.TrimSpace(data.TenantID)
	data.ExpectedModelVersion = strings.TrimSpace(data.ExpectedModelVersion)
	if data.ID == "" || data.TenantID == "" || data.ExpectedModelVersion == "" {
		return errors.New(apiError.InvalidPayload)
	}

	job, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobByID(data.ID)
	if err != nil {
		return err
	}
	if job.TenantID != data.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	if strings.TrimSpace(job.ModelVersion) != data.ExpectedModelVersion || job.Status != entity.InferenceIngestionJobStatusStopped {
		return errors.New(apiError.InferenceIngestionJobUpgradeConflict)
	}

	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, data.TenantID)
	if err != nil {
		return err
	}
	registered, _, err := matchIngestionRegistration(models, job)
	if err != nil {
		return err
	}
	deployment := currentIngestionDeployment(registered)
	if deployment == nil || strings.TrimSpace(deployment.ArtifactModelID) == "" ||
		strings.TrimSpace(deployment.ModelVersion) == "" || strings.TrimSpace(registered.ContainerID) == "" ||
		strings.TrimSpace(registered.ID) == "" {
		return errors.New(apiError.InferenceUpgradeUnavailable)
	}

	if strings.TrimSpace(deployment.ModelVersion) == data.ExpectedModelVersion &&
		strings.TrimSpace(job.ContainerID) == strings.TrimSpace(registered.ContainerID) &&
		strings.TrimSpace(job.ModelID) == strings.TrimSpace(registered.ID) {
		return nil
	}
	return service.InferenceCommandRepositoryInterface.UpgradeInferenceIngestionJob(repositoryTypes.UpgradeInferenceIngestionJob{
		ID:                   data.ID,
		TenantID:             data.TenantID,
		ExpectedModelVersion: data.ExpectedModelVersion,
		ContainerID:          strings.TrimSpace(registered.ContainerID),
		ModelID:              strings.TrimSpace(registered.ID),
		ModelName:            strings.TrimSpace(job.ModelName),
		ModelVersion:         strings.TrimSpace(deployment.ModelVersion),
	})
}
