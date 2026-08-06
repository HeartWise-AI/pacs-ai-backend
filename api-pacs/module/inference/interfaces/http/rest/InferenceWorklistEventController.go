package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

const (
	defaultWorklistEventHeartbeatInterval = 20 * time.Second
	defaultWorklistEventBuffer            = 32
)

// StreamWorklistEvents keeps one tenant-scoped HTTP connection open and sends
// committed worklist changes as Server-Sent Events.
func (controller *InferenceQueryController) StreamWorklistEvents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeWorklistQueryError(w, apiError.UnauthorizedAccess)
		return
	}
	if controller.WorklistNotificationSubscriberInterface == nil {
		writeWorklistQueryError(w, apiError.HystrixTimeout)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is unsupported.", http.StatusInternalServerError)
		return
	}

	notifications, unsubscribe := controller.SubscribeWorklistNotifications(tenantID, defaultWorklistEventBuffer)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(controller.worklistEventHeartbeatInterval())
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case notification, open := <-notifications:
			if !open {
				return
			}
			if err := writeWorklistServerSentEvent(w, notification); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (controller *InferenceQueryController) worklistEventHeartbeatInterval() time.Duration {
	if controller.WorklistEventHeartbeatInterval > 0 {
		return controller.WorklistEventHeartbeatInterval
	}
	return defaultWorklistEventHeartbeatInterval
}

func writeWorklistServerSentEvent(w http.ResponseWriter, notification serviceTypes.WorklistNotification) error {
	payload, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", notification.Type, payload)
	return err
}
