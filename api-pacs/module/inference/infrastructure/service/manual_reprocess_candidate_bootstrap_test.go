package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type manualBootstrapOrthancAPI struct {
	orthancAPITypes.OrthancAPIInterface
	resources []orthancAPITypes.GetLocalResourceResponse
	err       error
	requests  []orthancAPITypes.QueryLocalResourceRequest
}

func (api *manualBootstrapOrthancAPI) FindLocalResources(
	_ context.Context,
	request orthancAPITypes.QueryLocalResourceRequest,
) ([]orthancAPITypes.GetLocalResourceResponse, error) {
	api.requests = append(api.requests, request)
	return api.resources, api.err
}

func localXAStudyResource() orthancAPITypes.GetLocalResourceResponse {
	return orthancAPITypes.GetLocalResourceResponse{
		ID: "orthanc-study-1",
		RequestedTags: map[string]string{
			"ModalitiesInStudy":             "XA\\SR",
			"StudyDate":                     "20211116",
			"StudyTime":                     "101500",
			"PatientID":                     "patient-1",
			"AccessionNumber":               "accession-1",
			"NumberOfStudyRelatedSeries":    "3",
			"NumberOfStudyRelatedInstances": "46",
		},
	}
}

func manualBootstrapState() *realtimeWorklistE2EState {
	now := time.Now().UTC()
	return &realtimeWorklistE2EState{
		candidates: map[string]entity.InferenceIngestionCandidate{},
		jobs: map[string]entity.InferenceIngestionJob{
			"xa-new": {
				ID: "xa-new", TenantID: "tenant-a", DICOMModality: "PACS_QUERY", Modalities: []string{"XA"},
				ModelName: "CoroModel", ModelVersion: "v2", ContainerID: "coro-v2",
				Status: entity.InferenceIngestionJobStatusRunning, CreatedAt: now, UpdatedAt: now.Add(-2 * time.Hour),
			},
			"xa-old-duplicate": {
				ID: "xa-old-duplicate", TenantID: "tenant-a", DICOMModality: "PACS_QUERY", Modalities: []string{"XA"},
				ModelName: "CoroModel", ModelVersion: "v1", ContainerID: "coro-v1",
				Status: entity.InferenceIngestionJobStatusRunning, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			},
			"xa-second": {
				ID: "xa-second", TenantID: "tenant-a", DICOMModality: "PACS_QUERY", Modalities: []string{"XA"},
				ModelName: "SecondModel", ModelVersion: "v1", ContainerID: "second",
				Status: entity.InferenceIngestionJobStatusRunning, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
			},
			"us-model": {
				ID: "us-model", TenantID: "tenant-a", DICOMModality: "PACS_QUERY", Modalities: []string{"US"},
				ModelName: "EchoModel", Status: entity.InferenceIngestionJobStatusRunning, UpdatedAt: now,
			},
			"stopped-xa": {
				ID: "stopped-xa", TenantID: "tenant-a", DICOMModality: "PACS_QUERY", Modalities: []string{"XA"},
				ModelName: "StoppedModel", Status: entity.InferenceIngestionJobStatusStopped, UpdatedAt: now,
			},
			"viewer-xa": {
				ID: "viewer-xa", TenantID: "tenant-a", DICOMModality: viewerIngestionJobSource, Modalities: []string{"XA"},
				ModelName: "ViewerModel", Status: entity.InferenceIngestionJobStatusRunning, UpdatedAt: now,
			},
			"other-tenant": {
				ID: "other-tenant", TenantID: "tenant-b", DICOMModality: "PACS_QUERY", Modalities: []string{"XA"},
				ModelName: "ForeignModel", Status: entity.InferenceIngestionJobStatusRunning, UpdatedAt: now,
			},
		},
		runs: map[string]entity.InferenceIngestionProcessingRun{}, executions: map[string]entity.InferenceIngestionProcessingJob{},
	}
}

