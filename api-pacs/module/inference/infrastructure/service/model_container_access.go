package service

import (
	"context"
	"errors"
	"strings"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/repository"
)

// tenantModelContainerID returns the container ID registered for one of the
// tenant's models when ref is exactly that canonical ID. Docker also resolves
// container names and ID prefixes, so any other reference is refused before
// it reaches the engine. Callers act on the returned ID and must not resolve
// the registration again.
func tenantModelContainerID(ctx context.Context, models repository.InferenceQueryRepositoryInterface, tenantID, ref string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || !preparedCanonicalContainerID.MatchString(ref) {
		return "", errors.New(apiError.MissingRecord)
	}
	model, err := models.SelectInferenceModelByContainer(ctx, tenantID, ref)
	if err != nil {
		return "", err
	}
	containerID := strings.TrimSpace(model.ContainerID)
	if model.TenantID != tenantID || containerID != ref {
		return "", errors.New(apiError.MissingRecord)
	}
	return containerID, nil
}

// tenantModelContainerInfo inspects the tenant's registered container named
// by ref and refuses an answer about any other container.
func (service *InferenceQueryService) tenantModelContainerInfo(ctx context.Context, tenantID, ref string) (dockerTypes.GetContainerInfoResult, error) {
	containerID, err := tenantModelContainerID(ctx, service.InferenceQueryRepositoryInterface, tenantID, ref)
	if err != nil {
		return dockerTypes.GetContainerInfoResult{}, err
	}
	info, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil || info.ID != containerID {
		return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}
	return info, nil
}
