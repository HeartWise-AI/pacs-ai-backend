package repository

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func TestListWorklistStudyStatusesBuildsTenantScopedCurrentStudySnapshot(t *testing.T) {
	now := time.Now().UTC()
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.NotContains(t, query, "::", "sqlx named queries corrupt PostgreSQL double-colon casts")
		require.Contains(t, query, "CAST('[]' AS jsonb)")
		require.Contains(t, query, "CAST(COUNT(jobs.id) AS int)")
		require.Contains(t, query, "candidates.tenant_id = :tenant_id")
		require.Contains(t, query, "DISTINCT ON (candidates.study_instance_uid)")
		require.Contains(t, query, "LEFT JOIN LATERAL")
		require.Contains(t, query, "selected_run.tenant_id = :tenant_id")
		require.Contains(t, query, "(selected_run.phase <> 'TERMINAL') DESC")
		require.Contains(t, query, "jobs.processing_run_id = runs.id")
		require.Contains(t, query, "jobs.status IN ('pending', 'queued', 'running')")
		require.Contains(t, query, "ORDER BY updated_at DESC, candidates.study_instance_uid ASC")
		require.Contains(t, query, "LIMIT :limit OFFSET :offset")

		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, 3, arguments["limit"])
		require.Equal(t, 10, arguments["offset"])
		require.NotContains(t, arguments, "study_instance_uids")

		statuses := target.(*[]types.WorklistStudyStatus)
		*statuses = append(*statuses,
			types.WorklistStudyStatus{StudyInstanceUID: "study-3", RunID: stringPointer("run-3"), UpdatedAt: now},
			types.WorklistStudyStatus{StudyInstanceUID: "study-2", UpdatedAt: now.Add(-time.Minute)},
			types.WorklistStudyStatus{StudyInstanceUID: "study-1", UpdatedAt: now.Add(-2 * time.Minute)},
		)
		return nil
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	page, err := repository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID: "tenant-a", Limit: 2, Offset: 10,
	})

	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Equal(t, []string{"study-3", "study-2"}, []string{
		page.Studies[0].StudyInstanceUID, page.Studies[1].StudyInstanceUID,
	})
}

func TestListWorklistStudyStatusesFiltersVisibleStudyUIDs(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "candidates.study_instance_uid = ANY(:study_instance_uids)")
		arguments := model.(map[string]interface{})
		uids, ok := arguments["study_instance_uids"].(*pq.StringArray)
		require.True(t, ok)
		require.Equal(t, pq.StringArray{"study-a", "study-b"}, *uids)
		return nil
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	page, err := repository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID: "tenant-a", StudyInstanceUIDs: []string{"study-a", "study-b"}, Limit: 25,
	})

	require.NoError(t, err)
	require.Empty(t, page.Studies)
	require.False(t, page.HasMore)
}

func TestListWorklistStudyStatusesPreservesRetrievalOnlyStudy(t *testing.T) {
	now := time.Now().UTC()
	handler := &processingRunTestHandler{}
	handler.query = func(_ string, _ interface{}, target interface{}) error {
		statuses := target.(*[]types.WorklistStudyStatus)
		*statuses = append(*statuses, types.WorklistStudyStatus{
			StudyInstanceUID: "study-retrieving",
			IngestionStatus:  entity.InferenceIngestionCandidateStatusRetrievalQueued,
			RetrievalState:   stringPointer("RUNNING"),
			UpdatedAt:        now,
		})
		return nil
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	page, err := repository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID: "tenant-a", Limit: 25,
	})

	require.NoError(t, err)
	require.Len(t, page.Studies, 1)
	require.Nil(t, page.Studies[0].RunID)
	require.Equal(t, entity.InferenceIngestionCandidateStatusRetrievalQueued, page.Studies[0].IngestionStatus)
}

func TestListWorklistStudyStatusesMapsDatabaseFailures(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(_ string, _ interface{}, _ interface{}) error {
		return errors.New("query failed")
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID: "tenant-a", Limit: 25,
	})

	require.EqualError(t, err, apiError.DatabaseError)
}

func TestWorklistStatusQueryDoesNotSelectDetailedInferenceResults(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, _ interface{}, _ interface{}) error {
		require.NotContains(t, strings.ToLower(query), "result_json")
		require.NotContains(t, strings.ToLower(query), "study_service_job_id")
		return nil
	}

	repository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID: "tenant-a", Limit: 25,
	})
	require.NoError(t, err)
}

func stringPointer(value string) *string {
	return &value
}
