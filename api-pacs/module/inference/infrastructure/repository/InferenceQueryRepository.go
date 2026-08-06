package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceQueryRepository handles the inference query repository logic
type InferenceQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

// SelectInferenceModelByID get inference model by id
func (repository *InferenceQueryRepository) SelectInferenceModelByID(ctx context.Context, ID string) (entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	// get firestore inference model
	var inferenceModel entity.InferenceModel

	firestoreRes, err := firestoreClient.Collection(inferenceModel.GetModelName()).Doc(ID).Get(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&inferenceModel)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	return inferenceModel, nil
}

// SelectInferenceModelByContainerID get inference model by container
func (repository *InferenceQueryRepository) SelectInferenceModelByContainer(ctx context.Context, tenantID, containerID string) (entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	// get firestore inference model
	var inferenceModel entity.InferenceModel

	firestoreRes, err := firestoreClient.Collection(inferenceModel.GetModelName()).Where("tenant_id", "==", tenantID).Where("container_id", "==", containerID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&inferenceModel)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	return inferenceModel, nil
}

// SelectInferenceModels get inference models by tenant id
func (repository *InferenceQueryRepository) SelectInferenceModels(ctx context.Context, tenantID string) ([]entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore inference models
	var inferenceModel entity.InferenceModel

	query := firestoreClient.Collection(inferenceModel.GetModelName()).Where("tenant_id", "==", tenantID)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var inferenceModels []entity.InferenceModel

	for _, doc := range docs {
		var inferenceModel entity.InferenceModel
		if err := doc.DataTo(&inferenceModel); err != nil {
			log.Println(err)
			continue
		}

		inferenceModel.ID = doc.Ref.ID
		inferenceModels = append(inferenceModels, inferenceModel)
	}

	if len(inferenceModels) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return inferenceModels, nil
}

// SelectInferenceIngestionJobs get inference ingestion jobs
func (repository *InferenceQueryRepository) SelectInferenceIngestionJobs(tenantID *string) ([]entity.InferenceIngestionJob, error) {
	var job entity.InferenceIngestionJob
	var jobs []entity.InferenceIngestionJob

	stmt := fmt.Sprintf("SELECT * FROM %s", job.GetModelName())

	// if tenant id is set
	if tenantID != nil {
		stmt = fmt.Sprintf("%s WHERE tenant_id = :tenant_id", stmt)
	}

	stmt = fmt.Sprintf("%s ORDER BY updated_at DESC", stmt)

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, map[string]interface{}{
		"tenant_id": tenantID,
	}, &jobs)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(jobs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return jobs, nil
}

// SelectInferenceIngestionJobByID get inference ingestion job by ID
func (repository *InferenceQueryRepository) SelectInferenceIngestionJobByID(ID string) (entity.InferenceIngestionJob, error) {
	var job entity.InferenceIngestionJob

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE id = :id", job.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.QueryRow(stmt, map[string]interface{}{
		"id": ID,
	}, &job)
	if err != nil {
		log.Println(err)
		if err == sql.ErrNoRows {
			return entity.InferenceIngestionJob{}, errors.New(apiError.MissingRecord)
		}

		return entity.InferenceIngestionJob{}, errors.New(apiError.DatabaseError)
	}

	return job, nil
}

// SelectInferenceIngestionCandidateByID gets one ingestion candidate by ID
func (repository *InferenceQueryRepository) SelectInferenceIngestionCandidateByID(ID string) (entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE id = :id", candidate.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.QueryRow(stmt, map[string]interface{}{
		"id": ID,
	}, &candidate)
	if err != nil {
		log.Println(err)
		if err == sql.ErrNoRows {
			return entity.InferenceIngestionCandidate{}, errors.New(apiError.MissingRecord)
		}

		return entity.InferenceIngestionCandidate{}, errors.New(apiError.DatabaseError)
	}

	return candidate, nil
}

