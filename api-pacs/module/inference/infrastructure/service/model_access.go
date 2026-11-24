package service

import (
	"context"
	"errors"
	"strings"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
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
	if allow == nil {
		return nil
	}
	directory := service.loadViewerModelDirectory(ctx, tenantID)
	// A stopped or busy container has no live model info: use the last good
	// identity (as /model/proxy/available does) before calling it unknown.
	for index := range directory.entries {
		entry := &directory.entries[index]
		if entry.info != nil {
			continue
		}
		if cached, ok := service.lastModelInfo(ctx, strings.TrimSpace(entry.model.ContainerID)); ok && strings.TrimSpace(cached.ModelID) != "" {
			entry.info = &dockerInferenceTypes.ModelInfo{ModelID: cached.ModelID, ModelName: cached.ModelName, Version: cached.Version}
		}
	}
	entry := directory.match(modelName, modelVersion)
	if entry == nil {
		return errors.New(apiError.ForbiddenAccess)
	}
	if modelID := strings.TrimSpace(entry.artifactModelID()); modelID == "" || !allow(modelID) {
		return errors.New(apiError.ForbiddenAccess)
	}
	return nil
}
