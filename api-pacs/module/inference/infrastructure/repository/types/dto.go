package types

import "api-pacs/module/inference/domain/entity"

type AddInferenceModel struct {
	ID                  string
	TenantID            string
	ContainerID         string
	Name                string
	DockerImage         string
	Envs                []string
	DisallowedDICOMTags []string
	OutputMode          entity.OutputMode
}

type UpdateInferenceModel struct {
	ID                  string
	DisallowedDICOMTags []string
	OutputMode          entity.OutputMode
}
