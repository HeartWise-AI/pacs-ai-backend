package http

import (
	"github.com/go-playground/validator/v10"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"AddInferenceModel.Name":                           "Name is required.",
		"AddInferenceModel.DockerImage":                    "Docker image is required.",
		"AddInferenceModel.OutputMode":                     "Output mode is required.",
		"PredictInferenceModelRequest.StudyInstanceUID":    "Study Instance UID is required.",
		"PredictInferenceModelRequest.SeriesInstanceUIDs":  "Series Instance UIDs are required.",
		"UpdateInferenceModelRequest.DisallowedDICOMTags":  "Disallowed DICOM tags are required.",
		"UpdateInferenceModelRequest.OutputMode":           "Output mode is required.",
		"UpdateInferenceModelContainerRequest.ContainerID": "Container ID is required.",
		"UpdateModelFeedbackRequest.InferenceModelID":      "Inference Model ID is required.",
		"UpdateModelFeedbackRequest.ModelID":               "Model ID is required.",
		"UpdateModelFeedbackRequest.FeedbackType":          "Feedback type is required.",
	}
)

type AddInferenceModelRequest struct {
	Name        string            `json:"name" validate:"required"`
	DockerImage string            `json:"dockerImage" validate:"required"`
	Envs        []string          `json:"envs"`
	OutputMode  entity.OutputMode `json:"outputMode" validate:"required"`
}

type SaveOnboardingQuestionnaireAnswerRequest struct {
	QuestionnaireType      entity.QuestionnaireType `json:"questionnaireType" validate:"required"`
	QuestionnaireID        string                   `json:"questionnaireId" validate:"required"`
	QuestionnaireQuestion  string                   `json:"questionnaireQuestion" validate:"required"`
	QuestionnaireAnswerIDs []string                 `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string                 `json:"questionnaireAnswers"`
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
	OutputMode                    entity.OutputMode `json:"outputMode"`
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

type GetOnboardingQuestionnaireAnswerResponse struct {
	ID                     string                   `json:"id"`
	TenantID               string                   `json:"tenantId"`
	UserID                 string                   `json:"userId"`
	QuestionnaireType      entity.QuestionnaireType `json:"questionnaireType"`
	QuestionnaireID        string                   `json:"questionnaireId"`
	QuestionnaireQuestion  string                   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string                 `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string                 `json:"questionnaireAnswers"`
	CreatedAt              uint64                   `json:"createdAt"`
	UpdatedAt              uint64                   `json:"updatedAt"`
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
