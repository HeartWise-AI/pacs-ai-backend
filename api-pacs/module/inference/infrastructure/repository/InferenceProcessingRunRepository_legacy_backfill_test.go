package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func TestListLegacyProcessingRunBackfillRowsUsesReadOnlyMinimalProjection(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		normalized := strings.ToLower(query)
		require.Contains(t, normalized, "where jobs.processing_run_id is null")
		require.Contains(t, normalized, "exists (")
		require.Contains(t, normalized, "join ingestion_candidates")
		require.Contains(t, normalized, "order by jobs.tenant_id, candidates.study_instance_uid")
		require.NotContains(t, normalized, "patient_id")
		require.NotContains(t, normalized, "accession_number")
		require.NotContains(t, normalized, "result_json")
		require.NotContains(t, normalized, "insert ")
		require.NotContains(t, normalized, "update ")
		require.Empty(t, model.(map[string]interface{}))

		rows := target.(*[]types.LegacyProcessingRunBackfillRow)
		*rows = append(*rows, types.LegacyProcessingRunBackfillRow{
			ExecutionID: "execution-1", CandidateID: "candidate-1",
			ExecutionTenantID: "tenant-a", CandidateTenantID: "tenant-a",
			StudyInstanceUID: "study-1", ModelName: "model-one",
			Status: entity.InferenceIngestionProcessingJobStatusCompleted,
		})
		return nil
	}
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	rows, err := repository.ListLegacyProcessingRunBackfillRows(context.Background())

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "execution-1", rows[0].ExecutionID)
}

func TestListLegacyProcessingRunBackfillRowsMapsDatabaseFailure(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(string, interface{}, interface{}) error { return errors.New("query failed") }
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	_, err := repository.ListLegacyProcessingRunBackfillRows(context.Background())

	require.EqualError(t, err, apiError.DatabaseError)
}
