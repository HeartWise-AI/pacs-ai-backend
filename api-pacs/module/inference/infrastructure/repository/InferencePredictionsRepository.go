package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/lib/pq"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferencePredictionsRepository reads the AI Predictions page: exams with
// processing runs, the latest execution of each model, and earlier executions.
// Every statement is tenant-scoped and binds every value; only fixed SQL
// fragments are chosen in Go.
type InferencePredictionsRepository struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

// automaticRunTriggers are the triggers shown as "Automatic": runs started by
// ingestion, and the runs imported from before processing runs existed.
const automaticRunTriggers = `('AUTO', 'LEGACY_IMPORT')`

// predictionsScopeFragment restricts the runs aliased r to the exams of a scope.
func predictionsScopeFragment(scope types.PredictionsScope) (string, error) {
	const prefix = ` AND r.study_instance_uid IN (
			SELECT scope_runs.study_instance_uid FROM ingestion_processing_runs scope_runs
			WHERE scope_runs.tenant_id = :tenant_id AND `
	switch scope {
	case types.PredictionsScopeAll:
		return "", nil
	case types.PredictionsScopeRequestedBy:
		return prefix + `scope_runs.requested_by_user_id = :requested_by_user_id)`, nil
	case types.PredictionsScopeAutomatic:
		return prefix + `scope_runs.run_trigger IN ` + automaticRunTriggers + `)`, nil
	case types.PredictionsScopePeople:
		return prefix + `scope_runs.requested_by_user_id IS NOT NULL)`, nil
	default:
		return "", errors.New(apiError.InvalidPayload)
	}
}

// predictionsRunFilterFragment keeps the runs aliased r of a launcher scope (export lines).
func predictionsRunFilterFragment(scope types.PredictionsScope) (string, error) {
	switch scope {
	case "", types.PredictionsScopeAll:
		return "", nil
	case types.PredictionsScopeRequestedBy:
		return ` AND r.requested_by_user_id = :requested_by_user_id`, nil
	case types.PredictionsScopeAutomatic:
		return ` AND r.run_trigger IN ` + automaticRunTriggers, nil
	case types.PredictionsScopePeople:
		return ` AND r.requested_by_user_id IS NOT NULL`, nil
	default:
		return "", errors.New(apiError.InvalidPayload)
	}
}

func predictionsStatusFragment(status types.PredictionsExamStatus) (string, error) {
	switch status {
	case types.PredictionsExamStatusAny:
		return "", nil
	case types.PredictionsExamStatusActive:
		return ` AND exams.any_active`, nil
	case types.PredictionsExamStatusAttention:
		return ` AND exams.any_attention`, nil
	case types.PredictionsExamStatusCompleted:
		return ` AND exams.all_completed`, nil
	default:
		return "", errors.New(apiError.InvalidPayload)
	}
}

