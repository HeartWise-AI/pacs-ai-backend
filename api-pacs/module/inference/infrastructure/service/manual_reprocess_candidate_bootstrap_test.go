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
	orthancApplication "api-pacs/module/orthanc/application"
	orthancServiceTypes "api-pacs/module/orthanc/infrastructure/service/types"
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

type manualBootstrapOrthancQuery struct {
	orthancApplication.OrthancQueryServiceInterface
	studies  map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse
	err      error
	requests []orthancServiceTypes.FindModalityStudies
}

func (query *manualBootstrapOrthancQuery) FindModalityStudies(
	_ context.Context,
	request orthancServiceTypes.FindModalityStudies,
) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error) {
	query.requests = append(query.requests, request)
	return query.studies[request.ModalityID], "query-1", query.err
}

func remoteXAStudy() orthancAPITypes.QueryModalityStudyAnswersResponse {
	return orthancAPITypes.QueryModalityStudyAnswersResponse{
		StudyInstanceUID: "1.2.3", ModalitiesInStudy: "XA", StudyDate: "20241023", StudyTime: "101500",
		PatientID: "patient-1", AccessionNumber: "accession-1",
		NumberOfStudyRelatedSeries: "3", NumberOfStudyRelatedInstances: "30",
	}
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

func TestReconcileExistingManualStudyCandidatesAddsNewModelsAndReplacesOldJob(t *testing.T) {
	state := manualBootstrapState()
	modalities := "XA"
	lastSeen := time.Now().UTC().Add(-time.Hour)
	state.candidates["old-candidate"] = entity.InferenceIngestionCandidate{
		ID: "old-candidate", TenantID: "tenant-a", IngestionJobID: "xa-old-duplicate",
		StudyInstanceUID: "1.2.3", ModalitiesInStudy: &modalities,
		Status: entity.InferenceIngestionCandidateStatusRetrieved, LastSeenAt: lastSeen,
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
	}

	first, err := service.reconcileExistingManualStudyCandidates(
		context.Background(), "tenant-a", "1.2.3", []entity.InferenceIngestionCandidate{state.candidates["old-candidate"]},
	)
	require.NoError(t, err)
	require.Len(t, first, 2)

	jobIDs := make(map[string]bool, len(first))
	firstIDs := make([]string, 0, len(first))
	for _, candidate := range first {
		jobIDs[candidate.IngestionJobID] = true
		firstIDs = append(firstIDs, candidate.ID)
		require.Equal(t, entity.InferenceIngestionCandidateStatusDiscovered, candidate.Status)
	}
	require.Equal(t, map[string]bool{"xa-new": true, "xa-second": true}, jobIDs)
	require.False(t, jobIDs["xa-old-duplicate"])

	second, err := service.reconcileExistingManualStudyCandidates(context.Background(), "tenant-a", "1.2.3", first)
	require.NoError(t, err)
	secondIDs := make([]string, 0, len(second))
	for _, candidate := range second {
		secondIDs = append(secondIDs, candidate.ID)
	}
	require.ElementsMatch(t, firstIDs, secondIDs)
	require.Len(t, state.candidates, 3, "the obsolete candidate remains auditable but is not selected")
}

func TestCreateManualStudyProcessingRunReconcilesCurrentModelSet(t *testing.T) {
	state := manualBootstrapState()
	modalities := "XA"
	state.candidates["old-candidate"] = entity.InferenceIngestionCandidate{
		ID: "old-candidate", TenantID: "tenant-a", IngestionJobID: "xa-old-duplicate",
		StudyInstanceUID: "1.2.3", ModalitiesInStudy: &modalities,
		Status: entity.InferenceIngestionCandidateStatusRetrieved, LastSeenAt: time.Now().UTC(),
	}
	state.dispatchUpdates = make(chan string, 2)
	dispatchCalls := make(chan serviceTypes.DispatchStudyRequest, 2)
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            &recordingInferenceQuotaManager{},
		OrthancAPIInterface: &manualBootstrapOrthancAPI{
			resources: []orthancAPITypes.GetLocalResourceResponse{localXAStudyResource()},
		},
		ProcessingDispatcherInterface: &guardedProcessingDispatcher{
			dispatchCall: dispatchCalls, response: serviceTypes.DispatchStudyResponse{JobID: "study-service-job"},
			echoManualCorrelation: true,
		},
		// This test verifies candidate reconciliation, not concurrent dispatch.
		// Serialize the asynchronous dispatches because the shared in-memory
		// repository and dispatcher test doubles are intentionally lock-free.
		StudyServiceDispatchSemaphore: make(chan struct{}, 1),
	}
	configureReadyInferenceContainer(service)
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})
	require.NoError(t, err)
	require.Len(t, result.Executions, 2)

	selectedJobIDs := make(map[string]bool, 2)
	for _, execution := range result.Executions {
		candidate := state.candidates[execution.CandidateID]
		selectedJobIDs[candidate.IngestionJobID] = true
		select {
		case <-state.dispatchUpdates:
		case <-time.After(time.Second):
			t.Fatal("reconciled execution was not dispatched")
		}
	}
	require.Equal(t, map[string]bool{"xa-new": true, "xa-second": true}, selectedJobIDs)
	require.False(t, selectedJobIDs["xa-old-duplicate"])
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