func TestBootstrapLocalManualStudyCandidatesCreatesOnlyCurrentCompatibleModels(t *testing.T) {
	state := manualBootstrapState()
	orthanc := &manualBootstrapOrthancAPI{resources: []orthancAPITypes.GetLocalResourceResponse{localXAStudyResource()}}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		OrthancAPIInterface: orthanc,
	}

	candidates, err := service.bootstrapLocalManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")

	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Len(t, state.candidates, 2)
	jobIDs := map[string]bool{}
	for _, candidate := range candidates {
		jobIDs[candidate.IngestionJobID] = true
		require.Equal(t, entity.InferenceIngestionCandidateStatusRetrieved, candidate.Status)
		require.Equal(t, "XA\\SR", *candidate.ModalitiesInStudy)
		require.Equal(t, 46, *candidate.InstanceCount)
	}
	require.Equal(t, map[string]bool{"xa-new": true, "xa-second": true}, jobIDs)
	require.Len(t, orthanc.requests, 1)
	require.ElementsMatch(t, manualReprocessCandidateTags, orthanc.requests[0].RequestedTags)
}

func TestManualReprocessJobIsNewerIgnoresRuntimeUpdatesAndBreaksTies(t *testing.T) {
	now := time.Now().UTC()
	current := entity.InferenceIngestionJob{ID: "job-a", CreatedAt: now, UpdatedAt: now.Add(-time.Hour)}
	olderButRecentlyRun := entity.InferenceIngestionJob{ID: "job-z", CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	equalCreationWithHigherID := entity.InferenceIngestionJob{ID: "job-b", CreatedAt: now, UpdatedAt: now.Add(-2 * time.Hour)}

	require.False(t, manualReprocessJobIsNewer(olderButRecentlyRun, current))
	require.True(t, manualReprocessJobIsNewer(equalCreationWithHigherID, current))
}

func TestBootstrapLocalManualStudyCandidatesIsIdempotent(t *testing.T) {
	state := manualBootstrapState()
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		OrthancAPIInterface: &manualBootstrapOrthancAPI{resources: []orthancAPITypes.GetLocalResourceResponse{localXAStudyResource()}},
	}

	first, err := service.bootstrapLocalManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)
	second, err := service.bootstrapLocalManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)

	require.Len(t, state.candidates, 2)
	require.ElementsMatch(t, []string{first[0].ID, first[1].ID}, []string{second[0].ID, second[1].ID})
}

func TestBootstrapLocalManualStudyCandidatesFailsSafelyWithoutLocalStudyOrCompatibleModel(t *testing.T) {
	t.Run("study unavailable", func(t *testing.T) {
		state := manualBootstrapState()
		service := &InferenceCommandService{
			InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
			OrthancAPIInterface: &manualBootstrapOrthancAPI{err: errors.New(apiError.MissingRecord)},
		}
		_, err := service.bootstrapLocalManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
		require.EqualError(t, err, apiError.MissingRecord)
		require.Empty(t, state.candidates)
	})

	t.Run("no modality match", func(t *testing.T) {
		state := manualBootstrapState()
		resource := localXAStudyResource()
		resource.RequestedTags["ModalitiesInStudy"] = "CT"
		service := &InferenceCommandService{
			InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
			OrthancAPIInterface: &manualBootstrapOrthancAPI{resources: []orthancAPITypes.GetLocalResourceResponse{resource}},
		}
		_, err := service.bootstrapLocalManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
		require.EqualError(t, err, apiError.MissingRecord)
		require.Empty(t, state.candidates)
	})
}

func TestCreateManualStudyProcessingRunBootstrapsHistoricalLocalStudy(t *testing.T) {
	state := manualBootstrapState()
	delete(state.jobs, "xa-second")
	state.dispatchUpdates = make(chan string, 1)
	dispatchCalls := make(chan serviceTypes.DispatchStudyRequest, 1)
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            &recordingInferenceQuotaManager{},
		OrthancAPIInterface:                       &manualBootstrapOrthancAPI{resources: []orthancAPITypes.GetLocalResourceResponse{localXAStudyResource()}},
		ProcessingDispatcherInterface: &guardedProcessingDispatcher{
			dispatchCall: dispatchCalls, response: serviceTypes.DispatchStudyResponse{JobID: "study-service-job"},
			echoManualCorrelation: true,
		},
		StudyServiceDispatchSemaphore: make(chan struct{}, 1),
	}
	configureReadyInferenceContainer(service)
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerManualReprocess, result.Run.RunTrigger)
	require.Len(t, result.Executions, 1)
	for range result.Executions {
		select {
		case <-state.dispatchUpdates:
		case <-time.After(time.Second):
			t.Fatal("bootstrapped execution was not dispatched")
		}
	}
	require.Len(t, state.candidates, 1)
}