// escapeLikePattern makes user text match literally inside ILIKE ... ESCAPE '\'.
func escapeLikePattern(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// buildListPredictionExamsQuery returns the exam page statement and its arguments.
//
// exam_keys narrows the tenant's exams by scope (and by recent activity when a
// start date is given, which the index on (tenant_id, updated_at) serves);
// latest keeps the newest execution of each model of those exams and marks
// the stalled ones exactly like predictionExecutionState does; exams
// aggregates the latest states (a stalled model needs attention and is not
// active); meta picks the stored patient ID, accession,
// modalities and study date from the candidates of those executions.
func buildListPredictionExamsQuery(data types.ListPredictionExams) (string, map[string]interface{}, error) {
	scope, err := predictionsScopeFragment(data.Scope)
	if err != nil {
		return "", nil, err
	}
	status, err := predictionsStatusFragment(data.Status)
	if err != nil {
		return "", nil, err
	}
	args := map[string]interface{}{
		"tenant_id":            data.TenantID,
		"requested_by_user_id": data.RequestedByUserID,
		"model_name":           data.ModelName,
		"stalled_before":       data.StalledBefore,
		"limit":                data.Limit,
		"offset":               data.Offset,
	}

	keyFilters := scope
	if data.From != nil {
		keyFilters += ` AND r.updated_at >= :from`
		args["from"] = *data.From
	}
	latestFilters := ""
	if data.ModelName != "" {
		latestFilters = ` AND j.model_name = :model_name`
	}

	pageFilters := status
	if data.From != nil {
		pageFilters += ` AND exams.last_activity_at >= :from`
	}
	if data.To != nil {
		pageFilters += ` AND exams.last_activity_at < :to`
		args["to"] = *data.To
	}
	if modality := strings.ToUpper(strings.TrimSpace(data.Modality)); modality != "" {
		pageFilters += ` AND (exams.dispatched_with_modality OR meta.modalities_in_study ILIKE :modality_pattern ESCAPE '\')`
		args["modality"] = modality
		args["modality_pattern"] = "%" + escapeLikePattern(modality) + "%"
	} else {
		args["modality"] = ""
	}
	if search := strings.TrimSpace(data.Search); search != "" {
		pageFilters += ` AND (meta.patient_id ILIKE :search_pattern ESCAPE '\'
			OR meta.accession_number ILIKE :search_pattern ESCAPE '\'
			OR exams.study_instance_uid = :search)`
		args["search"] = search
		args["search_pattern"] = "%" + escapeLikePattern(search) + "%"
	}

	query := `
		WITH exam_keys AS (
			SELECT DISTINCT r.study_instance_uid
			FROM ingestion_processing_runs r
			WHERE r.tenant_id = :tenant_id` + keyFilters + `
		),
		latest AS (
			SELECT DISTINCT ON (r.study_instance_uid, j.model_name)
				r.study_instance_uid, j.model_name, CAST(j.status AS text) AS status, j.modality, j.candidate_id,
				GREATEST(r.updated_at, j.updated_at) AS activity_at,
				(CAST(j.status AS text) IN ('pending', 'queued')
					AND NULLIF(BTRIM(j.study_service_job_id), '') IS NULL
					AND r.created_at < :stalled_before) AS stalled
			FROM exam_keys
			JOIN ingestion_processing_runs r
				ON r.tenant_id = :tenant_id AND r.study_instance_uid = exam_keys.study_instance_uid
			JOIN ingestion_processing_jobs j
				ON j.processing_run_id = r.id AND j.tenant_id = :tenant_id
			WHERE TRUE` + latestFilters + `
			ORDER BY r.study_instance_uid, j.model_name, r.run_number DESC, j.created_at DESC, j.id DESC
		),
		exams AS (
			SELECT latest.study_instance_uid,
				MAX(latest.activity_at) AS last_activity_at,
				BOOL_OR(latest.status IN ('pending', 'queued', 'running') AND NOT latest.stalled) AS any_active,
				BOOL_OR(latest.status IN ('failed', 'skipped') OR latest.stalled) AS any_attention,
				BOOL_AND(latest.status = 'completed') AS all_completed,
				BOOL_OR(UPPER(COALESCE(latest.modality, '')) = :modality) AS dispatched_with_modality
			FROM latest
			GROUP BY latest.study_instance_uid
		),
		meta AS (
			SELECT DISTINCT ON (latest.study_instance_uid)
				latest.study_instance_uid, c.patient_id, c.accession_number, c.modalities_in_study, c.study_date
			FROM latest
			JOIN ingestion_candidates c ON c.id = latest.candidate_id AND c.tenant_id = :tenant_id
			ORDER BY latest.study_instance_uid,
				(c.patient_id IS NULL AND c.accession_number IS NULL), c.updated_at DESC, c.id
		)
		SELECT exams.study_instance_uid, exams.last_activity_at,
			meta.patient_id, meta.accession_number, meta.modalities_in_study, meta.study_date,
			COUNT(*) OVER () AS total
		FROM exams
		LEFT JOIN meta ON meta.study_instance_uid = exams.study_instance_uid
		WHERE TRUE` + pageFilters + `
		ORDER BY exams.last_activity_at DESC, exams.study_instance_uid DESC
		LIMIT :limit OFFSET :offset`
	return query, args, nil
}

// ListPredictionExams returns one page of exams, most recent activity first,
// and the number of exams matching the filters.
func (repository *InferencePredictionsRepository) ListPredictionExams(_ context.Context, data types.ListPredictionExams) (types.PredictionExamPage, error) {
	if strings.TrimSpace(data.TenantID) == "" || data.Limit <= 0 || data.Offset < 0 ||
		(data.Scope == types.PredictionsScopeRequestedBy && strings.TrimSpace(data.RequestedByUserID) == "") {
		return types.PredictionExamPage{}, errors.New(apiError.InvalidPayload)
	}
	query, args, err := buildListPredictionExamsQuery(data)
	if err != nil {
		return types.PredictionExamPage{}, err
	}
	rows := make([]types.PredictionExamRow, 0)
	if err := repository.PostgresSQLDBHandlerInterface.Query(query, args, &rows); err != nil {
		return types.PredictionExamPage{}, processingRunError(err)
	}
	page := types.PredictionExamPage{Exams: rows}
	if len(rows) > 0 {
		page.Total = rows[0].Total
	}
	return page, nil
}

const predictionExecutionColumns = `
	r.study_instance_uid, r.id AS run_id, r.run_number, r.run_trigger, r.phase AS run_phase,
	r.attention_reasons AS run_attention_reasons, r.requested_by_user_id,
	r.created_at AS run_created_at, r.cancelled_at,
	j.id AS execution_id, j.model_name, j.model_version, j.modality, j.status,
	j.study_service_job_id, j.error_message, j.skip_reason_code, j.skip_reason_message,
	j.started_at, j.completed_at, j.created_at, j.updated_at,
	c.status AS candidate_status`

const predictionExecutionJoins = `
	FROM ingestion_processing_runs r
	JOIN ingestion_processing_jobs j ON j.processing_run_id = r.id AND j.tenant_id = :tenant_id
	LEFT JOIN ingestion_candidates c ON c.id = j.candidate_id AND c.tenant_id = :tenant_id`

// ListPredictionExecutions returns the executions of a bounded set of exams:
// the newest of each model (with the number of earlier ones) or all of them,
// grouped by exam, newest run first.
func (repository *InferencePredictionsRepository) ListPredictionExecutions(_ context.Context, data types.ListPredictionExecutions) ([]types.PredictionExecutionRow, error) {
	rows := make([]types.PredictionExecutionRow, 0)
	if len(data.StudyInstanceUIDs) == 0 {
		return rows, nil
	}
	if strings.TrimSpace(data.TenantID) == "" ||
		(data.LaunchedBy == types.PredictionsScopeRequestedBy && strings.TrimSpace(data.RequestedByUserID) == "") {
		return nil, errors.New(apiError.InvalidPayload)
	}
	runFilter, err := predictionsRunFilterFragment(data.LaunchedBy)
	if err != nil {
		return nil, err
	}
	filters := runFilter
	if data.ModelName != "" {
		filters += ` AND j.model_name = :model_name`
	}
	args := map[string]interface{}{
		"tenant_id":            data.TenantID,
		"study_instance_uids":  pq.Array(data.StudyInstanceUIDs),
		"model_name":           data.ModelName,
		"requested_by_user_id": data.RequestedByUserID,
	}
	where := `
	WHERE r.tenant_id = :tenant_id AND r.study_instance_uid = ANY(:study_instance_uids)` + filters

	var query string
	if data.LatestOnly {
		query = `SELECT * FROM (
			SELECT DISTINCT ON (r.study_instance_uid, j.model_name)` + predictionExecutionColumns + `,
				(COUNT(*) OVER (PARTITION BY r.study_instance_uid, j.model_name) - 1) AS earlier_runs` +
			predictionExecutionJoins + where + `
			ORDER BY r.study_instance_uid, j.model_name, r.run_number DESC, j.created_at DESC, j.id DESC
		) latest
		ORDER BY latest.study_instance_uid, latest.model_name`
	} else {
		query = `SELECT` + predictionExecutionColumns + `, 0 AS earlier_runs` + predictionExecutionJoins + where + `
		ORDER BY r.study_instance_uid, r.run_number DESC, j.created_at ASC, j.id ASC`
	}
	if err := repository.PostgresSQLDBHandlerInterface.Query(query, args, &rows); err != nil {
		return nil, processingRunError(err)
	}
	return rows, nil
}

// ListPredictionModelHistory returns the executions of one model on one exam,
// newest first, with one extra row to tell whether more exist.
func (repository *InferencePredictionsRepository) ListPredictionModelHistory(_ context.Context, data types.ListPredictionModelHistory) ([]types.PredictionExecutionRow, error) {
	if strings.TrimSpace(data.TenantID) == "" || strings.TrimSpace(data.StudyInstanceUID) == "" ||
		strings.TrimSpace(data.ModelName) == "" || data.Limit <= 0 || data.Offset < 0 {
		return nil, errors.New(apiError.InvalidPayload)
	}
	rows := make([]types.PredictionExecutionRow, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`SELECT`+predictionExecutionColumns+`, 0 AS earlier_runs`+predictionExecutionJoins+`
		WHERE r.tenant_id = :tenant_id AND r.study_instance_uid = :study_instance_uid AND j.model_name = :model_name
		ORDER BY r.run_number DESC, j.created_at DESC, j.id DESC
		LIMIT :limit OFFSET :offset`, map[string]interface{}{
		"tenant_id": data.TenantID, "study_instance_uid": data.StudyInstanceUID,
		"model_name": data.ModelName, "limit": data.Limit + 1, "offset": data.Offset,
	}, &rows)
	if err != nil {
		return nil, processingRunError(err)
	}
	return rows, nil
}

// ListPredictionLaunchers counts the runs of each exam by trigger and requester.
func (repository *InferencePredictionsRepository) ListPredictionLaunchers(_ context.Context, tenantID string, studyInstanceUIDs []string) ([]types.PredictionLauncherRow, error) {
	rows := make([]types.PredictionLauncherRow, 0)
	if len(studyInstanceUIDs) == 0 {
		return rows, nil
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New(apiError.InvalidPayload)
	}
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT r.study_instance_uid, r.run_trigger, r.requested_by_user_id,
			COUNT(*) AS runs, MIN(r.created_at) AS first_at
		FROM ingestion_processing_runs r
		WHERE r.tenant_id = :tenant_id AND r.study_instance_uid = ANY(:study_instance_uids)
		GROUP BY r.study_instance_uid, r.run_trigger, r.requested_by_user_id
		ORDER BY r.study_instance_uid, first_at ASC`, map[string]interface{}{
		"tenant_id": tenantID, "study_instance_uids": pq.Array(studyInstanceUIDs),
	}, &rows)
	if err != nil {
		return nil, processingRunError(err)
	}
	return rows, nil
}
