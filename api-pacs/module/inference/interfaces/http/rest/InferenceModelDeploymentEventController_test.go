package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

type modelDeploymentEventSubscriber struct {
	mutex        sync.Mutex
	tenantID     string
	modelID      string
	deploymentID string
	buffer       int
	events       chan entity.InferenceModelDeploymentOperation
	subscribed   chan struct{}
	unsubscribed chan struct{}
	closeOnce    sync.Once
}

func newModelDeploymentEventSubscriber() *modelDeploymentEventSubscriber {
	return &modelDeploymentEventSubscriber{
		events:     make(chan entity.InferenceModelDeploymentOperation, 8),
		subscribed: make(chan struct{}), unsubscribed: make(chan struct{}),
	}
}

func (subscriber *modelDeploymentEventSubscriber) SubscribeModelDeploymentOperation(
	tenantID, modelID, deploymentID string,
	buffer int,
) (<-chan entity.InferenceModelDeploymentOperation, func()) {
	subscriber.mutex.Lock()
	subscriber.tenantID = tenantID
	subscriber.modelID = modelID
	subscriber.deploymentID = deploymentID
	subscriber.buffer = buffer
	subscriber.mutex.Unlock()
	close(subscriber.subscribed)
	return subscriber.events, func() {
		subscriber.closeOnce.Do(func() { close(subscriber.unsubscribed) })
	}
}

func deploymentEventOperation(sequence int64, status entity.InferenceModelDeploymentOperationStatus) entity.InferenceModelDeploymentOperation {
	operation := entity.InferenceModelDeploymentOperation{
		ID: "operation-1", TenantID: "tenant-1", ModelID: "registration-1", ActorUserID: "private-user",
		Status: status, Stage: entity.InferenceModelDeploymentStagePull, Sequence: sequence,
		CreatedAt: 1000, StartedAt: 1100, UpdatedAt: 1200,
	}
	if operation.Terminal() {
		operation.FinishedAt = 1500
	}
	return operation
}

func TestStreamInferenceModelDeploymentEventsSendsScopedSnapshotAndTerminalUpdate(t *testing.T) {
	subscriber := newModelDeploymentEventSubscriber()
	service := &deploymentControllerService{operation: deploymentEventOperation(2, entity.InferenceModelDeploymentOperationRunning)}
	controller := InferenceCommandController{
		InferenceCommandServiceInterface:            service,
		ModelDeploymentOperationSubscriberInterface: subscriber,
		ModelDeploymentEventHeartbeatInterval:       time.Hour,
	}
	requestContext, cancel := context.WithCancel(deploymentControllerRequest(
		http.MethodGet,
		"/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	).Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/models/registration-1/deployments/operation-1/events", nil).
		WithContext(requestContext)
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamInferenceModelDeploymentEvents(recorder, request)
		close(done)
	}()

	<-subscriber.subscribed
	require.Eventually(t, func() bool { return strings.Contains(recorder.BodyString(), "id: 2\n") }, time.Second, time.Millisecond)
	terminal := deploymentEventOperation(3, entity.InferenceModelDeploymentOperationCompleted)
	subscriber.events <- terminal
	<-done

	subscriber.mutex.Lock()
	require.Equal(t, "tenant-1", subscriber.tenantID)
	require.Equal(t, "registration-1", subscriber.modelID)
	require.Equal(t, "operation-1", subscriber.deploymentID)
	require.Equal(t, defaultModelDeploymentEventBuffer, subscriber.buffer)
	subscriber.mutex.Unlock()
	require.Equal(t, http.StatusOK, recorder.StatusCode())
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	body := recorder.BodyString()
	require.Contains(t, body, "event: model-deployment\n")
	require.Contains(t, body, `"sequence":2`)
	require.Contains(t, body, `"sequence":3`)
	require.Contains(t, body, `"terminal":true`)
	require.NotContains(t, body, "tenant-1")
	require.NotContains(t, body, "private-user")
	select {
	case <-subscriber.unsubscribed:
	default:
		t.Fatal("stream did not release its subscription")
	}
}

