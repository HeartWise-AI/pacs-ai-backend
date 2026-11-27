package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type ingestionUpgradeCapture struct {
	repository.InferenceCommandRepositoryInterface
	upgraded repositoryTypes.UpgradeInferenceIngestionJob
	err      error
}

func (capture *ingestionUpgradeCapture) UpgradeInferenceIngestionJob(data repositoryTypes.UpgradeInferenceIngestionJob) error {
	capture.upgraded = data
	return capture.err
}

func upgradeFixture(status entity.InferenceIngestionJobStatus) (*InferenceCommandService, *ingestionUpgradeCapture) {
	reader := &ingestionRegistrationReader{
		job: entity.InferenceIngestionJob{
			ID: "job-1", TenantID: "tenant-a", ContainerID: "container-1", ModelID: "model-a",
			ModelName: "Model A", ModelVersion: "1.0.0", Status: status,
		},
		model: entity.InferenceModel{
			ID: "registration-1", TenantID: "tenant-a", ContainerID: "container-2", DockerImage: "heartwisehub/model:2.0.0",
			Deployment: &entity.InferenceModelDeployment{
				ContainerID: "container-2", ImageReference: "heartwisehub/model:2.0.0",
				ArtifactModelID: "model-a", ModelVersion: "2.0.0",
			},
		},
	}
	capture := &ingestionUpgradeCapture{}
	return &InferenceCommandService{InferenceQueryRepositoryInterface: reader, InferenceCommandRepositoryInterface: capture}, capture
}

func TestUpgradeInferenceIngestionJobUsesAuthoritativeCurrentDeployment(t *testing.T) {
	service, capture := upgradeFixture(entity.InferenceIngestionJobStatusStopped)
	err := service.UpgradeInferenceIngestionJob(context.Background(), serviceTypes.UpgradeInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-a", ExpectedModelVersion: "1.0.0",
	})
	require.NoError(t, err)
	require.Equal(t, repositoryTypes.UpgradeInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-a", ExpectedModelVersion: "1.0.0",
		ContainerID: "container-2", ModelID: "registration-1", ModelName: "Model A", ModelVersion: "2.0.0",
	}, capture.upgraded)
}

func TestUpgradeInferenceIngestionJobRejectsUnsafeOrStaleRequests(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   entity.InferenceIngestionJobStatus
		tenant   string
		expected string
		want     string
	}{
		{name: "job is running", status: entity.InferenceIngestionJobStatusRunning, tenant: "tenant-a", expected: "1.0.0", want: apiError.InferenceIngestionJobUpgradeConflict},
		{name: "snapshot is stale", status: entity.InferenceIngestionJobStatusStopped, tenant: "tenant-a", expected: "0.9.0", want: apiError.InferenceIngestionJobUpgradeConflict},
		{name: "tenant differs", status: entity.InferenceIngestionJobStatusStopped, tenant: "tenant-b", expected: "1.0.0", want: apiError.MissingRecord},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, capture := upgradeFixture(test.status)
			err := service.UpgradeInferenceIngestionJob(context.Background(), serviceTypes.UpgradeInferenceIngestionJob{
				ID: "job-1", TenantID: test.tenant, ExpectedModelVersion: test.expected,
			})
			require.EqualError(t, err, test.want)
			require.Empty(t, capture.upgraded.ID)
		})
	}
}

func TestUpgradeInferenceIngestionJobPropagatesAtomicConflict(t *testing.T) {
	service, capture := upgradeFixture(entity.InferenceIngestionJobStatusStopped)
	capture.err = errors.New(apiError.InferenceIngestionJobUpgradeConflict)
	err := service.UpgradeInferenceIngestionJob(context.Background(), serviceTypes.UpgradeInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-a", ExpectedModelVersion: "1.0.0",
	})
	require.EqualError(t, err, apiError.InferenceIngestionJobUpgradeConflict)
}