// SelectInferenceIngestionProcessingJobByCandidateModel gets the latest run-aware processing job by candidate/model.
func (repository *InferenceQueryRepository) SelectInferenceIngestionProcessingJobByCandidateModel(candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	var processingJob entity.InferenceIngestionProcessingJob

	stmt := fmt.Sprintf(`SELECT jobs.* FROM %s jobs
LEFT JOIN ingestion_processing_runs runs ON runs.id = jobs.processing_run_id
WHERE jobs.candidate_id = :candidate_id AND jobs.model_name = :model_name
ORDER BY runs.run_number DESC NULLS LAST, jobs.updated_at DESC, jobs.id DESC
LIMIT 1`, processingJob.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.QueryRow(stmt, map[string]interface{}{
		"candidate_id": candidateID,
		"model_name":   modelName,
	}, &processingJob)
	if err != nil {
		log.Println(err)
		if err == sql.ErrNoRows {
			return entity.InferenceIngestionProcessingJob{}, errors.New(apiError.MissingRecord)
		}

		return entity.InferenceIngestionProcessingJob{}, errors.New(apiError.DatabaseError)
	}

	return processingJob, nil
}

// ListInferenceIngestionCandidates lists ingestion candidates with operator debug filters
func (repository *InferenceQueryRepository) ListInferenceIngestionCandidates(data types.ListInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate
	var candidates []entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE tenant_id = :tenant_id", candidate.GetModelName())
	args := map[string]interface{}{
		"tenant_id": data.TenantID,
	}

	if data.IngestionJobID != nil {
		stmt = fmt.Sprintf("%s AND ingestion_job_id = :ingestion_job_id", stmt)
		args["ingestion_job_id"] = *data.IngestionJobID
	}

	if data.StudyInstanceUID != nil {
		stmt = fmt.Sprintf("%s AND study_instance_uid = :study_instance_uid", stmt)
		args["study_instance_uid"] = *data.StudyInstanceUID
	}

	if data.Status != nil {
		stmt = fmt.Sprintf("%s AND status = :status", stmt)
		args["status"] = *data.Status
	}

	if data.RetrievalFailures {
		stmt = fmt.Sprintf("%s AND (status = :failed_status OR last_retrieval_error IS NOT NULL)", stmt)
		args["failed_status"] = entity.InferenceIngestionCandidateStatusFailed
	}

	stmt = fmt.Sprintf("%s ORDER BY updated_at DESC", stmt)

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, args, &candidates)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(candidates) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return candidates, nil
}

