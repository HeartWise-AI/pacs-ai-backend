package http

import (
	"github.com/go-playground/validator/v10"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"AddInferenceModel.Name":                    "Name is required.",
		"AddInferenceModel.DockerImage":             "Docker image is required.",
		"AddInferenceModel.OutputMode":              "Output mode is required.",
		"PredictInferenceModel.StudyInstanceUID":    "Study Instance UID is required.",
		"PredictInferenceModel.SeriesInstanceUIDs":  "Series Instance UIDs are required.",
		"UpdateInferenceModel.DisallowedDICOMTags":  "Disallowed DICOM tags are required.",
		"UpdateInferenceModel.OutputMode":           "Output mode is required.",
		"UpdateInferenceModelContainer.ContainerID": "Container ID is required.",
	}
)

type AddInferenceModelRequest struct {
	Name        string            `json:"name" validate:"required"`
	DockerImage string            `json:"dockerImage" validate:"required"`
	Envs        []string          `json:"envs"`
	OutputMode  entity.OutputMode `json:"outputMode" validate:"required"`
}

type UpdateInferenceModelRequest struct {
	DisallowedDICOMTags []string          `json:"disallowedDICOMTags" validate:"required"`
	OutputMode          entity.OutputMode `json:"outputMode" validate:"required"`
}

type PredictInferenceModelRequest struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID" validate:"required"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs" validate:"required"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata"`
}

type UpdateInferenceModelContainerRequest struct {
	ContainerID string `json:"containerId" validate:"required"`
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
	ContainerID                 string            `json:"containerId"`
	ContainerName               string            `json:"containerName"`
	ModelName                   string            `json:"modelName"`
	ModelFacts                  ModelFacts        `json:"modelFacts"`
	Version                     string            `json:"version"`
	DicomTargetLevel            string            `json:"dicomTargetLevel"`
	DicomUploadMin              int               `json:"dicomUploadMin"`
	DicomUploadMax              int               `json:"dicomUploadMax"`
	SupportedDicomModalities    []string          `json:"supportedDicomModalities"`
	SupportedDicomTags          []string          `json:"supportedDicomTags"`
	SupportedAdditionalMetadata []interface{}     `json:"supportedAdditionalMetadata"`
	OutputMode                  entity.OutputMode `json:"outputMode"`
}

type ModelFacts struct {
	En map[string]interface{} `json:"en"`
}
