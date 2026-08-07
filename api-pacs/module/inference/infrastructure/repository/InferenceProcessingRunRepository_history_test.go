package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func TestListProcessingRunHistoryPageUsesLimitPlusOne(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "tenant_id = :tenant_id")
		require.Contains(t, query, "study_instance_uid = :study_instance_uid")
		require.Contains(t, query, "ORDER BY run_number DESC")
		arguments := model.(types.ListInferenceIngestionProcessingRuns)
		require.Equal(t, "tenant-a", arguments.TenantID)
		require.Equal(t, "1.2.3", arguments.StudyInstanceUID)
		require.Equal(t, 3, arguments.Limit)
		require.Equal(t, 10, arguments.Offset)

		runs := target.(*[]entity.InferenceIngestionProcessingRun)
		*runs = append(*runs,
			entity.InferenceIngestionProcessingRun{ID: "run-3", RunNumber: 3},
			entity.InferenceIngestionProcessingRun{ID: "run-2", RunNumber: 2},
			entity.InferenceIngestionProcessingRun{ID: "run-1", RunNumber: 1},
		)
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	page, err := repository.ListProcessingRunHistoryPage(context.Background(), types.ListInferenceIngestionProcessingRuns{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Limit: 2, Offset: 10,
	})

	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Equal(t, []string{"run-3", "run-2"}, []string{page.Runs[0].ID, page.Runs[1].ID})
}

func TestListProcessingRunHistoryPageReturnsEmptyNonNilRuns(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(_ string, _ interface{}, _ interface{}) error { return nil }
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	page, err := repository.ListProcessingRunHistoryPage(context.Background(), types.ListInferenceIngestionProcessingRuns{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Limit: 25,
	})

	require.NoError(t, err)
	require.NotNil(t, page.Runs)
	require.Empty(t, page.Runs)
	require.False(t, page.HasMore)
}

func TestListProcessingRunExecutionsByRunIDsIsTenantScopedAndDeterministic(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "JOIN ingestion_processing_runs runs")
		require.Contains(t, query, "runs.tenant_id = :tenant_id")
		require.Contains(t, query, "runs.id = ANY(:processing_run_ids)")
		require.Contains(t, query, "ORDER BY runs.run_number DESC, jobs.created_at ASC, jobs.id ASC")

		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		runIDs, ok := arguments["processing_run_ids"].(*pq.StringArray)
		require.True(t, ok)
		require.Equal(t, pq.StringArray{"run-3", "run-2"}, *runIDs)

		run3 := "run-3"
		run2 := "run-2"
		executions := target.(*[]entity.InferenceIngestionProcessingJob)
		*executions = append(*executions,
			entity.InferenceIngestionProcessingJob{ID: "execution-3", ProcessingRunID: &run3},
			entity.InferenceIngestionProcessingJob{ID: "execution-2", ProcessingRunID: &run2},
		)
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	executions, err := repository.ListProcessingRunExecutionsByRunIDs(context.Background(), types.ListInferenceIngestionProcessingRunExecutions{
		TenantID: "tenant-a", ProcessingRunIDs: []string{"run-3", "run-2"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"execution-3", "execution-2"}, []string{executions[0].ID, executions[1].ID})
}

func TestListProcessingRunExecutionsByRunIDsSkipsQueryForEmptyPage(t *testing.T) {
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{}}

	executions, err := repository.ListProcessingRunExecutionsByRunIDs(context.Background(), types.ListInferenceIngestionProcessingRunExecutions{
		TenantID: "tenant-a",
	})

	require.NoError(t, err)
	require.NotNil(t, executions)
	require.Empty(t, executions)
}

func TestListProcessingRunExecutionsByRunIDsMapsDatabaseFailure(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(_ string, _ interface{}, _ interface{}) error { return errors.New("query failed") }
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	_, err := repository.ListProcessingRunExecutionsByRunIDs(context.Background(), types.ListInferenceIngestionProcessingRunExecutions{
		TenantID: "tenant-a", ProcessingRunIDs: []string{"run-1"},
	})

	require.EqualError(t, err, apiError.DatabaseError)
}
