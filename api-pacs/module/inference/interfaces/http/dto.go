package http

import (
	"github.com/go-playground/validator/v10"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"AddInferenceModel.Name":                                                                                "Name is required.",
		"AddInferenceModel.DockerImage":                                                                         "Docker image is required.",
		"AddInferenceModel.OutputMode":                                                                          "Output mode is required.",
		"PredictInferenceModelRequest.StudyInstanceUID":                                                         "Study Instance UID is required.",
		"PredictInferenceModelRequest.SeriesInstanceUIDs":                                                       "Series Instance UIDs are required.",
		"AddOnboardingModelQuestionnaireAnswerRequest.ModelID":                                                  "Model ID is required.",
		"AddOnboardingModelQuestionnaireAnswerRequest.OnboardingModelQuestionnaireAnswers":                      "Questionnaire answers are required.",
		"AddOnboardingModelQuestionnaireAnswerRequest.OnboardingModelQuestionnaireAnswer.QuestionnaireID":       "Questionnaire answer IDs are required.",
		"AddOnboardingModelQuestionnaireAnswerRequest.OnboardingModelQuestionnaireAnswer.QuestionnaireQuestion": "Questionnaire answers are required.",
		"UpdateInferenceModelRequest.DisallowedDICOMTags":                                                       "Disallowed DICOM tags are required.",
		"UpdateInferenceModelRequest.OutputMode":                                                                "Output mode is required.",
		"UpdateInferenceModelContainerRequest.ContainerID":                                                      "Container ID is required.",
		"UpdateModelFeedbackRequest.InferenceModelID":                                                           "Inference Model ID is required.",
		"UpdateModelFeedbackRequest.ModelID":                                                                    "Model ID is required.",
		"UpdateModelFeedbackRequest.FeedbackType":                                                               "Feedback type is required.",
		"ImportInferenceIngestionJob.DICOMModality":                                                             "DICOM modality is required.",
		"ImportInferenceIngestionJob.ContainerID":                                                               "Container ID is required.",
		"ImportInferenceIngestionJob.ModelID":                                                                   "Model ID is required.",
		"ImportInferenceIngestionJob.ModelName":                                                                 "Model name is required.",
		"ImportInferenceIngestionJob.ModelVersion":                                                              "Model version is required.",
		"ImportInferenceIngestionJob.Modalities":                                                                "Modalities are required.",
		"ImportInferenceIngestionJob.IntervalInMinutes":                                                         "Interval in minutes is required.",
	}
)

type AddInferenceModelRequest struct {
	Name        string            `json:"name" validate:"required"`
	DockerImage string            `json:"dockerImage" validate:"required"`
	Envs        []string          `json:"envs"`
	OutputMode  entity.OutputMode `json:"outputMode" validate:"required"`
}

type AddOnboardingModelQuestionnaireAnswerRequest struct {
	ModelID                             string                               `json:"modelId" validate:"required"`
	OnboardingModelQuestionnaireAnswers []OnboardingModelQuestionnaireAnswer `json:"onboardingModelQuestionnaireAnswers" validate:"required"`
}

type CreateInferenceIngestionJobRequest struct {
	DICOMModality          string   `json:"dicomModality" validate:"required"`
	ContainerID            string   `json:"containerId" validate:"required"`
	ModelID                string   `json:"modelId" validate:"required"`
	ModelName              string   `json:"modelName" validate:"required"`
	ModelVersion           string   `json:"modelVersion" validate:"required"`
	Modalities             []string `json:"modalities" validate:"required"`
	IntervalInMinutes      uint     `json:"intervalInMinutes" validate:"required"`
	ScheduleStartTimestamp uint64   `json:"scheduleStartTimestamp"`
	ScheduleEndTimestamp   uint64   `json:"scheduleEndTimestamp"`
}

type PredictInferenceModelRequest struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID" validate:"required"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs" validate:"required"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata"`
	ForceJSON          *bool                  `json:"forceJSON,omitempty"`
}

type UpdateInferenceModelRequest struct {
	DisallowedDICOMTags []string          `json:"disallowedDICOMTags" validate:"required"`
	OutputMode          entity.OutputMode `json:"outputMode" validate:"required"`
}

type UpdateInferenceModelContainerRequest struct {
	ContainerID string `json:"containerId" validate:"required"`
}

type UpdateInferenceIngestionJobRequest struct {
	Modalities             []string `json:"modalities" validate:"required"`
	IntervalInMinutes      uint     `json:"intervalInMinutes" validate:"required"`
	ScheduleStartTimestamp uint64   `json:"scheduleStartTimestamp"`
	ScheduleEndTimestamp   uint64   `json:"scheduleEndTimestamp"`
}

type UpdateModelFeedbackRequest struct {
	ID                   *string               `json:"id"`
	InferenceModelID     string                `json:"inferenceModelId" validate:"required"`
	ModelID              string                `json:"modelId" validate:"required"`
	FeedbackType         entity.FeedbackType   `json:"feedbackType" validate:"required"`
	ModelFeedbackAnswers []ModelFeedbackAnswer `json:"modelFeedbackAnswers"`
}

