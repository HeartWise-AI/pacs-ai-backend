package types

import "api-pacs/module/inference/domain/entity"

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
