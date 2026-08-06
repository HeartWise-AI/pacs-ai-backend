package rest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type worklistEventSubscriber struct {
	mutex         sync.Mutex
	tenantID      string
	buffer        int
	notifications chan serviceTypes.WorklistNotification
	subscribed    chan struct{}
	unsubscribed  chan struct{}
	closeOnce     sync.Once
}

type synchronizedResponseRecorder struct {
	mutex  sync.Mutex
	header http.Header
	body   bytes.Buffer
	code   int
}

func newSynchronizedResponseRecorder() *synchronizedResponseRecorder {
	return &synchronizedResponseRecorder{header: make(http.Header)}
}

func (recorder *synchronizedResponseRecorder) Header() http.Header {
	return recorder.header
}

func (recorder *synchronizedResponseRecorder) WriteHeader(code int) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.code == 0 {
		recorder.code = code
	}
}

func (recorder *synchronizedResponseRecorder) Write(payload []byte) (int, error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.code == 0 {
		recorder.code = http.StatusOK
	}
	return recorder.body.Write(payload)
}

func (recorder *synchronizedResponseRecorder) Flush() {}

func (recorder *synchronizedResponseRecorder) BodyString() string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.body.String()
}

func (recorder *synchronizedResponseRecorder) StatusCode() int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.code
}

func newWorklistEventSubscriber() *worklistEventSubscriber {
	return &worklistEventSubscriber{
		notifications: make(chan serviceTypes.WorklistNotification, 1),
		subscribed:    make(chan struct{}),
		unsubscribed:  make(chan struct{}),
	}
}

func (subscriber *worklistEventSubscriber) SubscribeWorklistNotifications(
	tenantID string,
	buffer int,
) (<-chan serviceTypes.WorklistNotification, func()) {
	subscriber.mutex.Lock()
	subscriber.tenantID = tenantID
	subscriber.buffer = buffer
	subscriber.mutex.Unlock()
	close(subscriber.subscribed)
	return subscriber.notifications, func() {
		subscriber.closeOnce.Do(func() { close(subscriber.unsubscribed) })
	}
}

func TestStreamWorklistEventsUsesAuthenticatedTenantAndWritesSSE(t *testing.T) {
	subscriber := newWorklistEventSubscriber()
	controller := InferenceQueryController{
		WorklistNotificationSubscriberInterface: subscriber,
		WorklistEventHeartbeatInterval:          time.Hour,
	}
	requestContext, cancel := context.WithCancel(context.WithValue(
		context.Background(), iamTypes.TenantIDCtx, "tenant-a",
	))
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/worklist/events?tenantId=tenant-b", nil).
		WithContext(requestContext)
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamWorklistEvents(recorder, request)
		close(done)
	}()

	<-subscriber.subscribed
	subscriber.notifications <- serviceTypes.WorklistNotification{
		Type:             serviceTypes.WorklistNotificationTypeStudyStatusUpdated,
		TenantID:         "tenant-a",
		StudyInstanceUID: "study-1",
		RunID:            "run-1",
		Version:          3,
	}
	require.Eventually(t, func() bool {
		return strings.Contains(recorder.BodyString(), `"version":3`)
	}, time.Second, time.Millisecond)
	cancel()
	<-done

	subscriber.mutex.Lock()
	require.Equal(t, "tenant-a", subscriber.tenantID)
	require.Equal(t, defaultWorklistEventBuffer, subscriber.buffer)
	subscriber.mutex.Unlock()
	require.Equal(t, http.StatusOK, recorder.StatusCode())
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	body := recorder.BodyString()
	require.Contains(t, body, ": connected\n\n")
	require.Contains(t, body, "event: study_status.updated\n")
	require.Contains(t, body, `"studyInstanceUID":"study-1"`)
	require.NotContains(t, body, "tenant-a")
	select {
	case <-subscriber.unsubscribed:
	default:
		t.Fatal("stream did not release its subscription")
	}
}

func TestStreamWorklistEventsSendsHeartbeat(t *testing.T) {
	subscriber := newWorklistEventSubscriber()
	controller := InferenceQueryController{
		WorklistNotificationSubscriberInterface: subscriber,
		WorklistEventHeartbeatInterval:          time.Millisecond,
	}
	requestContext, cancel := context.WithCancel(context.WithValue(
		context.Background(), iamTypes.TenantIDCtx, "tenant-a",
	))
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/worklist/events", nil).
		WithContext(requestContext)
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamWorklistEvents(recorder, request)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(recorder.BodyString(), ": heartbeat\n\n")
	}, time.Second, time.Millisecond)
	cancel()
	<-done
}

func TestStreamWorklistEventsRejectsMissingTenant(t *testing.T) {
	controller := InferenceQueryController{WorklistNotificationSubscriberInterface: newWorklistEventSubscriber()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/worklist/events", nil)

	controller.StreamWorklistEvents(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
