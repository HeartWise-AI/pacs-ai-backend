package service

import (
	"context"
	"errors"
	"strings"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

// Historical ingestion model_id values are artifact IDs from model-info, not
// Firestore document IDs. Resolve only current tenant registrations; never fall
// back to a retained Docker container when an inventory read fails.
func (service *InferenceCommandService) selectIngestionRegistration(ctx context.Context, job entity.InferenceIngestionJob) (entity.InferenceModel, error) {
	if strings.TrimSpace(job.TenantID) == "" || strings.TrimSpace(job.ModelID) == "" || service.InferenceQueryRepositoryInterface == nil {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, job.TenantID)
	if err != nil {
		return entity.InferenceModel{}, err
	}
	var byID, byContainer, byArtifact []entity.InferenceModel
	for _, model := range models {
		if model.TenantID != job.TenantID || strings.TrimSpace(model.ContainerID) == "" {
			continue
		}
		if model.ID == strings.TrimSpace(job.ModelID) {
			byID = append(byID, model)
		}
		if model.ContainerID == strings.TrimSpace(job.ContainerID) {
			byContainer = append(byContainer, model)
		}
		if deployment := currentIngestionDeployment(model); deployment != nil && deployment.ArtifactModelID == strings.TrimSpace(job.ModelID) {
			byArtifact = append(byArtifact, model)
		}
	}
	for index, matches := range [][]entity.InferenceModel{byID, byContainer, byArtifact} {
		if len(matches) == 0 {
			continue
		}
		if len(matches) != 1 {
			return entity.InferenceModel{}, errors.New("ingestion model registration is ambiguous")
		}
		model := matches[0]
		if err := service.verifyIngestionRegistration(ctx, model, job, index != 0); err != nil {
			return entity.InferenceModel{}, err
		}
		return model, nil
	}
	return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
}

// A snapshot belongs to the current registration only while both immutable
// routing fields agree. Retained deployment history is never a routing source.
func currentIngestionDeployment(model entity.InferenceModel) *entity.InferenceModelDeployment {
	if model.Deployment != nil && model.Deployment.ContainerID == model.ContainerID && model.Deployment.ImageReference == model.DockerImage {
		return model.Deployment
	}
	return nil
}

func (service *InferenceCommandService) verifyIngestionRegistration(ctx context.Context, model entity.InferenceModel, job entity.InferenceIngestionJob, logicalID bool) error {
	artifactID, version := "", ""
	if deployment := currentIngestionDeployment(model); deployment != nil {
		artifactID, version = strings.TrimSpace(deployment.ArtifactModelID), strings.TrimSpace(deployment.ModelVersion)
	}
	if (logicalID && artifactID == "") || (strings.TrimSpace(job.ModelVersion) != "" && version == "") {
		if model.ContainerID != strings.TrimSpace(job.ContainerID) || service.DockerSDKInterface == nil || service.DockerInferenceAPIInterface == nil {
			return errors.New("ingestion model version changed or cannot be verified; update the job before dispatch")
		}
		container, err := service.DockerSDKInterface.GetContainerInfo(ctx, model.ContainerID)
		if err != nil {
			return err
		}
		info, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, strings.TrimPrefix(container.Name, "/"))
		if err != nil {
			return err
		}
		artifactID, version = strings.TrimSpace(info.Data.ModelID), strings.TrimSpace(info.Data.Version)
	}
	if logicalID && artifactID != strings.TrimSpace(job.ModelID) {
		return errors.New(apiError.MissingRecord)
	}
	if expected := strings.TrimSpace(job.ModelVersion); expected != "" && expected != version {
		return errors.New("ingestion model version changed; update the job before dispatch")
	}
	return nil
}
