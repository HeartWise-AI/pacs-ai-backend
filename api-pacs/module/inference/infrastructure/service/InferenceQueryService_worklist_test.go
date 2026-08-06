package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type worklistStatusQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	input repositoryTypes.ListWorklistStudyStatuses
	page  repositoryTypes.WorklistStudyStatusPage
	err   error
}

func (repository *worklistStatusQueryRepository) ListWorklistStudyStatuses(data repositoryTypes.ListWorklistStudyStatuses) (repositoryTypes.WorklistStudyStatusPage, error) {
	repository.input = data
	return repository.page, repository.err
}

func TestGetWorklistStudyStatusesMapsRepositorySnapshot(t *testing.T) {
	now := time.Now().UTC()
	startedAt := now.Add(-time.Minute)
	runID := "run-2"
	runNumber := 2
	trigger := entity.InferenceIngestionProcessingRunTriggerManualReprocess
	phase := entity.InferenceIngestionProcessingRunPhaseProcessing
	version := int64(7)
	message := "callback delivery failed"
	repository := &worklistStatusQueryRepository{page: repositoryTypes.WorklistStudyStatusPage{
		Studies: []repositoryTypes.WorklistStudyStatus{{
			StudyInstanceUID:  "1.2.3",
			IngestionStatus:   entity.InferenceIngestionCandidateStatusRetrieved,
			RunID:             &runID,
			RunNumber:         &runNumber,
			RunTrigger:        &trigger,
			Phase:             &phase,
			AttentionRequired: true,
			AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
				Code: "CALLBACK_DELIVERY_FAILED", Message: &message,
			}},
			ExpectedModels:  4,
			PendingModels:   1,
			RunningModels:   1,
			CompletedModels: 1,
			SkippedModels:   1,
			ActiveModels:    2,
			Version:         &version,
			StartedAt:       &startedAt,
			UpdatedAt:       now,
		}},
		HasMore: true,
	}}
	service := InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	page, err := service.GetWorklistStudyStatuses(context.Background(), serviceTypes.GetWorklistStudyStatuses{
		TenantID: " tenant-a ", StudyInstanceUIDs: []string{" 1.2.3 ", "1.2.3"}, Limit: 10, Offset: 20,
	})

	require.NoError(t, err)
	require.Equal(t, repositoryTypes.ListWorklistStudyStatuses{
		TenantID: "tenant-a", StudyInstanceUIDs: []string{"1.2.3"}, Limit: 10, Offset: 20,
	}, repository.input)
	require.Equal(t, serviceTypes.WorklistPage{Limit: 10, Offset: 20, HasMore: true}, page.WorklistPage)
	require.Len(t, page.Studies, 1)
	status := page.Studies[0]
	require.Equal(t, "1.2.3", status.StudyInstanceUID)
	require.Equal(t, &runID, status.RunID)
	require.Equal(t, &trigger, status.Trigger)
	require.Equal(t, serviceTypes.ProcessingRunCounts{
		Expected: 4, Pending: 1, Running: 1, Completed: 1, Skipped: 1, Active: 2,
	}, status.ProcessingRunCounts)
	require.Equal(t, &version, status.Version)
	require.Equal(t, "CALLBACK_DELIVERY_FAILED", status.AttentionReasons[0].Code)
}

func TestGetWorklistStudyStatusesAppliesDefaultLimitAndReturnsEmptyArray(t *testing.T) {
	repository := &worklistStatusQueryRepository{}
	service := InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	page, err := service.GetWorklistStudyStatuses(context.Background(), serviceTypes.GetWorklistStudyStatuses{
		TenantID: "tenant-a",
	})

	require.NoError(t, err)
	require.Equal(t, defaultWorklistPageLimit, repository.input.Limit)
	require.Equal(t, defaultWorklistPageLimit, page.Limit)
	require.NotNil(t, page.Studies)
	require.Empty(t, page.Studies)
}

func TestGetWorklistStudyStatusesNormalizesNilAttentionReasons(t *testing.T) {
	repository := &worklistStatusQueryRepository{page: repositoryTypes.WorklistStudyStatusPage{
		Studies: []repositoryTypes.WorklistStudyStatus{{StudyInstanceUID: "1.2.3"}},
	}}
	service := InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	page, err := service.GetWorklistStudyStatuses(context.Background(), serviceTypes.GetWorklistStudyStatuses{
		TenantID: "tenant-a",
	})

	require.NoError(t, err)
	require.NotNil(t, page.Studies[0].AttentionReasons)
	require.Empty(t, page.Studies[0].AttentionReasons)
}

func TestGetWorklistStudyStatusesRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name string
		data serviceTypes.GetWorklistStudyStatuses
		want string
	}{
		{name: "missing tenant", data: serviceTypes.GetWorklistStudyStatuses{}, want: apiError.InvalidPayload},
		{name: "negative limit", data: serviceTypes.GetWorklistStudyStatuses{TenantID: "tenant-a", Limit: -1}, want: apiError.InvalidPayload},
		{name: "negative offset", data: serviceTypes.GetWorklistStudyStatuses{TenantID: "tenant-a", Offset: -1}, want: apiError.InvalidPayload},
		{name: "empty study uid", data: serviceTypes.GetWorklistStudyStatuses{TenantID: "tenant-a", StudyInstanceUIDs: []string{" "}}, want: apiError.InvalidPayload},
		{name: "limit above maximum", data: serviceTypes.GetWorklistStudyStatuses{TenantID: "tenant-a", Limit: maximumWorklistPageLimit + 1}, want: apiError.MaximumLimitReached},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &worklistStatusQueryRepository{}
			service := InferenceQueryService{InferenceQueryRepositoryInterface: repository}

			_, err := service.GetWorklistStudyStatuses(context.Background(), test.data)

			require.EqualError(t, err, test.want)
			require.Empty(t, repository.input.TenantID)
		})
	}
}

func TestGetWorklistStudyStatusesPropagatesRepositoryFailure(t *testing.T) {
	repository := &worklistStatusQueryRepository{err: errors.New(apiError.DatabaseError)}
	service := InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	_, err := service.GetWorklistStudyStatuses(context.Background(), serviceTypes.GetWorklistStudyStatuses{
		TenantID: "tenant-a",
	})

	require.EqualError(t, err, apiError.DatabaseError)
}
