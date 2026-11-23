package rest

import (
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	inferenceService "api-pacs/module/inference/infrastructure/service"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	userEntity "api-pacs/module/user/domain/entity"
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

	viewer := controller.worklistNotificationViewer(r)
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
	inferenceService.ObserveWorklistSSEConnectionOpened()
	log.Printf("[Worklist SSE] event=connection_opened")
	defer func() {
		inferenceService.ObserveWorklistSSEConnectionClosed()
		log.Printf("[Worklist SSE] event=connection_closed")
	}()

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
			if err := writeWorklistServerSentEvent(w, notification, viewer); err != nil {
				inferenceService.ObserveWorklistNotification("sse_write_failed")
				return
			}
			inferenceService.ObserveWorklistNotification("sse_sent")
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

// worklistClientEvent is the browser contract of one worklist event: the
// committed status plus whether this connection's user should be notified.
// It never carries user ids or the tenant.
type worklistClientEvent struct {
	serviceTypes.WorklistNotification
	// Notify tells the client to raise a bell entry / toast. Status updates
	// are applied whatever its value.
	Notify bool `json:"notify"`
	// CancelledByYou is true when this connection's user cancelled the run.
	CancelledByYou bool `json:"cancelledByYou"`
}

func writeWorklistServerSentEvent(
	w http.ResponseWriter,
	notification serviceTypes.WorklistNotification,
	viewer serviceTypes.WorklistNotificationViewer,
) error {
	payload, err := json.Marshal(worklistClientEvent{
		WorklistNotification: notification,
		Notify:               notification.ShouldNotify(viewer),
		CancelledByYou:       notification.CancelledByViewer(viewer),
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", notification.Type, payload)
	return err
}

// WorklistUserMetadataReader reads a user's free-form metadata, where the
// notifyAutomaticRuns preference lives.
type WorklistUserMetadataReader interface {
	GetUserMetadata(ctx context.Context, userID string) (userEntity.UserMetadata, error)
}

// worklistNotificationViewer captures who is connected. The personal mute is
// read once when the stream opens: a change applies server-side on the next
// reconnect (the viewer also applies it immediately on its side).
func (controller *InferenceQueryController) worklistNotificationViewer(r *http.Request) serviceTypes.WorklistNotificationViewer {
	userID, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	role, _ := r.Context().Value(iamTypes.RoleCtx).(string)
	viewer := serviceTypes.WorklistNotificationViewer{
		UserID:  strings.TrimSpace(userID),
		IsAdmin: role == iamEntity.AdminRole || role == iamEntity.OwnerRole || accessctx.Has(r.Context(), accessEntity.PermProcessingManage),
	}
	if controller.UserMetadataReader != nil && viewer.UserID != "" {
		metadata, err := controller.UserMetadataReader.GetUserMetadata(r.Context(), viewer.UserID)
		if err == nil {
			viewer.AutomaticRunsMuted = automaticRunNotificationsMuted(metadata.Metadata)
		}
	}
	return viewer
}

// automaticRunNotificationsMuted is true only for an explicit
// "notifyAutomaticRuns": false; anything else keeps notifications on.
func automaticRunNotificationsMuted(rawMetadata string) bool {
	if strings.TrimSpace(rawMetadata) == "" {
		return false
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		return false
	}
	value, ok := metadata["notifyAutomaticRuns"]
	if !ok {
		return false
	}
	var enabled bool
	if err := json.Unmarshal(value, &enabled); err != nil {
		return false
	}
	return !enabled
}
