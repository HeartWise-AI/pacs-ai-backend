package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type ingestionJobCaptureHandler struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
	statement string
	model     interface{}
}

func (handler *ingestionJobCaptureHandler) Execute(statement string, model interface{}) (sql.Result, error) {
	handler.statement, handler.model = statement, model
	return nil, nil
}

func (handler *ingestionJobCaptureHandler) Query(statement string, model interface{}, _ interface{}) error {
	handler.statement, handler.model = statement, model
	return nil
}

func (handler *ingestionJobCaptureHandler) QueryRow(statement string, model interface{}, _ interface{}) error {
	handler.statement, handler.model = statement, model
	return nil
}

func TestInsertInferenceIngestionJobPersistsCreatorAndSwitch(t *testing.T) {
	handler := &ingestionJobCaptureHandler{}
	repository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: handler}
	creator := "user-a"
	require.NoError(t, repository.InsertInferenceIngestionJob(types.CreateInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-a", Modalities: []string{"US"},
		ScheduleStartTimestamp: time.Unix(0, 0), ScheduleEndTimestamp: time.Unix(0, 0),
		Status: entity.InferenceIngestionJobStatusRunning, CreatedByUserID: &creator, NotificationsEnabled: false,
	}))
	require.Contains(t, handler.statement, "created_by_user_id, notifications_enabled)")
	require.Contains(t, handler.statement, ":created_by_user_id, :notifications_enabled)")
	job := handler.model.(entity.InferenceIngestionJob)
	require.Equal(t, &creator, job.CreatedByUserID)
	require.False(t, job.NotificationsEnabled)
}

func TestUpdateInferenceIngestionJobKeepsSwitchWhenAbsent(t *testing.T) {
	handler := &ingestionJobCaptureHandler{}
	repository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: handler}

	require.NoError(t, repository.UpdateInferenceIngestionJob(types.UpdateInferenceIngestionJob{ID: "job-1", Modalities: []string{"US"}}))
	require.Contains(t, handler.statement, "notifications_enabled = COALESCE(CAST(:notifications_enabled AS boolean), notifications_enabled)")
	params := handler.model.(map[string]interface{})
	require.Nil(t, params["notifications_enabled"])
	require.Equal(t, "job-1", params["id"])

	off := false
	require.NoError(t, repository.UpdateInferenceIngestionJob(types.UpdateInferenceIngestionJob{ID: "job-1", NotificationsEnabled: &off}))
	require.Equal(t, false, handler.model.(map[string]interface{})["notifications_enabled"])
}

func TestSelectInferenceIngestionJobsNamesItsColumns(t *testing.T) {
	handler := &ingestionJobCaptureHandler{}
	repository := &InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	_, _ = repository.SelectInferenceIngestionJobs(nil)
	require.NotContains(t, handler.statement, "SELECT *")
	require.Contains(t, handler.statement, "created_by_user_id, notifications_enabled FROM ingestion_jobs")
	_, _ = repository.SelectInferenceIngestionJobByID("job-1")
	require.NotContains(t, handler.statement, "SELECT *")
	require.Contains(t, handler.statement, "notifications_enabled FROM ingestion_jobs WHERE id = :id")
}

func TestSelectProcessingRunNotificationJobsJoinsRunToJobs(t *testing.T) {
	handler := &ingestionJobCaptureHandler{}
	repository := &InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	jobs, err := repository.SelectProcessingRunNotificationJobs(context.Background(), "tenant-a", "run-1")
	require.NoError(t, err)
	require.Empty(t, jobs)
	statement := strings.Join(strings.Fields(handler.statement), " ")
	require.Contains(t, statement, "SELECT DISTINCT ingestion_jobs.id AS ingestion_job_id")
	require.Contains(t, statement, "JOIN ingestion_candidates candidates ON candidates.id = executions.candidate_id")
	require.Contains(t, statement, "JOIN ingestion_jobs ON ingestion_jobs.id = candidates.ingestion_job_id")
	require.Contains(t, statement, "WHERE runs.tenant_id = :tenant_id AND runs.id = :processing_run_id")
	require.Equal(t, map[string]interface{}{"tenant_id": "tenant-a", "processing_run_id": "run-1"}, handler.model)
}

func TestIngestionJobNotificationQueriesSurviveNamedQueryCompilation(t *testing.T) {
	handler := &ingestionJobCaptureHandler{}
	repository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: handler}
	require.NoError(t, repository.UpdateInferenceIngestionJob(types.UpdateInferenceIngestionJob{ID: "job-1"}))
	compiled, _, err := sqlx.Named(handler.statement, handler.model)
	require.NoError(t, err)
	require.Contains(t, compiled, "COALESCE(CAST(? AS boolean), notifications_enabled)")
	require.NotContains(t, compiled, ":")

	runRepository := &InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	_, err = runRepository.SelectProcessingRunNotificationJobs(context.Background(), "tenant-a", "run-1")
	require.NoError(t, err)
	compiled, _, err = sqlx.Named(handler.statement, handler.model)
	require.NoError(t, err)
	require.NotContains(t, compiled, ":")

	insertRepository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: handler}
	require.NoError(t, insertRepository.InsertInferenceIngestionJob(types.CreateInferenceIngestionJob{ID: "job-1"}))
	compiled, _, err = sqlx.Named(handler.statement, handler.model)
	require.NoError(t, err)
	require.NotContains(t, compiled, ":")
}
