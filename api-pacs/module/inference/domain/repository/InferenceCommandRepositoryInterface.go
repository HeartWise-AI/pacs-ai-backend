package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type InferenceCommandRepositoryInterface interface {
	// DeleteInferenceModel deletes an inference model
	DeleteInferenceModel(ctx context.Context, ID string) error
	// DeleteInferenceIngestionJob deletes an inference ingestion job
	DeleteInferenceIngestionJob(ID string) error
	// DeleteModelFeedback deletes model feedback
	DeleteModelFeedback(ctx context.Context, ID string) error
	// DeleteModelFeedbackAnswer deletes model feedback answer
	DeleteModelFeedbackAnswer(ctx context.Context, ID string) error
	// DeleteOnboardingModelQuestionnaireAnswer deletes onboarding model questionnaire answer
	DeleteOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error
	// InsertModelFeedbackAnswer inserts a new  model feedback answer
	InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error
	// InsertInferenceModel inserts an inference model
	InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error
	// InsertInferenceIngestionJob inserts a new inference ingestion job
	InsertInferenceIngestionJob(data types.CreateInferenceIngestionJob) error
	// DeleteInferenceIngestionJobByTenantContainerID deletes inference ingestion jobs by tenant ID and container ID
	DeleteInferenceIngestionJobByTenantContainerID(tenantID, containerID string) error
	// InsertInferenceIngestionRunResult inserts a new inference ingestion run result
	InsertInferenceIngestionRunResult(data types.AddInferenceIngestionRunResult) error
	// InsertOnboardingModelQuestionnaireAnswer inserts an onboarding model questionnaire answer
	InsertOnboardingModelQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateInferenceIngestionJob updates an inference ingestion job
	UpdateInferenceIngestionJob(data types.UpdateInferenceIngestionJob) error
	// UpdateInferenceIngestionJobStatus updates the status of an inference ingestion job
	UpdateInferenceIngestionJobStatus(ID string, status entity.InferenceIngestionJobStatus) error
	// UpdateInferenceIngestionJobLastExecutedAt updates last executed at of infererence ingestion job
	UpdateInferenceIngestionJobLastExecutedAt(ID string) error
	// UpdateInferenceModelContainerID updates the container ID of an inference model
	UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error
	// UpsertModelFeedback upserts model feedback
	UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error
}
