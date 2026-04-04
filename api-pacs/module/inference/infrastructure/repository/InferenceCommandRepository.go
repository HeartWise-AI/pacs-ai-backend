package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackc/pgx/v5/pgconn"
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
		IntervalInMinutes:      data.IntervalInMinutes,
		ScheduleStartTimestamp: data.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   data.ScheduleEndTimestamp,
		Status:                 data.Status,
	}

	stmt := fmt.Sprintf("INSERT INTO %s (id, tenant_id, dicom_modality, container_id, model_id, model_name, model_version, modalities, interval_in_minutes, schedule_start_timestamp, schedule_end_timestamp, status) "+
		"VALUES (:id, :tenant_id, :dicom_modality, :container_id, :model_id, :model_name, :model_version, :modalities, :interval_in_minutes, :schedule_start_timestamp, :schedule_end_timestamp, :status)", job.GetModelName())
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

// InsertInferenceIngestionRunResult inserts a new inference ingestion run result
func (repository *InferenceCommandRepository) InsertInferenceIngestionRunResult(data types.AddInferenceIngestionRunResult) error {
	result := entity.InferenceIngestionRunResult{
		ID:               data.ID,
		JobID:            data.JobID,
		StudyInstanceUID: data.StudyInstanceUID,
		InferenceOutput:  data.InferenceOutput,
		ErrorMessage:     data.ErrorMessage,
		Status:           data.Status,
	}

	stmt := fmt.Sprintf("INSERT INTO %s (id, job_id, study_instance_uid, inference_output, error_message, status) "+
		"VALUES (:id, :job_id, :study_instance_uid, :inference_output, :error_message, :status)", result.GetModelName())
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(stmt, result)
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

// UpdateInferenceModelContainerID updates the container ID of an inference model
func (repository *InferenceCommandRepository) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update inference model
	var model entity.InferenceModel

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	updateInferenceModel := []firestore.Update{
		{
			Path:  "container_id",
			Value: containerID,
		},
		{
			Path:  "updated_at",
			Value: int(time.Now().Unix()),
		},
	}

	_, err = docRef.Update(ctx, updateInferenceModel)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateInferenceIngestionJob updates an inference ingestion job
func (repository *InferenceCommandRepository) UpdateInferenceIngestionJob(data types.UpdateInferenceIngestionJob) error {
	job := &entity.InferenceIngestionJob{
		ID:                     data.ID,
		Modalities:             data.Modalities,
		IntervalInMinutes:      data.IntervalInMinutes,
		ScheduleStartTimestamp: data.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   data.ScheduleEndTimestamp,
	}

	stmt := fmt.Sprintf("UPDATE %s SET modalities = :modalities, interval_in_minutes = :interval_in_minutes, "+
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
