package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func TestCreateInferenceIngestionJobRecordsCreatorAndNotificationSwitch(t *testing.T) {
	off := false
	on := true
	cases := []struct {
		name        string
		creator     string
		switchValue *bool
		wantCreator *string
		wantEnabled bool
	}{
		{"absent switch defaults to on", "user-a", nil, stringPointer("user-a"), true},
		{"explicit off", "user-a", &off, stringPointer("user-a"), false},
		{"explicit on", " user-b ", &on, stringPointer("user-b"), true},
		{"no authenticated creator", "", nil, nil, true},
	}
	for _, tc := range cases {
		for _, importing := range []bool{false, true} {
			reader := &ingestionRegistrationReader{model: entity.InferenceModel{ID: "firestore-document", TenantID: "tenant", ContainerID: "original", Deployment: &entity.InferenceModelDeployment{ContainerID: "original", ArtifactModelID: "CathEF_CLIP", ModelVersion: "1.0.1"}}}
			capture := &registrationJobCapture{}
			command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader, InferenceCommandRepositoryInterface: capture}
			input := serviceTypes.CreateInferenceIngestionJob{
				TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "original", ModelVersion: "1.0.1",
				CreatedByUserID: tc.creator, NotificationsEnabled: tc.switchValue,
			}
			if importing {
				require.NoError(t, command.ImportInferenceIngestionJobs(context.Background(), []serviceTypes.CreateInferenceIngestionJob{input}))
			} else {
				require.NoError(t, command.CreateInferenceIngestionJob(context.Background(), input))
			}
			require.Equal(t, tc.wantCreator, capture.stored.CreatedByUserID, tc.name)
			require.Equal(t, tc.wantEnabled, capture.stored.NotificationsEnabled, tc.name)
		}
	}
}

type ingestionJobUpdateCapture struct {
	repository.InferenceCommandRepositoryInterface
	updated repositoryTypes.UpdateInferenceIngestionJob
}

func (capture *ingestionJobUpdateCapture) UpdateInferenceIngestionJob(data repositoryTypes.UpdateInferenceIngestionJob) error {
	capture.updated = data
	return nil
}

func TestUpdateInferenceIngestionJobPassesNotificationSwitchThrough(t *testing.T) {
	off := false
	for _, value := range []*bool{nil, &off} {
		capture := &ingestionJobUpdateCapture{}
		command := &InferenceCommandService{InferenceCommandRepositoryInterface: capture}
		require.NoError(t, command.UpdateInferenceIngestionJob(context.Background(), serviceTypes.UpdateInferenceIngestionJob{
			ID: "job-1", Modalities: []string{"US"}, StabilityMinutes: 10, NotificationsEnabled: value,
		}))
		require.Equal(t, value, capture.updated.NotificationsEnabled)
	}
}

func TestViewerIngestionJobsKeepNotificationsOn(t *testing.T) {
	capture := &registrationJobCapture{}
	command := &InferenceCommandService{InferenceCommandRepositoryInterface: capture}
	job, err := command.createViewerIngestionJob("tenant", entity.InferenceModel{ID: "model", ContainerID: "container"}, dockerInferenceTypes.ModelInfo{SupportedDicomModalities: []string{"US"}}, "Model", "1", "US")
	require.NoError(t, err)
	require.True(t, job.NotificationsEnabled)
	require.True(t, capture.stored.NotificationsEnabled)
	require.Nil(t, capture.stored.CreatedByUserID)
}