type GetContainerInfoResponse struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Status          dockerTypes.Status `json:"status"`
	Running         bool               `json:"running"`
	StartedAt       uint64             `json:"startedAt"`
	FinishedAt      uint64             `json:"finishedAt"`
	CPUPercentUsage float64            `json:"cpuPercentUsage"`
	MemoryInBytes   uint64             `json:"memoryInBytes"`
}

type GetInferenceModelResponse struct {
	ID                  string                   `json:"id"`
	TenantID            string                   `json:"tenantId"`
	Container           GetContainerInfoResponse `json:"container"`
	Name                string                   `json:"name"`
	DockerImage         string                   `json:"dockerImage"`
	Envs                []string                 `json:"envs"`
	DisallowedDICOMTags []string                 `json:"disallowedDICOMTags"`
	OutputMode          entity.OutputMode        `json:"outputMode"`
	CreatedAt           uint64                   `json:"createdAt"`
	UpdatedAt           uint64                   `json:"updatedAt"`
}

type GetInferenceAvailableModelResponse struct {
	ContainerID                   string            `json:"containerId"`
	ContainerName                 string            `json:"containerName"`
	ModelID                       string            `json:"modelId"`
	ModelName                     string            `json:"modelName"`
	ModelFacts                    ModelFacts        `json:"modelFacts"`
	Version                       string            `json:"version"`
	DicomTargetLevel              string            `json:"dicomTargetLevel"`
	DicomUploadMin                int               `json:"dicomUploadMin"`
	DicomUploadMax                int               `json:"dicomUploadMax"`
	SupportedDicomModalities      []string          `json:"supportedDicomModalities"`
	SupportedDicomTags            []string          `json:"supportedDicomTags"`
	SupportedAdditionalMetadata   []interface{}     `json:"supportedAdditionalMetadata"`
	ApproveFeedbackQuestionnaires []interface{}     `json:"approveFeedbackQuestionnaires"`
	RejectFeedbackQuestionnaires  []interface{}     `json:"rejectFeedbackQuestionnaires"`
	OnboardingModelQuestionnaires []interface{}     `json:"onboardingModelQuestionnaires"`
	OutputMode                    entity.OutputMode `json:"outputMode"`
}

type GetInferenceIngestionJobResponse struct {
	ID                     string   `json:"id"`
	TenantID               string   `json:"tenantId"`
	DICOMModality          string   `json:"dicomModality"`
	ContainerID            string   `json:"containerId"`
	ModelID                string   `json:"modelId"`
	ModelName              string   `json:"modelName"`
	ModelVersion           string   `json:"modelVersion"`
	Modalities             []string `json:"modalities"`
	IntervalInMinutes      uint     `json:"intervalInMinutes"`
	ScheduleStartTimestamp uint64   `json:"scheduleStartTimestamp"`
	ScheduleEndTimestamp   uint64   `json:"scheduleEndTimestamp"`
	Status                 string   `json:"status"`
	LastExecutedAt         uint64   `json:"lastExecutedAt"`
	CreatedAt              uint64   `json:"createdAt"`
	UpdatedAt              uint64   `json:"updatedAt"`
}

type GetModelFeedbackResponse struct {
	ID                   string                      `json:"id"`
	TenantID             string                      `json:"tenantId"`
	UserID               string                      `json:"userId"`
	InferenceModelID     string                      `json:"inferenceModelId"`
	ModelID              string                      `json:"modelId"`
	FeedbackType         entity.FeedbackType         `json:"feedbackType"`
	ModelFeedbackAnswers []ModelFeedbackAnswerResult `json:"modelFeedbackAnswers"`
}

type GetOnboardingModelQuestionnaireAnswerResponse struct {
	ID                     string   `json:"id"`
	TenantID               string   `json:"tenantId"`
	UserID                 string   `json:"userId"`
	ModelID                string   `json:"modelId"`
	QuestionnaireID        string   `json:"questionnaireId"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
	CreatedAt              uint64   `json:"createdAt"`
	UpdatedAt              uint64   `json:"updatedAt"`
}

type ModelFacts struct {
	En map[string]interface{} `json:"en"`
}

type ModelFeedbackAnswer struct {
	QuestionnaireID        string   `json:"questionnaireId"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
}

type ModelFeedbackAnswerResult struct {
	ID                     string   `json:"id"`
	ModelFeedbackID        string   `json:"modelFeedbackId"`
	QuestionnaireID        string   `json:"questionnaireId"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
}

type OnboardingModelQuestionnaireAnswer struct {
	QuestionnaireID        string   `json:"questionnaireId" validate:"required"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion" validate:"required"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
}

type ImportInferenceIngestionJob struct {
	DICOMModality          string `csv:"dicom_modality" validate:"required"`
	ContainerID            string `csv:"container_id" validate:"required"`
	ModelID                string `csv:"model_id" validate:"required"`
	ModelName              string `csv:"model_name" validate:"required"`
	ModelVersion           string `csv:"model_version" validate:"required"`
	Modalities             string `csv:"modalities" validate:"required"`
	IntervalInMinutes      uint   `csv:"interval_in_minutes" validate:"required"`
	ScheduleStartTimestamp string `csv:"schedule_start_timestamp"`
	ScheduleEndTimestamp   string `csv:"schedule_end_timestamp"`
}
