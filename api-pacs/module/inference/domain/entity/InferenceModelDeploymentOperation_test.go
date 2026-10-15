package entity

import "testing"

func validDeploymentOperation() InferenceModelDeploymentOperation {
	return InferenceModelDeploymentOperation{
		ID: "operation-1", TenantID: "tenant-1", ModelID: "registration-1", ActorUserID: "user-1",
		CatalogVersionID: "catalog-1", RequestedVersion: "2.0.0",
		RequestedImage: "heartwisehub/model:2.0.0", RequestedImageDigest: "sha256:digest",
		Source: InferenceModelDeployment{ContainerID: "old-container", ImageReference: "heartwisehub/model:1.0.0"},
		Status: InferenceModelDeploymentOperationPending, Stage: InferenceModelDeploymentStageResolveActive,
		Sequence: 1, CreatedAt: 1000, UpdatedAt: 1000,
	}
}

func TestInferenceModelDeploymentOperationValidatesLifecycleStates(t *testing.T) {
	pending := validDeploymentOperation()
	if err := pending.Validate(); err != nil {
		t.Fatal(err)
	}

	running := pending
	running.Status = InferenceModelDeploymentOperationRunning
	running.Stage = InferenceModelDeploymentStagePull
	running.StartedAt = 1001
	running.UpdatedAt = 1002
	if err := running.Validate(); err != nil {
		t.Fatal(err)
	}

	completed := running
	completed.Status = InferenceModelDeploymentOperationCompleted
	completed.FinishedAt = 1003
	completed.UpdatedAt = 1003
	completed.Active = &InferenceModelDeployment{ContainerID: "new-container", ImageReference: "heartwisehub/model:2.0.0"}
	if err := completed.Validate(); err != nil || !completed.Terminal() {
		t.Fatalf("completed operation validation = %v terminal=%v", err, completed.Terminal())
	}

	interrupted := pending
	interrupted.Status = InferenceModelDeploymentOperationInterrupted
	interrupted.ErrorCode = "INFERENCE_DEPLOYMENT_INTERRUPTED"
	interrupted.ErrorMessage = "interrupted"
	interrupted.FinishedAt = 1001
	interrupted.UpdatedAt = 1001
	if err := interrupted.Validate(); err != nil || !interrupted.Terminal() {
		t.Fatalf("interrupted operation validation = %v terminal=%v", err, interrupted.Terminal())
	}
}

func TestInferenceModelDeploymentOperationRejectsIncompleteOrInconsistentState(t *testing.T) {
	tests := []struct {
		name   string
		change func(*InferenceModelDeploymentOperation)
	}{
		{name: "missing actor", change: func(operation *InferenceModelDeploymentOperation) { operation.ActorUserID = "" }},
		{name: "missing source", change: func(operation *InferenceModelDeploymentOperation) { operation.Source.ContainerID = "" }},
		{name: "invalid stage", change: func(operation *InferenceModelDeploymentOperation) { operation.Stage = "unknown" }},
		{name: "zero sequence", change: func(operation *InferenceModelDeploymentOperation) { operation.Sequence = 0 }},
		{name: "pending started", change: func(operation *InferenceModelDeploymentOperation) { operation.StartedAt = 1001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := validDeploymentOperation()
			test.change(&operation)
			if err := operation.Validate(); err == nil {
				t.Fatal("invalid operation was accepted")
			}
		})
	}
}
