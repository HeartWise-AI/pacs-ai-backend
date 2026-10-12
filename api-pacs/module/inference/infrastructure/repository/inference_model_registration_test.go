package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

func validRegistrationReplacement() repositoryTypes.ReplaceInferenceModelRegistration {
	sourceRevision := "source-revision"
	return repositoryTypes.ReplaceInferenceModelRegistration{
		ID:                     "registration-1",
		TenantID:               "tenant-1",
		ExpectedContainerID:    "old-container",
		ExpectedImageReference: "heartwise/model:1.0.0",
		Deployment: entity.InferenceModelDeployment{
			ContainerID:    "new-container",
			ImageReference: "heartwise/model:2.0.0",
			ImageDigest:    "sha256:digest",
			LocalImageID:   "sha256:local",
			ModelVersion:   "2.0.0",
			Provenance: &entity.InferenceModelProvenance{
				SourceRepository: "HeartWise-AI/pacs-ai-backend",
				SourceRevision:   &sourceRevision,
				ModelRepository:  "heartwise/model",
				ModelRevision:    "model-revision",
				WeightsPath:      "weights/model.pt",
				WeightsSHA256:    "weights-digest",
			},
			DeployedAt: time.Now().Unix(),
		},
	}
}

func TestValidateRegistrationReplacementRequiresCompleteDistinctDeployment(t *testing.T) {
	valid := validRegistrationReplacement()
	if err := validateRegistrationReplacement(valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		change func(*repositoryTypes.ReplaceInferenceModelRegistration)
	}{
		{name: "model ID", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.ID = "" }},
		{name: "tenant ID", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.TenantID = "" }},
		{name: "expected container", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.ExpectedContainerID = "" }},
		{name: "expected image", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.ExpectedImageReference = "" }},
		{name: "candidate container", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.ContainerID = "" }},
		{name: "same container", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) {
			data.Deployment.ContainerID = data.ExpectedContainerID
		}},
		{name: "candidate image", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.ImageReference = "" }},
		{name: "image digest", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.ImageDigest = "" }},
		{name: "local image ID", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.LocalImageID = "" }},
		{name: "model version", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.ModelVersion = "" }},
		{name: "provenance", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.Provenance = nil }},
		{name: "source revision", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) {
			data.Deployment.Provenance.SourceRevision = nil
		}},
		{name: "deployment time", change: func(data *repositoryTypes.ReplaceInferenceModelRegistration) { data.Deployment.DeployedAt = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := validRegistrationReplacement()
			test.change(&data)
			if err := validateRegistrationReplacement(data); err == nil || err.Error() != apiError.InvalidPayload {
				t.Fatalf("invalid replacement returned %v", err)
			}
		})
	}
}

func TestRegisteredDeploymentUsesOnlySnapshotMatchingLiveRegistration(t *testing.T) {
	persisted := &entity.InferenceModelDeployment{
		ContainerID: "active", ImageReference: "heartwise/model:2.0.0", ImageDigest: "sha256:digest",
	}
	current := registeredDeployment(entity.InferenceModel{
		ContainerID: "active", DockerImage: "heartwise/model:2.0.0", Deployment: persisted,
	})
	if current.ImageDigest != persisted.ImageDigest {
		t.Fatalf("matching deployment snapshot was not preserved: %+v", current)
	}

	legacy := registeredDeployment(entity.InferenceModel{
		ContainerID: "manually-replaced", DockerImage: "heartwise/model:3.0.0", Deployment: persisted,
	})
	if legacy.ContainerID != "manually-replaced" || legacy.ImageReference != "heartwise/model:3.0.0" || legacy.ImageDigest != "" {
		t.Fatalf("stale snapshot overrode the live registration: %+v", legacy)
	}
}

func TestValidateCurrentRegistrationRejectsStaleReplacement(t *testing.T) {
	data := validRegistrationReplacement()
	model := entity.InferenceModel{
		TenantID: data.TenantID, ContainerID: data.ExpectedContainerID, DockerImage: data.ExpectedImageReference,
	}
	if err := validateCurrentRegistration(model, data); err != nil {
		t.Fatal(err)
	}

	staleContainer := model
	staleContainer.ContainerID = "already-replaced"
	if err := validateCurrentRegistration(staleContainer, data); err == nil || err.Error() != apiError.InferenceUpgradeConflict {
		t.Fatalf("stale container returned %v", err)
	}
	staleImage := model
	staleImage.DockerImage = "heartwise/model:other"
	if err := validateCurrentRegistration(staleImage, data); err == nil || err.Error() != apiError.InferenceUpgradeConflict {
		t.Fatalf("stale image returned %v", err)
	}
	otherTenant := model
	otherTenant.TenantID = "tenant-2"
	if err := validateCurrentRegistration(otherTenant, data); err == nil || err.Error() != apiError.MissingRecord {
		t.Fatalf("cross-tenant registration returned %v", err)
	}
}

type registrationCommandRepository struct {
	domainRepository.InferenceCommandRepositoryInterface
	received repositoryTypes.ReplaceInferenceModelRegistration
	result   entity.InferenceModelDeployment
	err      error
}

func (repository *registrationCommandRepository) ReplaceInferenceModelRegistration(
	_ context.Context,
	data repositoryTypes.ReplaceInferenceModelRegistration,
) (entity.InferenceModelDeployment, error) {
	repository.received = data
	return repository.result, repository.err
}

func TestRegistrationCircuitBreakerReturnsRollbackBaseline(t *testing.T) {
	data := validRegistrationReplacement()
	baseline := entity.InferenceModelDeployment{ContainerID: data.ExpectedContainerID}
	inner := &registrationCommandRepository{result: baseline}
	decorator := InferenceCommandRepositoryCircuitBreaker{InferenceCommandRepositoryInterface: inner}

	result, err := decorator.ReplaceInferenceModelRegistration(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContainerID != baseline.ContainerID || inner.received.ID != data.ID {
		t.Fatalf("replacement result was not preserved: %+v", result)
	}

	inner.err = errors.New(apiError.InferenceUpgradeConflict)
	if _, err = decorator.ReplaceInferenceModelRegistration(context.Background(), data); err == nil || err.Error() != apiError.InferenceUpgradeConflict {
		t.Fatalf("replacement error was not preserved: %v", err)
	}
}