// ListWorklistStudyStatuses returns one current status row per logical study.
// A non-terminal run is preferred; otherwise the latest terminal run is used.
func (repository *InferenceQueryRepository) ListWorklistStudyStatuses(data types.ListWorklistStudyStatuses) (types.WorklistStudyStatusPage, error) {
	statuses := make([]types.WorklistStudyStatus, 0)
	args := map[string]interface{}{
		"tenant_id": data.TenantID,
		"limit":     data.Limit + 1,
		"offset":    data.Offset,
	}

	studyFilter := ""
	if len(data.StudyInstanceUIDs) > 0 {
		studyFilter = "AND candidates.study_instance_uid = ANY(:study_instance_uids)"
		args["study_instance_uids"] = pq.Array(data.StudyInstanceUIDs)
	}

	stmt := fmt.Sprintf(`
WITH latest_candidates AS (
	SELECT DISTINCT ON (candidates.study_instance_uid)
		candidates.study_instance_uid,
		candidates.status AS ingestion_status,
		candidates.last_retrieval_state AS retrieval_state,
		candidates.last_retrieval_error AS retrieval_error,
		candidates.updated_at
	FROM ingestion_candidates candidates
	WHERE candidates.tenant_id = :tenant_id
		%s
	ORDER BY candidates.study_instance_uid, candidates.updated_at DESC, candidates.id DESC
)
SELECT
	candidates.study_instance_uid,
	candidates.ingestion_status,
	candidates.retrieval_state,
	candidates.retrieval_error,
	runs.id AS run_id,
	runs.run_number,
	runs.run_trigger,
	runs.phase,
	runs.outcome,
	COALESCE(runs.attention_required, FALSE) AS attention_required,
	COALESCE(runs.attention_reasons, '[]'::jsonb) AS attention_reasons,
	COALESCE(counts.expected_models, 0) AS expected_models,
	COALESCE(counts.pending_models, 0) AS pending_models,
	COALESCE(counts.queued_models, 0) AS queued_models,
	COALESCE(counts.running_models, 0) AS running_models,
	COALESCE(counts.completed_models, 0) AS completed_models,
	COALESCE(counts.failed_models, 0) AS failed_models,
	COALESCE(counts.skipped_models, 0) AS skipped_models,
	COALESCE(counts.cancelled_models, 0) AS cancelled_models,
	COALESCE(counts.active_models, 0) AS active_models,
	runs.version,
	runs.started_at,
	runs.completed_at,
	GREATEST(candidates.updated_at, COALESCE(runs.updated_at, candidates.updated_at)) AS updated_at
FROM latest_candidates candidates
LEFT JOIN LATERAL (
	SELECT selected_run.*
	FROM ingestion_processing_runs selected_run
	WHERE selected_run.tenant_id = :tenant_id
		AND selected_run.study_instance_uid = candidates.study_instance_uid
	ORDER BY (selected_run.phase <> 'TERMINAL') DESC,
		selected_run.run_number DESC,
		selected_run.created_at DESC,
		selected_run.id DESC
	LIMIT 1
) runs ON TRUE
LEFT JOIN LATERAL (
	SELECT
		COUNT(jobs.id)::int AS expected_models,
		COUNT(*) FILTER (WHERE jobs.status = 'pending')::int AS pending_models,
		COUNT(*) FILTER (WHERE jobs.status = 'queued')::int AS queued_models,
		COUNT(*) FILTER (WHERE jobs.status = 'running')::int AS running_models,
		COUNT(*) FILTER (WHERE jobs.status = 'completed')::int AS completed_models,
		COUNT(*) FILTER (WHERE jobs.status = 'failed')::int AS failed_models,
		COUNT(*) FILTER (WHERE jobs.status = 'skipped')::int AS skipped_models,
		COUNT(*) FILTER (WHERE jobs.status = 'cancelled')::int AS cancelled_models,
		COUNT(*) FILTER (WHERE jobs.status IN ('pending', 'queued', 'running'))::int AS active_models
	FROM ingestion_processing_jobs jobs
	WHERE jobs.processing_run_id = runs.id
) counts ON runs.id IS NOT NULL
ORDER BY updated_at DESC, candidates.study_instance_uid ASC
LIMIT :limit OFFSET :offset`, studyFilter)

	if err := repository.PostgresSQLDBHandlerInterface.Query(stmt, args, &statuses); err != nil {
		log.Println(err)
		return types.WorklistStudyStatusPage{}, errors.New(apiError.DatabaseError)
	}

	hasMore := len(statuses) > data.Limit
	if hasMore {
		statuses = statuses[:data.Limit]
	}

	return types.WorklistStudyStatusPage{Studies: statuses, HasMore: hasMore}, nil
}

// ListCandidatesByJob lists ingestion candidates by ingestion job ID
func (repository *InferenceQueryRepository) ListCandidatesByJob(ingestionJobID string) ([]entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate
	var candidates []entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE ingestion_job_id = :ingestion_job_id ORDER BY updated_at DESC", candidate.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, map[string]interface{}{
		"ingestion_job_id": ingestionJobID,
	}, &candidates)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(candidates) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return candidates, nil
}

// ListCandidatesReadyForRetrieval lists stable ingestion candidates ready for retrieval
func (repository *InferenceQueryRepository) ListCandidatesReadyForRetrieval(ingestionJobID string, stableBefore time.Time) ([]entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate
	var candidates []entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE ingestion_job_id = :ingestion_job_id AND status = :status AND last_changed_at <= :stable_before ORDER BY last_changed_at ASC", candidate.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, map[string]interface{}{
		"ingestion_job_id": ingestionJobID,
		"status":           entity.InferenceIngestionCandidateStatusStable,
		"stable_before":    stableBefore,
	}, &candidates)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(candidates) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return candidates, nil
}