func TestStreamInferenceModelDeploymentEventsResumesAfterLastEventID(t *testing.T) {
	subscriber := newModelDeploymentEventSubscriber()
	service := &deploymentControllerService{operation: deploymentEventOperation(4, entity.InferenceModelDeploymentOperationRunning)}
	controller := InferenceCommandController{
		InferenceCommandServiceInterface:            service,
		ModelDeploymentOperationSubscriberInterface: subscriber,
		ModelDeploymentEventHeartbeatInterval:       time.Hour,
	}
	request := deploymentControllerRequest(
		http.MethodGet,
		"/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	)
	request.Header.Set("Last-Event-ID", "4")
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamInferenceModelDeploymentEvents(recorder, request)
		close(done)
	}()

	<-subscriber.subscribed
	require.Eventually(t, func() bool { return strings.Contains(recorder.BodyString(), "id: 4\n") }, time.Second, time.Millisecond)
	subscriber.events <- deploymentEventOperation(3, entity.InferenceModelDeploymentOperationRunning)
	subscriber.events <- deploymentEventOperation(5, entity.InferenceModelDeploymentOperationCompleted)
	<-done
	body := recorder.BodyString()
	require.NotContains(t, body, "id: 3\n")
	require.Contains(t, body, "id: 4\n")
	require.Contains(t, body, "id: 5\n")
}

func TestStreamInferenceModelDeploymentEventsSendsHeartbeat(t *testing.T) {
	subscriber := newModelDeploymentEventSubscriber()
	controller := InferenceCommandController{
		InferenceCommandServiceInterface: &deploymentControllerService{
			operation: deploymentEventOperation(1, entity.InferenceModelDeploymentOperationRunning),
		},
		ModelDeploymentOperationSubscriberInterface: subscriber,
		ModelDeploymentEventHeartbeatInterval:       time.Millisecond,
	}
	request := deploymentControllerRequest(
		http.MethodGet, "/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	)
	requestContext, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestContext)
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamInferenceModelDeploymentEvents(recorder, request)
		close(done)
	}()
	require.Eventually(t, func() bool { return strings.Contains(recorder.BodyString(), ": heartbeat\n\n") }, time.Second, time.Millisecond)
	cancel()
	<-done
}

func TestStreamInferenceModelDeploymentEventsRejectsMissingTenant(t *testing.T) {
	controller := InferenceCommandController{
		InferenceCommandServiceInterface:            &deploymentControllerService{},
		ModelDeploymentOperationSubscriberInterface: newModelDeploymentEventSubscriber(),
	}
	request := deploymentControllerRequest(
		http.MethodGet, "/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	)
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, ""))
	recorder := httptest.NewRecorder()

	controller.StreamInferenceModelDeploymentEvents(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestStreamInferenceModelDeploymentEventsReturnsScopedNotFound(t *testing.T) {
	controller := InferenceCommandController{
		InferenceCommandServiceInterface:            &deploymentControllerService{err: contextError(apiError.MissingRecord)},
		ModelDeploymentOperationSubscriberInterface: newModelDeploymentEventSubscriber(),
	}
	request := deploymentControllerRequest(
		http.MethodGet, "/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	)
	recorder := httptest.NewRecorder()

	controller.StreamInferenceModelDeploymentEvents(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestStreamInferenceModelDeploymentEventsSanitizesLookupFailure(t *testing.T) {
	controller := InferenceCommandController{
		InferenceCommandServiceInterface:            &deploymentControllerService{err: contextError("firestore secret detail")},
		ModelDeploymentOperationSubscriberInterface: newModelDeploymentEventSubscriber(),
	}
	request := deploymentControllerRequest(
		http.MethodGet, "/models/registration-1/deployments/operation-1/events",
		map[string]string{"ID": "registration-1", "deploymentID": "operation-1"},
	)
	recorder := httptest.NewRecorder()

	controller.StreamInferenceModelDeploymentEvents(recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret detail")
	require.Contains(t, recorder.Body.String(), apiError.ServerError)
}

type contextError string

func (err contextError) Error() string { return string(err) }
