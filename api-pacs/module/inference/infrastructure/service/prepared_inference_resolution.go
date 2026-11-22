package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/docker/docker/errdefs"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
)

var preparedContainerName = regexp.MustCompile(`^/?[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var preparedContainerID = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)
var preparedCanonicalContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Resolve stable names only through the requesting tenant's current registrations.
// An old container retained for rollback must never receive new predictions.
func (service *InferenceCommandService) resolvePreparedInferenceContainer(ctx context.Context, tenantID, ref string) (dockerTypes.GetContainerInfoResult, error) {
	if !preparedContainerName.MatchString(ref) {
		return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.InvalidPayload)
	}
	info, inspectErr := service.DockerSDKInterface.GetContainerInfo(ctx, ref)
	if inspectErr == nil {
		if !validPreparedContainer(info) {
			return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.DockerError)
		}
		if err := service.authorizePreparedContainer(ctx, tenantID, info.ID); err == nil {
			return info, nil
		} else if err.Error() != apiError.MissingRecord {
			return dockerTypes.GetContainerInfoResult{}, err
		}
	} else if !errdefs.IsNotFound(inspectErr) {
		return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}
	// Canonical and abbreviated IDs are identities, never stable routing aliases.
	if preparedContainerID.MatchString(strings.TrimPrefix(ref, "/")) {
		return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.MissingRecord)
	}
	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil {
		return dockerTypes.GetContainerInfoResult{}, err
	}
	matches := map[string]dockerTypes.GetContainerInfoResult{}
	seen := map[string]bool{}
	for _, model := range models {
		id := strings.TrimSpace(model.ContainerID)
		if model.TenantID != tenantID || !preparedCanonicalContainerID.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		candidate, err := service.DockerSDKInterface.GetContainerInfo(ctx, id)
		if errdefs.IsNotFound(err) {
			continue // A missing container cannot be an active routing target.
		}
		if err != nil || !validPreparedContainer(candidate) || candidate.ID != id {
			return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.DockerError)
		}
		name := strings.TrimPrefix(ref, "/")
		if strings.EqualFold(strings.TrimPrefix(candidate.Name, "/"), name) || strings.EqualFold(candidate.PlacementName, name) {
			matches[id] = candidate
		}
	}
	if len(matches) != 1 {
		return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.MissingRecord)
	}
	for _, candidate := range matches {
		// Recheck authorization after discovery so stale registrations fail closed.
		if err := service.authorizePreparedContainer(ctx, tenantID, candidate.ID); err != nil {
			return dockerTypes.GetContainerInfoResult{}, err
		}
		return candidate, nil
	}
	return dockerTypes.GetContainerInfoResult{}, errors.New(apiError.MissingRecord)
}

func validPreparedContainer(info dockerTypes.GetContainerInfoResult) bool {
	return preparedCanonicalContainerID.MatchString(info.ID) && strings.TrimPrefix(strings.TrimSpace(info.Name), "/") != ""
}

func (service *InferenceCommandService) authorizePreparedContainer(ctx context.Context, tenantID, id string) error {
	model, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if model.TenantID != tenantID || strings.TrimSpace(model.ContainerID) != id {
		return errors.New(apiError.MissingRecord)
	}
	return nil
}
