package repository

import (
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestSelectProcessingJobByCandidateModelPrefersLatestRun(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "LEFT JOIN ingestion_processing_runs")
		require.Contains(t, query, "runs.run_number DESC NULLS LAST")
		require.Contains(t, query, "jobs.updated_at DESC")
		require.Contains(t, query, "LIMIT 1")
		arguments := model.(map[string]interface{})
		require.Equal(t, "candidate-1", arguments["candidate_id"])
		require.Equal(t, "model-one", arguments["model_name"])
		*target.(*entity.InferenceIngestionProcessingJob) = entity.InferenceIngestionProcessingJob{ID: "latest-execution"}
		return nil
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	execution, err := repository.SelectInferenceIngestionProcessingJobByCandidateModel("candidate-1", "model-one")

	require.NoError(t, err)
	require.Equal(t, "latest-execution", execution.ID)
}
