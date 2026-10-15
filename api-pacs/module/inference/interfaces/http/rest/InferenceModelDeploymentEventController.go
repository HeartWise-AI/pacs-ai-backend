package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

const (
	defaultModelDeploymentEventHeartbeatInterval = 20 * time.Second
	defaultModelDeploymentEventBuffer            = 32
	modelDeploymentEventName                     = "model-deployment"
)

// StreamInferenceModelDeploymentEvents sends a durable initial snapshot and
// subsequent committed changes for exactly one tenant-scoped deployment.
func (controller *InferenceCommandController) StreamInferenceModelDeploymentEvents(
	w http.ResponseWriter,
	r *http.Request,
) {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	modelID := strings.TrimSpace(chi.URLParam(r, "ID"))
	deploymentID := strings.TrimSpace(chi.URLParam(r, "deploymentID"))
	if strings.TrimSpace(tenantID) == "" {
		writeModelDeploymentEventError(w, http.StatusUnauthorized, "Authentication is required.", apiError.UnauthorizedAccess)
		return
	}
	if modelID == "" || deploymentID == "" {
		writeModelDeploymentEventError(w, http.StatusBadRequest, "Invalid model deployment event request.", apiError.InvalidPayload)
		return
	}
	if controller.InferenceCommandServiceInterface == nil || controller.ModelDeploymentOperationSubscriberInterface == nil {
		writeModelDeploymentEventError(w, http.StatusServiceUnavailable, "Model deployment events are unavailable.", apiError.InferenceUpgradeUnavailable)
		return
	}
	lastEventID, err := parseModelDeploymentLastEventID(r.Header.Get("Last-Event-ID"))
	if err != nil {
		writeModelDeploymentEventError(w, http.StatusBadRequest, "Invalid model deployment event cursor.", apiError.InvalidPayload)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is unsupported.", http.StatusInternalServerError)
		return
	}

	// Subscribe before loading Firestore so an update committed between both
	// operations remains buffered and is filtered by its durable sequence.
	events, unsubscribe := controller.SubscribeModelDeploymentOperation(
		tenantID, modelID, deploymentID, defaultModelDeploymentEventBuffer,
	)
	defer unsubscribe()
	operation, err := controller.InferenceCommandServiceInterface.GetInferenceModelDeploymentOperation(
		r.Context(),
		serviceTypes.GetInferenceModelDeploymentOperation{
			TenantID: tenantID, ModelID: modelID, DeploymentID: deploymentID,
		},
	)
	if err != nil {
		statusCode, message, errorCode := modelDeploymentEventHTTPError(err)
		writeModelDeploymentEventError(w, statusCode, message, errorCode)
		return
	}
	if lastEventID > operation.Sequence {
		writeModelDeploymentEventError(w, http.StatusBadRequest, "Invalid model deployment event cursor.", apiError.InvalidPayload)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// A reconnect also receives the authoritative current snapshot. Repeating
	// the cursor sequence is intentional and lets clients rebuild UI state
	// without relying on an ephemeral event history.
	if err = writeModelDeploymentServerSentEvent(w, operation); err != nil {
		return
	}
	lastSent := operation.Sequence
	flusher.Flush()
	if operation.Terminal() {
		return
	}
	if _, err = fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(controller.modelDeploymentEventHeartbeatInterval())
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case operation, open := <-events:
			if !open {
				return
			}
			if operation.Sequence <= lastSent {
				continue
			}
			if err = writeModelDeploymentServerSentEvent(w, operation); err != nil {
				return
			}
			lastSent = operation.Sequence
			flusher.Flush()
			if operation.Terminal() {
				return
			}
		case <-heartbeat.C:
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (controller *InferenceCommandController) modelDeploymentEventHeartbeatInterval() time.Duration {
	if controller.ModelDeploymentEventHeartbeatInterval > 0 {
		return controller.ModelDeploymentEventHeartbeatInterval
	}
	return defaultModelDeploymentEventHeartbeatInterval
}

func parseModelDeploymentLastEventID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil || sequence < 1 {
		return 0, fmt.Errorf("invalid event sequence")
	}
	return sequence, nil
}

func writeModelDeploymentServerSentEvent(
	w http.ResponseWriter,
	operation entity.InferenceModelDeploymentOperation,
) error {
	payload, err := json.Marshal(modelDeploymentEventResponse(operation))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		w, "id: %d\nevent: %s\ndata: %s\n\n",
		operation.Sequence, modelDeploymentEventName, payload,
	)
	return err
}

func modelDeploymentEventResponse(
	operation entity.InferenceModelDeploymentOperation,
) types.InferenceModelDeploymentEventResponse {
	startedAt := operation.StartedAt
	if startedAt == 0 {
		startedAt = operation.CreatedAt
	}
	endedAt := operation.UpdatedAt
	if operation.FinishedAt > 0 {
		endedAt = operation.FinishedAt
	}
	elapsed := endedAt - startedAt
	if elapsed < 0 {
		elapsed = 0
	}
	return types.InferenceModelDeploymentEventResponse{
		DeploymentID: operation.ID, Status: operation.Status, Stage: operation.Stage,
		Sequence: operation.Sequence, Timestamp: operation.UpdatedAt,
		ElapsedMilliseconds: elapsed, Terminal: operation.Terminal(),
		ErrorCode: operation.ErrorCode, ErrorMessage: operation.ErrorMessage,
		RollbackError: operation.RollbackError,
	}
}

func modelDeploymentEventHTTPError(err error) (int, string, string) {
	switch err.Error() {
	case apiError.InvalidPayload:
		return http.StatusBadRequest, "Invalid model deployment event request.", apiError.InvalidPayload
	case apiError.MissingRecord:
		return http.StatusNotFound, "Inference model deployment was not found.", apiError.MissingRecord
	case apiError.InferenceUpgradeUnavailable:
		return http.StatusServiceUnavailable, "Model deployment events are unavailable.", apiError.InferenceUpgradeUnavailable
	default:
		return http.StatusInternalServerError, "Could not retrieve inference model deployment events.", apiError.ServerError
	}
}

func writeModelDeploymentEventError(w http.ResponseWriter, status int, message, errorCode string) {
	response := viewmodels.HTTPResponseVM{
		Status: status, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}