func TestBootstrapRemoteManualStudyCandidatesCreatesDiscoveredCompatibleCandidates(t *testing.T) {
	state := manualBootstrapState()
	query := &manualBootstrapOrthancQuery{studies: map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{
		"PACS_QUERY": {remoteXAStudy()},
	}}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		OrthancQueryServiceInterface: query,
	}

	candidates, err := service.bootstrapRemoteManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")

	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Len(t, state.candidates, 2)
	for _, candidate := range candidates {
		require.Equal(t, entity.InferenceIngestionCandidateStatusDiscovered, candidate.Status)
		require.Equal(t, 30, *candidate.InstanceCount)
	}
	require.Len(t, query.requests, 1)
	require.Equal(t, "1.2.3", query.requests[0].StudyInstanceUID)
}

func TestBootstrapRemoteManualStudyCandidatesIsIdempotent(t *testing.T) {
	state := manualBootstrapState()
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		OrthancQueryServiceInterface: &manualBootstrapOrthancQuery{studies: map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{
			"PACS_QUERY": {remoteXAStudy()},
		}},
	}

	first, err := service.bootstrapRemoteManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)
	second, err := service.bootstrapRemoteManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)

	require.Len(t, state.candidates, 2)
	require.ElementsMatch(t, []string{first[0].ID, first[1].ID}, []string{second[0].ID, second[1].ID})
}

func TestBootstrapRemoteManualStudyCandidatesRejectsWrongOrAmbiguousMatches(t *testing.T) {
	for name, studies := range map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{
		"wrong UID":      {{StudyInstanceUID: "9.9.9", ModalitiesInStudy: "XA"}},
		"ambiguous":      {remoteXAStudy(), remoteXAStudy()},
		"wrong modality": {{StudyInstanceUID: "1.2.3", ModalitiesInStudy: "CT"}},
	} {
		t.Run(name, func(t *testing.T) {
			state := manualBootstrapState()
			service := &InferenceCommandService{
				InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
				OrthancQueryServiceInterface: &manualBootstrapOrthancQuery{studies: map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{
					"PACS_QUERY": studies,
				}},
			}
			_, err := service.bootstrapRemoteManualStudyCandidates(context.Background(), "tenant-a", "1.2.3")
			require.EqualError(t, err, apiError.MissingRecord)
			require.Empty(t, state.candidates)
		})
	}
}

func TestCreateManualStudyProcessingRunBootstrapsRemoteStudyAndQueuesRetrieval(t *testing.T) {
	state := manualBootstrapState()
	delete(state.jobs, "xa-second")
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            &recordingInferenceQuotaManager{},
		OrthancAPIInterface:                       &manualBootstrapOrthancAPI{err: errors.New(apiError.MissingRecord)},
		OrthancQueryServiceInterface: &manualBootstrapOrthancQuery{studies: map[string][]orthancAPITypes.QueryModalityStudyAnswersResponse{
			"PACS_QUERY": {remoteXAStudy()},
		}},
	}
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})

	require.NoError(t, err)
	require.Len(t, result.Executions, 1)
	require.Len(t, state.candidates, 1)
	for _, candidate := range state.candidates {
		require.Equal(t, entity.InferenceIngestionCandidateStatusRetrievalQueued, candidate.Status)
		require.Equal(t, "queued", *candidate.LastRetrievalState)
	}
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
