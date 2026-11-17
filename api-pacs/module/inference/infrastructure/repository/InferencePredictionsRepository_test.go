package repository

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func TestPredictionExamsQueryBindsEveryUserValue(t *testing.T) {
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	hostile := "x' OR '1'='1"
	query, args, err := buildListPredictionExamsQuery(types.ListPredictionExams{
		TenantID: "tenant-a", Scope: types.PredictionsScopeRequestedBy, RequestedByUserID: hostile,
		ModelName: hostile, Modality: "xa", Search: "50%_off\\", Status: types.PredictionsExamStatusAttention,
		From: &from, To: &to, Limit: 25, Offset: 50,
	})
	require.NoError(t, err)
	require.NotContains(t, query, hostile)
	require.NotContains(t, query, "50%")
	require.Contains(t, query, "r.tenant_id = :tenant_id")
	require.Contains(t, query, "scope_runs.tenant_id = :tenant_id AND scope_runs.requested_by_user_id = :requested_by_user_id")
	require.Contains(t, query, "AND exams.any_attention")
	require.Contains(t, query, "AND exams.last_activity_at < :to")
	require.Contains(t, query, "ORDER BY exams.last_activity_at DESC, exams.study_instance_uid DESC")
	require.Equal(t, `%50\%\_off\\%`, args["search_pattern"])
	require.Equal(t, "XA", args["modality"])
	require.Equal(t, hostile, args["requested_by_user_id"])
	require.Equal(t, "tenant-a", args["tenant_id"])

	_, _, err = buildListPredictionExamsQuery(types.ListPredictionExams{TenantID: "t", Scope: "everyone", Limit: 1})
	require.EqualError(t, err, apiError.InvalidPayload)
	_, _, err = buildListPredictionExamsQuery(types.ListPredictionExams{TenantID: "t", Scope: types.PredictionsScopeAll, Status: "x", Limit: 1})
	require.EqualError(t, err, apiError.InvalidPayload)
}

func TestPredictionExamsQueryKeepsStalledModelsOutOfActiveAndInAttention(t *testing.T) {
	stalledBefore := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	query, args, err := buildListPredictionExamsQuery(types.ListPredictionExams{
		TenantID: "tenant-a", Scope: types.PredictionsScopeAll, StalledBefore: stalledBefore,
		Status: types.PredictionsExamStatusActive, Limit: 25,
	})
	require.NoError(t, err)
	require.Equal(t, stalledBefore, args["stalled_before"])
	// Same rule as predictionExecutionState: pending or queued, no
	// study-service job, run created before the limit.
	require.Contains(t, query, `(CAST(j.status AS text) IN ('pending', 'queued')
					AND NULLIF(BTRIM(j.study_service_job_id), '') IS NULL
					AND r.created_at < :stalled_before) AS stalled`)
	require.Contains(t, query, `BOOL_OR(latest.status IN ('pending', 'queued', 'running') AND NOT latest.stalled) AS any_active`)
	require.Contains(t, query, `BOOL_OR((latest.status IN ('failed', 'skipped') AND NOT latest.expired) OR latest.stalled) AS any_attention`)
	require.Contains(t, query, "AND exams.any_active")
}

func TestPredictionExamActivityComesFromRealEventsOnly(t *testing.T) {
	query, _, err := buildListPredictionExamsQuery(types.ListPredictionExams{
		TenantID: "tenant-a", Scope: types.PredictionsScopeAll, Limit: 25,
	})
	require.NoError(t, err)
	require.Contains(t, query, "COALESCE(j.completed_at, j.started_at, r.created_at) AS activity_at")
	require.NotContains(t, query, "UNION", "no date filter, no activity pre-filter")
	require.NotContains(t, query, "r.updated_at")
	require.NotContains(t, query, "j.updated_at")

	from := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	query, args, err := buildListPredictionExamsQuery(types.ListPredictionExams{
		TenantID: "tenant-a", Scope: types.PredictionsScopeAutomatic, From: &from, Limit: 25,
	})
	require.NoError(t, err)
	require.Equal(t, from, args["from"])
	// Index-friendly pre-filter: runs (tenant_id, created_at) and executions
	// (tenant_id, COALESCE(completed_at, started_at)), each with the scope.
	require.Contains(t, query, "WHERE r.tenant_id = :tenant_id AND r.created_at >= :from AND r.study_instance_uid IN")
	require.Contains(t, query, "AND COALESCE(activity_jobs.completed_at, activity_jobs.started_at) >= :from AND r.study_instance_uid IN")
	require.Contains(t, query, "UNION")
	require.Contains(t, query, "AND exams.last_activity_at >= :from")
	require.Contains(t, query, "ORDER BY exams.last_activity_at DESC, exams.study_instance_uid DESC")
	require.NotContains(t, query, "updated_at >= :from")
	require.NotContains(t, query, "r.updated_at")
}

func TestPredictionExamsRequireTenantAndRequester(t *testing.T) {
	repository := InferencePredictionsRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{}}
	for _, data := range []types.ListPredictionExams{
		{Scope: types.PredictionsScopeAll, Limit: 1},
		{TenantID: "t", Scope: types.PredictionsScopeRequestedBy, Limit: 1},
		{TenantID: "t", Scope: types.PredictionsScopeAll},
	} {
		_, err := repository.ListPredictionExams(context.Background(), data)
		require.EqualError(t, err, apiError.InvalidPayload)
	}
}

func TestPredictionExecutionsAreTenantScoped(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "r.tenant_id = :tenant_id AND r.study_instance_uid = ANY(:study_instance_uids)")
		require.Contains(t, query, "j.tenant_id = :tenant_id")
		require.Contains(t, query, "c.tenant_id = :tenant_id")
		require.Contains(t, query, "AND r.run_trigger IN ('AUTO', 'LEGACY_IMPORT')")
		require.Contains(t, query, "DISTINCT ON (r.study_instance_uid, j.model_name)")
		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, pq.StringArray{"1.2"}, *arguments["study_instance_uids"].(*pq.StringArray))
		return nil
	}
	repository := InferencePredictionsRepository{PostgresSQLDBHandlerInterface: handler}
	rows, err := repository.ListPredictionExecutions(context.Background(), types.ListPredictionExecutions{
		TenantID: "tenant-a", StudyInstanceUIDs: []string{"1.2"}, LatestOnly: true, LaunchedBy: types.PredictionsScopeAutomatic,
	})
	require.NoError(t, err)
	require.NotNil(t, rows)

	empty, err := repository.ListPredictionExecutions(context.Background(), types.ListPredictionExecutions{TenantID: "tenant-a"})
	require.NoError(t, err)
	require.Empty(t, empty)
	_, err = repository.ListPredictionExecutions(context.Background(), types.ListPredictionExecutions{StudyInstanceUIDs: []string{"1.2"}})
	require.EqualError(t, err, apiError.InvalidPayload)
}
