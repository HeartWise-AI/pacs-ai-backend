package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceCommandRepository handles the inference command repository logic
type InferenceCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

// DeleteInferenceModel deletes an inference model
func (repository *InferenceCommandRepository) DeleteInferenceModel(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete inference model
	var model entity.InferenceModel

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// DeleteInferenceIngestionJob deletes an inference ingestion job
func (repository *InferenceCommandRepository) DeleteInferenceIngestionJob(ID string) error {
	job := &entity.InferenceIngestionJob{
		ID: ID,
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE id = :id", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// DeleteInferenceIngestionJobByContainerID deletes an inference ingestion job by container ID
func (repository *InferenceCommandRepository) DeleteInferenceIngestionJobByContainerID(tenantID, containerID string) error {
	job := &entity.InferenceIngestionJob{
		TenantID:    tenantID,
		ContainerID: containerID,
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE tenant_id = :tenant_id AND container_id = :container_id", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// DeleteModelFeedback deletes model feedback
func (repository *InferenceCommandRepository) DeleteModelFeedback(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete model feedback
	var model entity.ModelFeedback

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// DeleteModelFeedbackAnswer deletes model feedback answer
func (repository *InferenceCommandRepository) DeleteModelFeedbackAnswer(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete model feedback answer
	var model entity.ModelFeedbackAnswer

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// DeleteOnboardingModelQuestionnaireAnswer deletes an onboarding model questionnaire answer
func (repository *InferenceCommandRepository) DeleteOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete onboarding model questionnaire answer
	var answer entity.OnboardingModelQuestionnaireAnswer

	collectionPath := fmt.Sprintf("%s/%s", answer.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertModelFeedbackAnswer inserts a new  model feedback answer
func (repository *InferenceCommandRepository) InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add model feedback answer
	model := entity.ModelFeedbackAnswer{
		ID:                     data.ID,
		ModelFeedbackID:        data.ModelFeedbackID,
		QuestionnaireID:        data.QuestionnaireID,
		QuestionnaireQuestion:  data.QuestionnaireQuestion,
		QuestionnaireAnswerIDs: data.QuestionnaireAnswerIDs,
		QuestionnaireAnswers:   data.QuestionnaireAnswers,
		CreatedAt:              int(time.Now().Unix()),
		UpdatedAt:              int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, model)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertInferenceModel inserts a new inference model
func (repository *InferenceCommandRepository) InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add inference model
	model := entity.InferenceModel{
		ID:                  data.ID,
		TenantID:            data.TenantID,
		ContainerID:         data.ContainerID,
		Name:                data.Name,
		DockerImage:         data.DockerImage,
		Envs:                data.Envs,
		DisallowedDICOMTags: data.DisallowedDICOMTags,
		OutputMode:          data.OutputMode,
		CreatedAt:           int(time.Now().Unix()),
		UpdatedAt:           int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, model)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertInferenceIngestionJob inserts a new inference ingestion job
func (repository *InferenceCommandRepository) InsertInferenceIngestionJob(data types.CreateInferenceIngestionJob) error {
	job := entity.InferenceIngestionJob{
		ID:                     data.ID,
		TenantID:               data.TenantID,
		DICOMModality:          data.DICOMModality,
		ContainerID:            data.ContainerID,
		ModelID:                data.ModelID,
		ModelName:              data.ModelName,
		ModelVersion:           data.ModelVersion,
		Modalities:             data.Modalities,
		StabilityMinutes:       data.StabilityMinutes,
		RecentWindowMinutes:    data.RecentWindowMinutes,
		MissingPollsThreshold:  data.MissingPollsThreshold,
		StudyTimeStart:         data.StudyTimeStart,
		StudyTimeEnd:           data.StudyTimeEnd,
		ScheduleStartTimestamp: data.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   data.ScheduleEndTimestamp,
		Status:                 data.Status,
	}

	stmt := fmt.Sprintf("INSERT INTO %s (id, tenant_id, dicom_modality, container_id, model_id, model_name, model_version, modalities, stability_minutes, recent_window_minutes, missing_polls_threshold, study_time_start, study_time_end, schedule_start_timestamp, schedule_end_timestamp, status) "+
		"VALUES (:id, :tenant_id, :dicom_modality, :container_id, :model_id, :model_name, :model_version, :modalities, :stability_minutes, :recent_window_minutes, :missing_polls_threshold, :study_time_start, :study_time_end, :schedule_start_timestamp, :schedule_end_timestamp, :status)", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return errors.New(apiError.DuplicateRecord)
			}
		}

		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// InsertInferenceIngestionProcessingJob inserts a new inference ingestion processing job
func (repository *InferenceCommandRepository) InsertInferenceIngestionProcessingJob(data types.AddInferenceIngestionProcessingJob) error {
	processingJob := entity.InferenceIngestionProcessingJob{
		ID:                data.ID,
		CandidateID:       data.CandidateID,
		TenantID:          data.TenantID,
		ModelName:         data.ModelName,
		ModelVersion:      data.ModelVersion,
		Modality:          data.Modality,
		Status:            data.Status,
		StudyServiceJobID: data.StudyServiceJobID,
		ErrorMessage:      data.ErrorMessage,
		LastEventID:       data.LastEventID,
		LastEventSequence: data.LastEventSequence,
		StartedAt:         data.StartedAt,
		CompletedAt:       data.CompletedAt,
	}

	stmt := fmt.Sprintf("INSERT INTO %s (id, candidate_id, tenant_id, model_name, model_version, modality, status, study_service_job_id, error_message, last_event_id, last_event_sequence, started_at, completed_at) "+
		"VALUES (:id, :candidate_id, :tenant_id, :model_name, :model_version, :modality, :status, :study_service_job_id, :error_message, :last_event_id, :last_event_sequence, :started_at, :completed_at)", processingJob.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, processingJob)
	if err != nil {
		log.Println(err)

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return errors.New(apiError.DuplicateRecord)
			}
		}

		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateInferenceIngestionProcessingJob updates an inference ingestion processing job
func (repository *InferenceCommandRepository) UpdateInferenceIngestionProcessingJob(data types.UpdateInferenceIngestionProcessingJob) error {
	var processingJob entity.InferenceIngestionProcessingJob

	stmt := fmt.Sprintf(`UPDATE %s
SET status = :status,
	model_version = COALESCE(:model_version, model_version),
	modality = COALESCE(:modality, modality),
	study_service_job_id = COALESCE(:study_service_job_id, study_service_job_id),
	error_message = :error_message,
	last_event_id = COALESCE(:last_event_id, last_event_id),
	last_event_sequence = COALESCE(:last_event_sequence, last_event_sequence),
	started_at = COALESCE(:started_at, started_at),
	completed_at = COALESCE(:completed_at, completed_at)
WHERE id = :id`, processingJob.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                   data.ID,
		"status":               data.Status,
		"model_version":        nullableStringValue(data.ModelVersion),
		"modality":             nullableStringValue(data.Modality),
		"study_service_job_id": nullableStringValue(data.StudyServiceJobID),
		"error_message":        nullableStringValue(data.ErrorMessage),
		"last_event_id":        nullableStringValue(data.LastEventID),
		"last_event_sequence":  nullableInt64Value(data.LastEventSequence),
		"started_at":           nullableTimeValue(data.StartedAt),
		"completed_at":         nullableTimeValue(data.CompletedAt),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpsertIngestionCandidate upserts an ingestion candidate
func (repository *InferenceCommandRepository) UpsertIngestionCandidate(data types.UpsertIngestionCandidate) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`INSERT INTO %s (
		id,
		tenant_id,
		ingestion_job_id,
		study_instance_uid,
		study_date,
		study_time,
		modalities_in_study,
		patient_id,
		accession_number,
		series_count,
		instance_count,
		first_seen_at,
		last_seen_at,
		last_changed_at,
		missing_polls,
		status
	) VALUES (
		:id,
		:tenant_id,
		:ingestion_job_id,
		:study_instance_uid,
		:study_date,
		:study_time,
		:modalities_in_study,
		:patient_id,
		:accession_number,
		:series_count,
		:instance_count,
		CURRENT_TIMESTAMP,
		CURRENT_TIMESTAMP,
		CURRENT_TIMESTAMP,
		0,
		'DISCOVERED'
	)
	ON CONFLICT (ingestion_job_id, study_instance_uid)
	DO UPDATE SET
		study_date = EXCLUDED.study_date,
		study_time = EXCLUDED.study_time,
		modalities_in_study = EXCLUDED.modalities_in_study,
		patient_id = EXCLUDED.patient_id,
		accession_number = EXCLUDED.accession_number,
		series_count = EXCLUDED.series_count,
		instance_count = EXCLUDED.instance_count,
		last_seen_at = CURRENT_TIMESTAMP,
		last_changed_at = CASE
			WHEN %s.series_count IS DISTINCT FROM EXCLUDED.series_count
				OR %s.instance_count IS DISTINCT FROM EXCLUDED.instance_count
			THEN CURRENT_TIMESTAMP
			ELSE %s.last_changed_at
		END,
		missing_polls = 0,
		status = CASE
			WHEN %s.status = 'DISAPPEARED' THEN 'DISCOVERED'
			ELSE %s.status
		END`, candidate.GetModelName(), candidate.GetModelName(), candidate.GetModelName(), candidate.GetModelName(), candidate.GetModelName(), candidate.GetModelName())

	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                  data.ID,
		"tenant_id":           data.TenantID,
		"ingestion_job_id":    data.IngestionJobID,
		"study_instance_uid":  data.StudyInstanceUID,
		"study_date":          nullableStringValue(data.StudyDate),
		"study_time":          nullableStringValue(data.StudyTime),
		"modalities_in_study": nullableStringValue(data.ModalitiesInStudy),
		"patient_id":          nullableStringValue(data.PatientID),
		"accession_number":    nullableStringValue(data.AccessionNumber),
		"series_count":        nullableIntValue(data.SeriesCount),
		"instance_count":      nullableIntValue(data.InstanceCount),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// InsertOnboardingModelQuestionnaireAnswer inserts a onboarding model questionnaire answer
func (repository *InferenceCommandRepository) InsertOnboardingModelQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add onboarding model questionnaire answer
	answer := entity.OnboardingModelQuestionnaireAnswer{
		ID:                     data.ID,
		TenantID:               data.TenantID,
		UserID:                 data.UserID,
		ModelID:                data.ModelID,
		QuestionnaireID:        data.QuestionnaireID,
		QuestionnaireQuestion:  data.QuestionnaireQuestion,
		QuestionnaireAnswerIDs: data.QuestionnaireAnswerIDs,
		QuestionnaireAnswers:   data.QuestionnaireAnswers,
		CreatedAt:              int(time.Now().Unix()),
		UpdatedAt:              int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", answer.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, answer)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateInferenceModel updates an inference model
func (repository *InferenceCommandRepository) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update inference model
	var model entity.InferenceModel

	updateInferenceModel := []firestore.Update{
		{
			Path:  "disallowed_dicom_tags",
			Value: data.DisallowedDICOMTags,
		},
		{
			Path:  "output_mode",
			Value: data.OutputMode,
		},
		{
			Path:  "updated_at",
			Value: int(time.Now().Unix()),
		},
	}

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateInferenceModel)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// ReplaceInferenceModelRegistration atomically compares and replaces the live
// registration. The returned deployment is the rollback baseline observed by
// the same transaction that commits the candidate.
func (repository *InferenceCommandRepository) ReplaceInferenceModelRegistration(
	ctx context.Context,
	data types.ReplaceInferenceModelRegistration,
) (entity.InferenceModelDeployment, error) {
	normalizeDeployment(&data.Deployment)
	data.ID, data.TenantID = strings.TrimSpace(data.ID), strings.TrimSpace(data.TenantID)
	data.ExpectedContainerID = strings.TrimSpace(data.ExpectedContainerID)
	data.ExpectedImageReference = strings.TrimSpace(data.ExpectedImageReference)
	if err := validateRegistrationReplacement(data); err != nil {
		return entity.InferenceModelDeployment{}, err
	}
	return repository.swapInferenceModelRegistration(
		ctx, data.ID, data.TenantID, data.ExpectedContainerID, data.ExpectedImageReference, data.Deployment, true,
	)
}

// RestoreInferenceModelRegistration is the rollback compare-and-swap. Legacy
// baselines deliberately delete the immutable deployment snapshot because no
// trustworthy digest or provenance existed before the upgrade.
func (repository *InferenceCommandRepository) RestoreInferenceModelRegistration(
	ctx context.Context,
	data types.RestoreInferenceModelRegistration,
) error {
	normalizeDeployment(&data.Previous)
	data.ID, data.TenantID = strings.TrimSpace(data.ID), strings.TrimSpace(data.TenantID)
	data.ExpectedContainerID = strings.TrimSpace(data.ExpectedContainerID)
	data.ExpectedImageReference = strings.TrimSpace(data.ExpectedImageReference)
	persistDeployment, err := validateRegistrationRestore(data)
	if err != nil {
		return err
	}
	_, err = repository.swapInferenceModelRegistration(
		ctx, data.ID, data.TenantID, data.ExpectedContainerID, data.ExpectedImageReference, data.Previous, persistDeployment,
	)
	return err
}

func (repository *InferenceCommandRepository) swapInferenceModelRegistration(
	ctx context.Context,
	modelID, tenantID, expectedContainerID, expectedImageReference string,
	replacement entity.InferenceModelDeployment,
	persistDeployment bool,
) (entity.InferenceModelDeployment, error) {

	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModelDeployment{}, errors.New(apiError.FirestoreError)
	}

	var model entity.InferenceModel
	modelRef := firestoreClient.Collection(model.GetModelName()).Doc(modelID)
	var previous entity.InferenceModelDeployment
	err = firestoreClient.RunTransaction(ctx, func(ctx context.Context, transaction *firestore.Transaction) error {
		snapshot, getErr := transaction.Get(modelRef)
		if getErr != nil {
			if status.Code(getErr) == codes.NotFound {
				return errors.New(apiError.MissingRecord)
			}
			return getErr
		}
		if decodeErr := snapshot.DataTo(&model); decodeErr != nil {
			return decodeErr
		}
		model.ID = snapshot.Ref.ID
		if validationErr := validateCurrentRegistration(
			model, tenantID, expectedContainerID, expectedImageReference,
		); validationErr != nil {
			return validationErr
		}

		for _, containerID := range []string{expectedContainerID, replacement.ContainerID} {
			if exclusivityErr := ensureExclusiveContainerRegistration(
				transaction, firestoreClient, model.GetModelName(), modelRef.ID, containerID,
			); exclusivityErr != nil {
				return exclusivityErr
			}
		}

		previous = registeredDeployment(model)
		deploymentValue := interface{}(firestore.Delete)
		if persistDeployment {
			deploymentValue = replacement
		}
		return transaction.Update(modelRef, []firestore.Update{
			{Path: "container_id", Value: replacement.ContainerID},
			{Path: "docker_image", Value: replacement.ImageReference},
			{Path: "deployment", Value: deploymentValue},
			{Path: "updated_at", Value: int(time.Now().Unix())},
		})
	})
	if err != nil {
		switch err.Error() {
		case apiError.MissingRecord, apiError.InferenceUpgradeConflict, apiError.InvalidPayload:
			return entity.InferenceModelDeployment{}, err
		default:
			log.Println(err)
			return entity.InferenceModelDeployment{}, fmt.Errorf("%s: %w", apiError.FirestoreError, err)
		}
	}
	return previous, nil
}

func normalizeDeployment(deployment *entity.InferenceModelDeployment) {
	deployment.ContainerID = strings.TrimSpace(deployment.ContainerID)
	deployment.ImageReference = strings.TrimSpace(deployment.ImageReference)
	deployment.ImageDigest = strings.TrimSpace(deployment.ImageDigest)
	deployment.LocalImageID = strings.TrimSpace(deployment.LocalImageID)
	deployment.ModelVersion = strings.TrimSpace(deployment.ModelVersion)
}

func ensureExclusiveContainerRegistration(
	transaction *firestore.Transaction,
	client *firestore.Client,
	collectionName, modelID, containerID string,
) error {
	registrations, err := transaction.Documents(
		client.Collection(collectionName).Where("container_id", "==", containerID),
	).GetAll()
	if err != nil {
		return err
	}
	for _, registration := range registrations {
		if registration.Ref.ID != modelID {
			return errors.New(apiError.InferenceUpgradeConflict)
		}
	}
	return nil
}

func validateRegistrationReplacement(data types.ReplaceInferenceModelRegistration) error {
	deployment := data.Deployment
	if data.ID == "" || data.TenantID == "" || data.ExpectedContainerID == "" || data.ExpectedImageReference == "" ||
		deployment.ContainerID == "" || deployment.ImageReference == "" || deployment.ImageDigest == "" ||
		deployment.LocalImageID == "" || deployment.ModelVersion == "" || deployment.DeployedAt <= 0 ||
		deployment.Provenance == nil {
		return errors.New(apiError.InvalidPayload)
	}
	if deployment.ContainerID == data.ExpectedContainerID {
		return errors.New(apiError.InvalidPayload)
	}
	provenance := deployment.Provenance
	if strings.TrimSpace(provenance.SourceRepository) == "" || strings.TrimSpace(provenance.ModelRepository) == "" ||
		strings.TrimSpace(provenance.ModelRevision) == "" || strings.TrimSpace(provenance.WeightsPath) == "" ||
		strings.TrimSpace(provenance.WeightsSHA256) == "" || provenance.SourceRevision == nil ||
		strings.TrimSpace(*provenance.SourceRevision) == "" {
		return errors.New(apiError.InvalidPayload)
	}
	return nil
}

func validateRegistrationRestore(data types.RestoreInferenceModelRegistration) (bool, error) {
	previous := data.Previous
	if data.ID == "" || data.TenantID == "" || data.ExpectedContainerID == "" || data.ExpectedImageReference == "" ||
		previous.ContainerID == "" || previous.ImageReference == "" || previous.ContainerID == data.ExpectedContainerID {
		return false, errors.New(apiError.InvalidPayload)
	}
	complete := deploymentSnapshotComplete(previous)
	legacy := previous.ImageDigest == "" && previous.LocalImageID == "" && previous.ModelVersion == "" &&
		previous.Provenance == nil && previous.DeployedAt == 0
	if !complete && !legacy {
		return false, errors.New(apiError.InvalidPayload)
	}
	return complete, nil
}

func deploymentSnapshotComplete(deployment entity.InferenceModelDeployment) bool {
	if deployment.ImageDigest == "" || deployment.LocalImageID == "" || deployment.ModelVersion == "" ||
		deployment.Provenance == nil || deployment.DeployedAt <= 0 {
		return false
	}
	provenance := deployment.Provenance
	return strings.TrimSpace(provenance.SourceRepository) != "" &&
		provenance.SourceRevision != nil && strings.TrimSpace(*provenance.SourceRevision) != "" &&
		strings.TrimSpace(provenance.ModelRepository) != "" && strings.TrimSpace(provenance.ModelRevision) != "" &&
		strings.TrimSpace(provenance.WeightsPath) != "" && strings.TrimSpace(provenance.WeightsSHA256) != ""
}

func validateCurrentRegistration(
	model entity.InferenceModel,
	tenantID, expectedContainerID, expectedImageReference string,
) error {
	if model.TenantID != tenantID {
		return errors.New(apiError.MissingRecord)
	}
	if model.ContainerID != expectedContainerID || model.DockerImage != expectedImageReference {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	return nil
}

func registeredDeployment(model entity.InferenceModel) entity.InferenceModelDeployment {
	if model.Deployment != nil &&
		model.Deployment.ContainerID == model.ContainerID &&
		model.Deployment.ImageReference == model.DockerImage {
		return *model.Deployment
	}
	return entity.InferenceModelDeployment{
		ContainerID:    model.ContainerID,
		ImageReference: model.DockerImage,
	}
}

// UpdateInferenceIngestionJob updates an inference ingestion job
func (repository *InferenceCommandRepository) UpdateInferenceIngestionJob(data types.UpdateInferenceIngestionJob) error {
	job := &entity.InferenceIngestionJob{
		ID:                     data.ID,
		Modalities:             data.Modalities,
		StabilityMinutes:       data.StabilityMinutes,
		RecentWindowMinutes:    data.RecentWindowMinutes,
		MissingPollsThreshold:  data.MissingPollsThreshold,
		StudyTimeStart:         data.StudyTimeStart,
		StudyTimeEnd:           data.StudyTimeEnd,
		ScheduleStartTimestamp: data.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   data.ScheduleEndTimestamp,
	}
	stmt := fmt.Sprintf("UPDATE %s SET modalities = :modalities, stability_minutes = :stability_minutes, "+
		"recent_window_minutes = :recent_window_minutes, "+
		"missing_polls_threshold = :missing_polls_threshold, study_time_start = :study_time_start, study_time_end = :study_time_end, "+
		"schedule_start_timestamp = :schedule_start_timestamp, schedule_end_timestamp = :schedule_end_timestamp WHERE id = :id", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateInferenceIngestionJobStatus updates the status of an inference ingestion job
func (repository *InferenceCommandRepository) UpdateInferenceIngestionJobStatus(ID string, status entity.InferenceIngestionJobStatus) error {
	job := &entity.InferenceIngestionJob{
		ID:     ID,
		Status: status,
	}

	stmt := fmt.Sprintf("UPDATE %s SET status = :status WHERE id = :id", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateInferenceIngestionJobLastExecutedAt updates last executed at of infererence ingestion job
func (repository *InferenceCommandRepository) UpdateInferenceIngestionJobLastExecutedAt(ID string) error {
	now := time.Now()

	job := &entity.InferenceIngestionJob{
		ID:             ID,
		LastExecutedAt: &now,
	}

	stmt := fmt.Sprintf("UPDATE %s SET last_executed_at = :last_executed_at WHERE id = :id", job.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, job)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateCandidateStatus updates the status of an ingestion candidate
func (repository *InferenceCommandRepository) UpdateCandidateStatus(ID string, status entity.InferenceIngestionCandidateStatus) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET status = :status WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":     ID,
		"status": status,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// SaveCandidateOrthancJobIDs stores Orthanc job IDs for an ingestion candidate
func (repository *InferenceCommandRepository) SaveCandidateOrthancJobIDs(ID string, orthancJobIDs []string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET orthanc_job_ids = :orthanc_job_ids, last_retrieval_checked_at = CURRENT_TIMESTAMP WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":              ID,
		"orthanc_job_ids": nullableStringArrayValue(orthancJobIDs),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateCandidateRetrievalState stores retrieval state and error details for an ingestion candidate
func (repository *InferenceCommandRepository) UpdateCandidateRetrievalState(data types.UpdateCandidateRetrievalState) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`UPDATE %s
SET orthanc_job_ids = COALESCE(:orthanc_job_ids, orthanc_job_ids),
	last_retrieval_state = COALESCE(:last_retrieval_state, last_retrieval_state),
	last_retrieval_error = COALESCE(:last_retrieval_error, last_retrieval_error),
	last_retrieval_error_details = COALESCE(:last_retrieval_error_details, last_retrieval_error_details),
	last_retrieval_checked_at = CURRENT_TIMESTAMP
WHERE id = :id`, candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                           data.ID,
		"orthanc_job_ids":              nullableStringArrayValue(data.OrthancJobIDs),
		"last_retrieval_state":         nullableStringValue(data.LastRetrievalState),
		"last_retrieval_error":         nullableStringValue(data.LastRetrievalError),
		"last_retrieval_error_details": nullableStringValue(data.LastRetrievalErrorDetails),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpdateCandidateDispatchState stores study-service dispatch state for an ingestion candidate
func (repository *InferenceCommandRepository) UpdateCandidateDispatchState(data types.UpdateCandidateDispatchState) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`UPDATE %s
SET last_dispatch_error = :last_dispatch_error,
	last_dispatch_attempted_at = COALESCE(:last_dispatch_attempted_at, CURRENT_TIMESTAMP)
WHERE id = :id`, candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                         data.ID,
		"last_dispatch_error":        nullableStringValue(data.LastDispatchError),
		"last_dispatch_attempted_at": nullableTimeValue(data.LastDispatchAttemptedAt),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateRetrievalQueued marks an ingestion candidate as retrieval queued
func (repository *InferenceCommandRepository) MarkCandidateRetrievalQueued(ID string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`UPDATE %s
SET status = :status,
	retrieval_queued_at = CURRENT_TIMESTAMP,
	orthanc_job_ids = NULL,
	last_retrieval_state = 'queued',
	last_retrieval_error = NULL,
	last_retrieval_error_details = NULL,
	last_retrieval_checked_at = CURRENT_TIMESTAMP
WHERE id = :id`, candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":     ID,
		"status": entity.InferenceIngestionCandidateStatusRetrievalQueued,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateRetrieved marks an ingestion candidate as retrieved
func (repository *InferenceCommandRepository) MarkCandidateRetrieved(ID string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET status = :status, retrieved_at = CURRENT_TIMESTAMP, last_retrieval_checked_at = CURRENT_TIMESTAMP WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":     ID,
		"status": entity.InferenceIngestionCandidateStatusRetrieved,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateRetrievedWithContext marks an ingestion candidate as retrieved with retrieval context
func (repository *InferenceCommandRepository) MarkCandidateRetrievedWithContext(data types.UpdateCandidateRetrievalState) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`UPDATE %s
SET status = :status,
	retrieved_at = CURRENT_TIMESTAMP,
	orthanc_job_ids = COALESCE(:orthanc_job_ids, orthanc_job_ids),
	last_retrieval_state = :last_retrieval_state,
	last_retrieval_error = :last_retrieval_error,
	last_retrieval_error_details = :last_retrieval_error_details,
	last_retrieval_checked_at = CURRENT_TIMESTAMP
WHERE id = :id`, candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                           data.ID,
		"status":                       entity.InferenceIngestionCandidateStatusRetrieved,
		"orthanc_job_ids":              nullableStringArrayValue(data.OrthancJobIDs),
		"last_retrieval_state":         nullableStringValue(data.LastRetrievalState),
		"last_retrieval_error":         nullableStringValue(data.LastRetrievalError),
		"last_retrieval_error_details": nullableStringValue(data.LastRetrievalErrorDetails),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateDisappeared marks an ingestion candidate as disappeared
func (repository *InferenceCommandRepository) MarkCandidateDisappeared(ID string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET status = :status WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":     ID,
		"status": entity.InferenceIngestionCandidateStatusDisappeared,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateFailed marks an ingestion candidate as failed
func (repository *InferenceCommandRepository) MarkCandidateFailed(ID string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET status = :status, last_retrieval_checked_at = CURRENT_TIMESTAMP WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":     ID,
		"status": entity.InferenceIngestionCandidateStatusFailed,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// MarkCandidateFailedWithContext marks an ingestion candidate as failed with retrieval context
func (repository *InferenceCommandRepository) MarkCandidateFailedWithContext(data types.UpdateCandidateRetrievalState) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf(`UPDATE %s
SET status = :status,
	orthanc_job_ids = COALESCE(:orthanc_job_ids, orthanc_job_ids),
	last_retrieval_state = COALESCE(:last_retrieval_state, last_retrieval_state),
	last_retrieval_error = COALESCE(:last_retrieval_error, last_retrieval_error),
	last_retrieval_error_details = COALESCE(:last_retrieval_error_details, last_retrieval_error_details),
	last_retrieval_checked_at = CURRENT_TIMESTAMP
WHERE id = :id`, candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id":                           data.ID,
		"status":                       entity.InferenceIngestionCandidateStatusFailed,
		"orthanc_job_ids":              nullableStringArrayValue(data.OrthancJobIDs),
		"last_retrieval_state":         nullableStringValue(data.LastRetrievalState),
		"last_retrieval_error":         nullableStringValue(data.LastRetrievalError),
		"last_retrieval_error_details": nullableStringValue(data.LastRetrievalErrorDetails),
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// IncrementCandidateMissingPolls increments missing polls of an ingestion candidate
func (repository *InferenceCommandRepository) IncrementCandidateMissingPolls(ID string) error {
	var candidate entity.InferenceIngestionCandidate

	stmt := fmt.Sprintf("UPDATE %s SET missing_polls = missing_polls + 1 WHERE id = :id", candidate.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, map[string]interface{}{
		"id": ID,
	})
	if err != nil {
		log.Println(err)
		return errors.New(apiError.DatabaseError)
	}

	return nil
}

// UpsertModelFeedback upserts model feedback
func (repository *InferenceCommandRepository) UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error {
	var modelFeedback entity.ModelFeedback

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	collectionPath := fmt.Sprintf("%s/%s", modelFeedback.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	// try to insert model feedback
	_, err = docRef.Create(ctx, entity.ModelFeedback{
		ID:               data.ID,
		TenantID:         data.TenantID,
		UserID:           data.UserID,
		InferenceModelID: data.InferenceModelID,
		ModelID:          data.ModelID,
		FeedbackType:     data.FeedbackType,
		CreatedAt:        int(time.Now().Unix()),
		UpdatedAt:        int(time.Now().Unix()),
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			// update model feedback
			updateModelFeedback := []firestore.Update{
				{
					Path:  "feedback_type",
					Value: data.FeedbackType,
				},
				{
					Path:  "updated_at",
					Value: int(time.Now().Unix()),
				},
			}

			_, err = docRef.Update(ctx, updateModelFeedback)
			if err != nil {
				log.Println(err)
				return errors.New(apiError.FirestoreError)
			}

			return nil
		}

		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

func nullableStringValue(value *string) interface{} {
	if value == nil {
		return nil
	}

	return *value
}

func nullableIntValue(value *int) interface{} {
	if value == nil {
		return nil
	}

	return *value
}

func nullableInt64Value(value *int64) interface{} {
	if value == nil {
		return nil
	}

	return *value
}

func nullableStringArrayValue(value []string) interface{} {
	if len(value) == 0 {
		return nil
	}

	return pq.Array(value)
}

func nullableTimeValue(value *time.Time) interface{} {
	if value == nil {
		return nil
	}

	return *value
}
