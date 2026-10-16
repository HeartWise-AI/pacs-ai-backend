package repository

import (
	"testing"

	"api-pacs/module/inference/domain/entity"
)

func repositoryDeploymentOperation() entity.InferenceModelDeploymentOperation {
	return entity.InferenceModelDeploymentOperation{
		ID: "operation-1", TenantID: "tenant-1", ModelID: "registration-1", ActorUserID: "user-1",
		CatalogVersionID: "catalog-1", RequestedVersion: "2.0.0",
		RequestedImage: "heartwisehub/model:2.0.0", RequestedImageDigest: "sha256:digest",
		Source: entity.InferenceModelDeployment{
			ContainerID: "old-container", ImageReference: "heartwisehub/model:1.0.0",
			Provenance: &entity.InferenceModelProvenance{SourceRepository: "HeartWise-AI/pacs-ai-backend"},
		},
		Status:   entity.InferenceModelDeploymentOperationPending,
		Stage:    entity.InferenceModelDeploymentStageResolveActive,
		Sequence: 1, CreatedAt: 1000, UpdatedAt: 1000,
	}
}

func TestValidateDeploymentOperationUpdateAcceptsMonotonicProgress(t *testing.T) {
	current := repositoryDeploymentOperation()
	next := current
	next.Source.Provenance = &entity.InferenceModelProvenance{SourceRepository: "HeartWise-AI/pacs-ai-backend"}
	next.Status = entity.InferenceModelDeploymentOperationRunning
	next.Stage = entity.InferenceModelDeploymentStageResolveVersion
	next.Sequence++
	next.StartedAt = 1001
	next.UpdatedAt = 1001
	if err := validateDeploymentOperationUpdate(current, next); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDeploymentOperationUpdateRejectsConflicts(t *testing.T) {
	tests := []struct {
		name   string
		change func(*entity.InferenceModelDeploymentOperation)
	}{
		{name: "identity mutation", change: func(next *entity.InferenceModelDeploymentOperation) { next.CatalogVersionID = "other" }},
		{name: "sequence gap", change: func(next *entity.InferenceModelDeploymentOperation) { next.Sequence += 2 }},
		{name: "stage regression", change: func(next *entity.InferenceModelDeploymentOperation) {
			next.Stage = entity.InferenceModelDeploymentStageResolveActive
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := repositoryDeploymentOperation()
			current.Status = entity.InferenceModelDeploymentOperationRunning
			current.Stage = entity.InferenceModelDeploymentStagePull
			current.StartedAt = 1000
			next := current
			next.Stage = entity.InferenceModelDeploymentStageCreate
			next.Sequence++
			next.UpdatedAt++
			test.change(&next)
			if err := validateDeploymentOperationUpdate(current, next); err == nil {
				t.Fatal("conflicting update was accepted")
			}
		})
	}

	terminal := repositoryDeploymentOperation()
	terminal.Status = entity.InferenceModelDeploymentOperationInterrupted
	terminal.ErrorCode = "INTERRUPTED"
	terminal.ErrorMessage = "interrupted"
	terminal.FinishedAt = 1001
	terminal.UpdatedAt = 1001
	next := terminal
	next.Sequence++
	next.UpdatedAt++
	if err := validateDeploymentOperationUpdate(terminal, next); err == nil {
		t.Fatal("terminal operation update was accepted")
	}
}
