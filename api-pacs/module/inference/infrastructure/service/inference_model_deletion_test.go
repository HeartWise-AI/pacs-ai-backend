package service

import (
	"context"
	"errors"
	"testing"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
)

type deletionCommandRepository struct {
	repository.InferenceCommandRepositoryInterface
	model       entity.InferenceModel
	claimIDs    []string
	deleteClaim string
	deleted     bool
}

func (repository *deletionCommandRepository) ClaimInferenceModelDeletion(_ context.Context, _, claimID string) (entity.InferenceModel, error) {
	repository.claimIDs = append(repository.claimIDs, claimID)
	if repository.model.ActiveUpgradeID != "" {
		return entity.InferenceModel{}, errors.New(apiError.InferenceUpgradeConflict)
	}
	if repository.model.DeletionClaimID != "" && repository.model.DeletionClaimID != claimID {
		return entity.InferenceModel{}, errors.New(apiError.InferenceUpgradeConflict)
	}
	repository.model.DeletionClaimID = claimID
	return repository.model, nil
}

func (repository *deletionCommandRepository) DeleteInferenceModel(_ context.Context, _, claimID string) error {
	repository.deleteClaim = claimID
	repository.deleted = true
	return nil
}

func (*deletionCommandRepository) DeleteInferenceIngestionJobByContainerID(string, string) error {
	return nil
}

type deletionDocker struct {
	dockerTypes.DockerSDKInterface
	removeErrors []error
	removeCalls  int
}

func (docker *deletionDocker) RemoveContainer(context.Context, string) error {
	docker.removeCalls++
	if len(docker.removeErrors) > 0 {
		err := docker.removeErrors[0]
		docker.removeErrors = docker.removeErrors[1:]
		return err
	}
	return nil
}

func TestRemoveInferenceModelRejectsActiveUpgradeBeforeContainerDeletion(t *testing.T) {
	commandRepository := &deletionCommandRepository{model: entity.InferenceModel{
		ID: "model-1", TenantID: "tenant-1", ContainerID: "container-1", ActiveUpgradeID: "upgrade-1",
	}}
	docker := &deletionDocker{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface: commandRepository,
		DockerSDKInterface:                  docker,
	}

	err := service.RemoveInferenceModel(context.Background(), "model-1")
	if err == nil || err.Error() != apiError.InferenceUpgradeConflict {
		t.Fatalf("active upgrade did not reject model deletion: %v", err)
	}
	if docker.removeCalls != 0 || commandRepository.deleted {
		t.Fatalf("deletion side effects ran while an upgrade was active: remove_calls=%d deleted=%t", docker.removeCalls, commandRepository.deleted)
	}
}

func TestRemoveInferenceModelRetainsDeterministicClaimForDockerRetry(t *testing.T) {
	commandRepository := &deletionCommandRepository{model: entity.InferenceModel{
		ID: "model-1", TenantID: "tenant-1", ContainerID: "container-1",
	}}
	docker := &deletionDocker{removeErrors: []error{errors.New("docker unavailable")}}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface: commandRepository,
		DockerSDKInterface:                  docker,
	}

	if err := service.RemoveInferenceModel(context.Background(), "model-1"); err == nil || err.Error() != apiError.DockerError {
		t.Fatalf("first deletion did not surface Docker failure: %v", err)
	}
	if commandRepository.model.DeletionClaimID == "" || commandRepository.deleted {
		t.Fatalf("failed deletion did not retain its claim safely: %+v", commandRepository)
	}
	if err := service.RemoveInferenceModel(context.Background(), "model-1"); err != nil {
		t.Fatal(err)
	}
	if len(commandRepository.claimIDs) != 2 || commandRepository.claimIDs[0] != commandRepository.claimIDs[1] {
		t.Fatalf("deletion retry did not reuse its claim: %v", commandRepository.claimIDs)
	}
	if !commandRepository.deleted || commandRepository.deleteClaim != commandRepository.claimIDs[0] {
		t.Fatalf("registration was not deleted by the claim owner: %+v", commandRepository)
	}
}