// ListCandidatesQueuedForRetrieval lists ingestion candidates queued for retrieval
func (repository *InferenceQueryRepository) ListCandidatesQueuedForRetrieval() ([]entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate
	var candidates []entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("SELECT * FROM %s WHERE status = :status ORDER BY retrieval_queued_at ASC NULLS LAST, updated_at ASC", candidate.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, map[string]interface{}{
		"status": entity.InferenceIngestionCandidateStatusRetrievalQueued,
	}, &candidates)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(candidates) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return candidates, nil
}

// ListStaleProcessingCandidates lists candidates whose queued/running processing state has gone stale.
func (repository *InferenceQueryRepository) ListStaleProcessingCandidates(staleBefore time.Time) ([]entity.InferenceIngestionCandidate, error) {
	var candidate entity.InferenceIngestionCandidate
	var candidates []entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`SELECT * FROM %s
WHERE processing_status IN (:queued_status, :running_status)
	AND processing_status_at IS NOT NULL
	AND processing_status_at <= :stale_before
ORDER BY processing_status_at ASC, updated_at ASC`, candidate.GetModelName())

	err := repository.PostgresSQLDBHandlerInterface.Query(stmt, map[string]interface{}{
		"queued_status":  entity.InferenceIngestionCandidateProcessingStatusQueued,
		"running_status": entity.InferenceIngestionCandidateProcessingStatusRunning,
		"stale_before":   staleBefore,
	}, &candidates)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.DatabaseError)
	} else if len(candidates) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return candidates, nil
}

// SelectModelFeedbackByUserModelID get model feedback by model and user ID
func (repository *InferenceQueryRepository) SelectModelFeedbackByUserModelID(ctx context.Context, data types.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	// get firestore model feedback
	var modelFeedback entity.ModelFeedback

	firestoreRes, err := firestoreClient.Collection(modelFeedback.GetModelName()).Where("tenant_id", "==", data.TenantID).Where("user_id", "==", data.UserID).Where("model_id", "==", data.ModelID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.ModelFeedback{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&modelFeedback)
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	return modelFeedback, nil
}

// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
func (repository *InferenceQueryRepository) SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore model feedback answer
	var modelFeedbackAnswer entity.ModelFeedbackAnswer

	query := firestoreClient.Collection(modelFeedbackAnswer.GetModelName()).Where("model_feedback_id", "==", feedbackID)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var modelFeedbackAnswers []entity.ModelFeedbackAnswer

	for _, doc := range docs {
		var modelFeedbackAnswer entity.ModelFeedbackAnswer
		if err := doc.DataTo(&modelFeedbackAnswer); err != nil {
			log.Println(err)
			continue
		}

		modelFeedbackAnswer.ID = doc.Ref.ID
		modelFeedbackAnswers = append(modelFeedbackAnswers, modelFeedbackAnswer)
	}

	if len(modelFeedbackAnswers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return modelFeedbackAnswers, nil
}

// SelectOnboardingModelQuestionnaireAnswers selects onboarding model questionnaire answers
func (repository *InferenceQueryRepository) SelectOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore onboarding model questionnaire answers
	var onboardingModelQuestionnaireAnswer entity.OnboardingModelQuestionnaireAnswer

	query := firestoreClient.Collection(onboardingModelQuestionnaireAnswer.GetModelName()).Where("tenant_id", "==", data.TenantID).Where("user_id", "==", data.UserID)

	// if model id is set
	if data.ModelID != nil {
		query = query.Where("model_id", "==", data.ModelID)
	}

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var onboardingModelQuestionnaireAnswers []entity.OnboardingModelQuestionnaireAnswer

	for _, doc := range docs {
		var onboardingModelQuestionnaireAnswer entity.OnboardingModelQuestionnaireAnswer
		if err := doc.DataTo(&onboardingModelQuestionnaireAnswer); err != nil {
			log.Println(err)
			continue
		}

		onboardingModelQuestionnaireAnswer.ID = doc.Ref.ID
		onboardingModelQuestionnaireAnswers = append(onboardingModelQuestionnaireAnswers, onboardingModelQuestionnaireAnswer)
	}

	if len(onboardingModelQuestionnaireAnswers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return onboardingModelQuestionnaireAnswers, nil
}
