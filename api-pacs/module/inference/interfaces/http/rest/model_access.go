package rest

import (
	"context"
	"net/http"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	types "api-pacs/module/inference/infrastructure/service/types"
)

// ModelArtifactResolver maps a viewer model reference to model-info modelIds.
type ModelArtifactResolver interface {
	ResolveModelArtifactIDs(ctx context.Context, tenantID, ref string) ([]string, error)
}

// authorizeModelRef checks that the signed-in user's custom role allows the
// referenced model. It returns "" when allowed, otherwise an error code:
// ForbiddenAccess, or AccessUnavailable when it cannot be decided.
func authorizeModelRef(r *http.Request, resolver ModelArtifactResolver, tenantID, ref string) string {
	access, err := accessctx.Get(r.Context())
	if err != nil {
		return apiError.AccessUnavailable
	}
	if access.AllModels {
		return ""
	}
	if resolver == nil {
		return apiError.AccessUnavailable
	}
	modelIDs, err := resolver.ResolveModelArtifactIDs(r.Context(), tenantID, ref)
	if err != nil {
		switch err.Error() {
		case apiError.MissingRecord, apiError.InvalidPayload:
			return apiError.ForbiddenAccess
		default:
			return apiError.AccessUnavailable
		}
	}
	for _, modelID := range modelIDs {
		if !access.AllowsModel(modelID) {
			return apiError.ForbiddenAccess
		}
	}
	return ""
}

// modelAccessFilter returns the signed-in user's access, or an error code.
func modelAccessFilter(r *http.Request) (accessEntity.Access, string) {
	access, err := accessctx.Get(r.Context())
	if err != nil {
		return accessEntity.Access{}, apiError.AccessUnavailable
	}
	return access, ""
}

// filterAvailableModels keeps the models the access allows.
func filterAvailableModels(access accessEntity.Access, models []types.GetInferenceAvailableModelResult) []types.GetInferenceAvailableModelResult {
	if access.AllModels {
		return models
	}
	kept := make([]types.GetInferenceAvailableModelResult, 0, len(models))
	for _, model := range models {
		if access.AllowsModel(model.ModelID) {
			kept = append(kept, model)
		}
	}
	return kept
}

// filterLatestResults keeps results of allowed models; a result whose
// model-info modelId is unknown is dropped for restricted users.
func filterLatestResults(access accessEntity.Access, results types.StudyLatestModelResults) types.StudyLatestModelResults {
	if access.AllModels {
		return results
	}
	kept := make([]types.StudyLatestModelResult, 0, len(results.Results))
	for _, result := range results.Results {
		if result.ArtifactModelID == nil || !access.AllowsModel(*result.ArtifactModelID) {
			continue
		}
		// The listed runs are runs of the same model; one that resolves to
		// another, disallowed registration is left out all the same.
		if len(result.Runs) > 0 {
			runs := make([]types.StudyLatestModelResult, 0, len(result.Runs))
			for _, run := range result.Runs {
				if run.ArtifactModelID != nil && access.AllowsModel(*run.ArtifactModelID) {
					runs = append(runs, run)
				}
			}
			result.Runs = runs
		}
		kept = append(kept, result)
	}
	results.Results = kept
	return results
}

func writeModelAccessError(w http.ResponseWriter, code string) {
	status, message := http.StatusForbidden, "This model is not available to your role."
	if code == apiError.AccessUnavailable {
		status, message = http.StatusServiceUnavailable, "Access verification is temporarily unavailable."
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: code}
	response.JSON(w)
}

// resultModelFilter returns the allow-check for stored results: nil for users
// with every model, or an error code when access cannot be read.
func resultModelFilter(r *http.Request) (func(string) bool, string) {
	access, code := modelAccessFilter(r)
	if code != "" {
		return nil, code
	}
	if access.AllModels {
		return nil, ""
	}
	return access.AllowsModel, ""
}
