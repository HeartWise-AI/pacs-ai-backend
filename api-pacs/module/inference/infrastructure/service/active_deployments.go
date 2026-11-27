package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

// This inventory is available only to the authenticated internal service. Public
// model listing remains tenant scoped and must never call this repository method.
type activeModelReader interface {
	ListActiveModelRegistrations(context.Context) ([]entity.InferenceModel, error)
}

func (service *InferenceCommandService) ResolveDispatchDeployment(ctx context.Context, job entity.InferenceIngestionJob) (*entity.ActiveDeployment, error) {
	model, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, job.TenantID, job.ContainerID)
	if err != nil {
		return nil, err
	}
	if model.TenantID != job.TenantID || model.ContainerID != job.ContainerID {
		return nil, errors.New(apiError.MissingRecord)
	}
	deployment, err := service.activeDeployment(ctx, model)
	if err != nil {
		return nil, err
	}
	if deployment.ModelName != job.ModelName || deployment.ModelVersion != job.ModelVersion {
		return nil, errors.New(apiError.InferenceUpgradeConflict)
	}
	return &deployment, nil
}

func (service *InferenceCommandService) ActiveDeployments(ctx context.Context) (entity.ActiveDeploymentRegistry, error) {
	reader, ok := service.InferenceQueryRepositoryInterface.(activeModelReader)
	if !ok {
		return entity.ActiveDeploymentRegistry{}, errors.New(apiError.MissingConfiguration)
	}
	models, err := reader.ListActiveModelRegistrations(ctx)
	if err != nil {
		return entity.ActiveDeploymentRegistry{}, err
	}
	registry := entity.ActiveDeploymentRegistry{SchemaVersion: 1, Models: make([]entity.ActiveDeployment, 0, len(models))}
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return registry, err
		}
		if model.TenantID == "" || model.ID == "" {
			return registry, errors.New(apiError.InvalidPayload)
		}
		entry, err := service.activeDeployment(ctx, model)
		if ctx.Err() != nil {
			return registry, ctx.Err()
		}
		if err != nil {
			// Retain a tombstone instead of presenting an unreachable/invalid runtime
			// as a valid deployment or retaining the previously cached version.
			entry = entity.ActiveDeployment{TenantID: model.TenantID, RegistrationID: model.ID, ModelName: model.Name, State: "UNAVAILABLE"}
		}
		registry.Models = append(registry.Models, entry)
	}
	sort.Slice(registry.Models, func(i, j int) bool {
		a, b := registry.Models[i], registry.Models[j]
		return a.TenantID+"/"+a.RegistrationID < b.TenantID+"/"+b.RegistrationID
	})
	encoded, _ := json.Marshal(registry.Models)
	sum := sha256.Sum256(encoded)
	registry.Revision = hex.EncodeToString(sum[:])
	return registry, nil
}

func (service *InferenceCommandService) activeDeployment(ctx context.Context, model entity.InferenceModel) (entity.ActiveDeployment, error) {
	entry := entity.ActiveDeployment{TenantID: model.TenantID, RegistrationID: model.ID, ContainerID: model.ContainerID, State: "READY"}
	info, err := service.DockerSDKInterface.GetContainerInfo(ctx, model.ContainerID)
	if err != nil || info.ID != model.ContainerID || !validPreparedContainer(info) || info.ImageID == "" {
		return entry, errors.New(apiError.DockerError)
	}
	entry.LocalImageID = info.ImageID
	if d := model.Deployment; d != nil {
		if d.ContainerID != info.ID || d.LocalImageID != info.ImageID || d.ImageReference != model.DockerImage || d.ModelVersion == "" {
			return entry, errors.New(apiError.InferenceUpgradeConflict)
		}
		entry.ModelVersion, entry.CatalogVersionID, entry.ImageDigest = d.ModelVersion, d.CatalogVersionID, d.ImageDigest
		entry.ModelName = strings.TrimSpace(d.ModelName)
	}
	if entry.ModelName == "" {
		// Registration names are editable display names, not artifact names.
		// Older snapshots lack ModelName; recover it from immutable release labels
		// or the runtime contract without assuming it equals the display name.
		var image dockerTypes.InspectImageResult
		imageErr := errors.New("image inspection unavailable")
		if inspector, ok := service.DockerSDKInterface.(interface {
			InspectImage(context.Context, string) (dockerTypes.InspectImageResult, error)
		}); ok {
			image, imageErr = inspector.InspectImage(ctx, info.ImageID)
		}
		if imageErr == nil && image.ID == info.ImageID {
			name := strings.TrimSpace(image.Labels["org.opencontainers.image.title"])
			version := strings.TrimSpace(image.Labels["org.opencontainers.image.version"])
			// HeartWise release provenance distinguishes a model title from an
			// inherited OS title when the registration has a custom display name.
			if name != "" && version != "" && (name == model.Name || image.Labels["ai.heartwise.model.repository"] != "") {
				if entry.ModelVersion != "" && entry.ModelVersion != version {
					return entry, errors.New(apiError.InferenceUpgradeConflict)
				}
				entry.ModelName, entry.ModelVersion = name, version
			}
		}
		if entry.ModelName == "" {
			if service.DockerInferenceAPIInterface == nil {
				return entry, errors.New(apiError.DockerInferenceError)
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			metadata, err := service.DockerInferenceAPIInterface.GetModelInfo(probeCtx, strings.TrimPrefix(info.Name, "/"))
			cancel()
			if err != nil || !metadata.Success || metadata.Data.ModelName == "" || metadata.Data.Version == "" {
				return entry, errors.New(apiError.DockerInferenceError)
			}
			if entry.ModelVersion != "" && entry.ModelVersion != metadata.Data.Version {
				return entry, errors.New(apiError.InferenceUpgradeConflict)
			}
			entry.ModelName, entry.ModelVersion = metadata.Data.ModelName, metadata.Data.Version
		}
	}
	return entry, nil
}

// PinPreparedDeployment is called before both buffered and streamed predictions.
// The selected container ID cannot be retargeted by an upgrade while GPU
// admission waits; replacement/removal fails instead of changing artifacts.
func (service *InferenceCommandService) PinPreparedDeployment(ctx context.Context, tenantID string, expected entity.ActiveDeployment) (string, error) {
	if tenantID == "" || expected.TenantID != tenantID || expected.RegistrationID == "" || expected.ModelVersion == "" || expected.ContainerID == "" || expected.LocalImageID == "" {
		return "", errors.New(apiError.InvalidPayload)
	}
	model, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, expected.RegistrationID)
	if err != nil {
		return "", err
	}
	if model.TenantID != tenantID {
		return "", errors.New(apiError.MissingRecord)
	}
	current, err := service.activeDeployment(ctx, model)
	if err != nil {
		return "", err
	}
	if current != expected {
		return "", errors.New(apiError.InferenceUpgradeConflict)
	}
	return current.ContainerID, nil
}
