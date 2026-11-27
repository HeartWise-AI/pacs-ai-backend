package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func jobResultFixture(modelName, status string, tenantID string) *executionResultDispatcher {
	version := "1.0.0"
	completed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &executionResultDispatcher{found: true, job: serviceTypes.StudyServiceJobResult{
		StudyServiceJob: serviceTypes.StudyServiceJob{
			JobID: "job-1", StudyInstanceUID: "1.2.3", TenantID: &tenantID,
			ModelName: modelName, ModelVersion: &version, Status: status, CompletedAt: &completed,
		},
		ResultJSON: json.RawMessage(`{"summary":{"avVmax":{"value":4.2,"unit":"m/s"}}}`),
	}}
}

func TestGetStudyJobResultReturnsStoredResultOfOwnedCompletedJob(t *testing.T) {
	service, _, _ := viewerResultsService()
	service.ProcessingResultProviderInterface = jobResultFixture("echo-sr-measurements", "completed", "tenant-a")

	result, err := service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1",
	})

	require.NoError(t, err)
	require.Equal(t, "job-1", result.StudyServiceJobID)
	require.Equal(t, "1.2.3", result.StudyInstanceUID)
	require.Equal(t, "echo-sr-measurements", result.ModelName)
	require.Equal(t, "1.0.0", *result.ModelVersion)
	require.Equal(t, "completed", result.Status)
	require.NotNil(t, result.CompletedAt)
	require.JSONEq(t, `{"summary":{"avVmax":{"value":4.2,"unit":"m/s"}}}`, string(result.Result))
}

func TestGetStudyJobResultRejectsOtherStudyOrTenant(t *testing.T) {
	service, _, _ := viewerResultsService()
	service.ProcessingResultProviderInterface = jobResultFixture("echo-sr-measurements", "completed", "tenant-b")
	_, err := service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1",
	})
	require.EqualError(t, err, apiError.MissingRecord)

	service.ProcessingResultProviderInterface = jobResultFixture("echo-sr-measurements", "completed", "tenant-a")
	_, err = service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "9.9.9", StudyServiceJobID: "job-1",
	})
	require.EqualError(t, err, apiError.MissingRecord)

	_, err = service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "",
	})
	require.EqualError(t, err, apiError.InvalidPayload)
}

func TestGetStudyJobResultRequiresCompletedJob(t *testing.T) {
	service, _, _ := viewerResultsService()
	service.ProcessingResultProviderInterface = jobResultFixture("echo-sr-measurements", "running", "tenant-a")

	_, err := service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1",
	})

	require.EqualError(t, err, apiError.InferenceExecutionResultNotAvailable)
}

func TestGetStudyJobResultLetsRestrictedRolesReadMeasurementsButNotUnknownModels(t *testing.T) {
	service, _, _ := viewerResultsService()
	denyAll := func(string) bool { return false }

	service.ProcessingResultProviderInterface = jobResultFixture("echo-sr-measurements", "completed", "tenant-a")
	_, err := service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1", AllowModel: denyAll,
	})
	require.NoError(t, err)

	service.ProcessingResultProviderInterface = jobResultFixture("Unknown-Model", "completed", "tenant-a")
	_, err = service.GetStudyJobResult(context.Background(), serviceTypes.GetStudyJobResult{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1", AllowModel: denyAll,
	})
	require.EqualError(t, err, apiError.ForbiddenAccess)
}
