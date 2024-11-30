package types

import (
	"time"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

type AddInferenceModel struct {
	TenantID    string
	Name        string
	DockerImage string
	Envs        []string
	OutputMode  entity.OutputMode
}

type UpdateInferenceModel struct {
	ID          string
	Name        string
	DockerImage string
	Envs        []string
	OutputMode  entity.OutputMode
}

type PredictInferenceModel struct {
	QueryIDs   []string
	OutputMode entity.OutputMode
}

type GetContainerInfoResult struct {
	ID              string
	Name            string
	Status          dockerTypes.Status
	Running         bool
	StartedAt       time.Time
	FinishedAt      time.Time
	CPUPercentUsage float64 // in percent
	MemoryInBytes   uint64  // in bytes
}

type GetInferenceModelResult struct {
	ID          string
	TenantID    string
	Container   GetContainerInfoResult
	Name        string
	DockerImage string
	Envs        []string
	OutputMode  entity.OutputMode
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type GetInferenceAvailableModelResult struct {
	ContainerID              string
	ModelName                string
	Version                  string
	DicomTargetLevel         string
	DicomUploadMin           int
	DicomUploadMax           int
	SupportedDicomModalities []string
	OutputMode               entity.OutputMode
}
