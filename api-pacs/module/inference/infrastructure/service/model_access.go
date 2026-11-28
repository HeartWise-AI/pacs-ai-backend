package service

import (
	"context"
	"errors"
	"strings"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/service/types"
)

// ResolveModelArtifactIDs maps a viewer model reference (Firestore
// registration ID, container ID or model-info modelId) to the model-info
// modelIds of the tenant's matching registrations, which is what custom-role
// model access is keyed on. No match returns MissingRecord. A match whose
// modelId cannot be determined returns AccessUnavailable, so callers deny.
func (service *InferenceQueryService) ResolveModelArtifactIDs(ctx context.Context, tenantID, ref string) ([]string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New(apiError.InvalidPayload)
	}
	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return nil, err
		}
		return nil, errors.New(apiError.AccessUnavailable)
	}
	matched := []string{}
	unknown := false
	for _, model := range models {
		if model.TenantID != tenantID || strings.TrimSpace(model.ContainerID) == "" {
			continue
		}
		artifactID := ""
		if deployment := currentIngestionDeployment(model); deployment != nil {
			artifactID = strings.TrimSpace(deployment.ArtifactModelID)
		}
		if artifactID == "" {
			if cached, ok := service.lastModelInfo(ctx, model.ContainerID); ok {
				artifactID = strings.TrimSpace(cached.ModelID)
			}
		}
		if artifactID == "" && (model.ID == ref || model.ContainerID == ref) {
			artifactID = service.probeModelArtifactID(ctx, model.ContainerID)
		}
		if model.ID != ref && model.ContainerID != ref && (artifactID == "" || artifactID != ref) {
			continue
		}
		if artifactID == "" {
			unknown = true
			continue
		}
		matched = append(matched, artifactID)
	}
	if unknown {
		return nil, errors.New(apiError.AccessUnavailable)
	}
	if len(matched) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}
	return matched, nil
}

// authorizeResultModel applies custom-role model access to a stored result,
// identified by model name and version as recorded with the execution. A nil
// allow accepts everything; a model that cannot be matched to a registration
// with a known modelId is refused.
func (service *InferenceQueryService) authorizeResultModel(ctx context.Context, tenantID, modelName, modelVersion string, allow func(string) bool) error {
	if allow == nil || types.IsStudyServiceMeasurementResult(modelName) {
		return nil
	}
	directory := service.loadViewerModelDirectory(ctx, tenantID)
	entry := directory.match(modelName, modelVersion)
	modelID := ""
	if entry != nil {
		modelID = strings.TrimSpace(entry.artifactModelID())
	}
	// allow("") is true only for access to every model except research-only
	// ones: a result whose model is unknown is then not a research result.
	// Allowlist access rejects "" and keeps failing closed.
	if modelID == "" {
		if allow("") {
			return nil
		}
		return errors.New(apiError.ForbiddenAccess)
	}
	if !allow(modelID) {
		return errors.New(apiError.ForbiddenAccess)
	}
	return nil
}

// probeModelArtifactID asks a running container for its model-info modelId,
// bounded by the viewer probe timeout. It returns "" when the container is
// not running or does not answer, so callers keep failing closed.
func (service *InferenceQueryService) probeModelArtifactID(ctx context.Context, containerID string) string {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" || service.DockerSDKInterface == nil || service.DockerInferenceAPIInterface == nil {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, viewerModelInfoProbeTimeout)
	defer cancel()
	info, err := service.DockerSDKInterface.GetContainerInfo(probeCtx, containerID)
	if err != nil || info.Lifecycle != dockerTypes.LifecycleRunning {
		return ""
	}
	name := strings.TrimPrefix(strings.TrimSpace(info.Name), "/")
	if name == "" {
		return ""
	}
	response, err := service.DockerInferenceAPIInterface.GetModelInfo(probeCtx, name)
	if err != nil || !response.Success {
		return ""
	}
	return strings.TrimSpace(response.Data.ModelID)
}
