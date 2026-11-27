package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"api-pacs/module/inference/domain/entity"
)

type activeDeploymentProvider interface {
	ActiveDeployments(context.Context) (entity.ActiveDeploymentRegistry, error)
}

// GetActiveDeployments is private to the trusted study-service identity. The
// response contains all tenants so every worker can initialize after restart.
func (controller *InferenceCommandController) GetActiveDeployments(w http.ResponseWriter, r *http.Request) {
	if !authorizeStudyService(w, r) {
		return
	}
	provider, ok := controller.InferenceCommandServiceInterface.(activeDeploymentProvider)
	if !ok {
		http.Error(w, "Deployment registry unavailable", http.StatusServiceUnavailable)
		return
	}
	registry, err := provider.ActiveDeployments(r.Context())
	if err != nil {
		http.Error(w, "Deployment registry unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(registry)
}
