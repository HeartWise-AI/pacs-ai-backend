package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestWorklistStudyStatusContractRepresentsRetrievalBeforeRun(t *testing.T) {
	status := WorklistStudyStatus{
		StudyInstanceUID: "1.2.3",
		IngestionStatus:  entity.InferenceIngestionCandidateStatusRetrievalQueued,
		AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{},
		UpdatedAt:        time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(status)

	require.NoError(t, err)
	require.JSONEq(t, `{
		"studyInstanceUID":"1.2.3",
		"ingestionStatus":"RETRIEVAL_QUEUED",
		"retrievalState":null,
		"retrievalError":null,
		"runId":null,
		"runNumber":null,
		"trigger":null,
		"phase":null,
		"outcome":null,
		"attentionRequired":false,
		"attentionReasons":[],
		"expectedModels":0,
		"pendingModels":0,
		"queuedModels":0,
		"runningModels":0,
		"completedModels":0,
		"failedModels":0,
		"skippedModels":0,
		"cancelledModels":0,
		"activeModels":0,
		"version":null,
		"startedAt":null,
		"completedAt":null,
		"updatedAt":"2026-08-06T12:00:00Z"
	}`, string(payload))
}

func TestProcessingRunDetailContractExcludesPythonResults(t *testing.T) {
	result := ProcessingRunDetail{
		ProcessingRunSummary: ProcessingRunSummary{
			RunID: "run-1", StudyInstanceUID: "1.2.3", RunNumber: 2,
			Trigger:             entity.InferenceIngestionProcessingRunTriggerManualReprocess,
			Phase:               entity.InferenceIngestionProcessingRunPhaseProcessing,
			AttentionReasons:    entity.InferenceIngestionProcessingRunAttentionReasons{},
			ProcessingRunCounts: ProcessingRunCounts{Expected: 1, Running: 1, Active: 1},
			Version:             4,
			CreatedAt:           time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC),
			UpdatedAt:           time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		},
		Executions: []ProcessingRunExecutionSummary{{
			ExecutionID: "execution-1",
			ModelName:   "EchoPrime",
			Status:      entity.InferenceIngestionProcessingJobStatusRunning,
			UpdatedAt:   time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		}},
	}

	payload, err := json.Marshal(result)

	require.NoError(t, err)
	require.Contains(t, string(payload), `"runId":"run-1"`)
	require.Contains(t, string(payload), `"modelName":"EchoPrime"`)
	require.NotContains(t, string(payload), "resultJson")
	require.NotContains(t, string(payload), "studyServiceJobId")
	require.NotContains(t, string(payload), "tenantId")
}

func TestWorklistPagesExposeBoundariesAndHasMore(t *testing.T) {
	page := WorklistStudyStatusPage{
		Studies:      []WorklistStudyStatus{},
		WorklistPage: WorklistPage{Limit: 25, Offset: 50, HasMore: true},
	}

	payload, err := json.Marshal(page)

	require.NoError(t, err)
	require.JSONEq(t, `{"studies":[],"limit":25,"offset":50,"hasMore":true}`, string(payload))
}
