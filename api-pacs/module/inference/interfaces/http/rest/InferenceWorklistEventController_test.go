package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	userEntity "api-pacs/module/user/domain/entity"
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

type worklistUserMetadataStub struct {
	metadata map[string]string
	err      error
}

func (stub worklistUserMetadataStub) GetUserMetadata(_ context.Context, userID string) (userEntity.UserMetadata, error) {
	if stub.err != nil {
		return userEntity.UserMetadata{}, stub.err
	}
	return userEntity.UserMetadata{UserID: userID, Metadata: stub.metadata[userID]}, nil
}

// streamOneWorklistEvent opens a stream as the given user and returns the
// SSE data line written for one notification.
func streamOneWorklistEvent(
	t *testing.T,
	controller InferenceQueryController,
	userID, role string,
	notification serviceTypes.WorklistNotification,
) map[string]interface{} {
	t.Helper()
	subscriber := newWorklistEventSubscriber()
	controller.WorklistNotificationSubscriberInterface = subscriber
	controller.WorklistEventHeartbeatInterval = time.Hour
	requestContext := context.WithValue(context.Background(), iamTypes.TenantIDCtx, "tenant-a")
	requestContext = context.WithValue(requestContext, iamTypes.UserIDCtx, userID)
	requestContext = context.WithValue(requestContext, iamTypes.RoleCtx, role)
	requestContext, cancel := context.WithCancel(requestContext)
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/worklist/events", nil).WithContext(requestContext)
	recorder := newSynchronizedResponseRecorder()
	done := make(chan struct{})
	go func() {
		controller.StreamWorklistEvents(recorder, request)
		close(done)
	}()
	<-subscriber.subscribed
	subscriber.notifications <- notification
	require.Eventually(t, func() bool {
		return strings.Contains(recorder.BodyString(), "data: ")
	}, time.Second, time.Millisecond)
	cancel()
	<-done

	body := recorder.BodyString()
	for _, secret := range []string{"tenant-a", "user-owner", "user-requester", "user-canceller", userID} {
		if secret != "" {
			require.NotContains(t, body, secret)
		}
	}
	line := body[strings.Index(body, "data: ")+len("data: "):]
	line = line[:strings.Index(line, "\n")]
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(line), &payload))
	return payload
}

func TestStreamWorklistEventsComputesNotifyPerConnection(t *testing.T) {
	automatic := serviceTypes.WorklistNotification{
		Type: serviceTypes.WorklistNotificationTypeStudyStatusUpdated, TenantID: "tenant-a",
		StudyInstanceUID: "study-1", RunID: "run-1", Version: 2, Trigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		Audience: serviceTypes.WorklistNotificationAudience{
			Resolved: true, AnyJobNotificationsEnabled: true, NotifyJobOwnerUserIDs: []string{"user-owner"},
		},
	}
	viewerRun := serviceTypes.WorklistNotification{
		Type: serviceTypes.WorklistNotificationTypeStudyStatusUpdated, TenantID: "tenant-a",
		StudyInstanceUID: "study-1", RunID: "run-2", Version: 3, Trigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest,
		Audience: serviceTypes.WorklistNotificationAudience{
			Resolved: true, RequestedByUserID: "user-requester", CancelledByUserID: "user-requester",
		},
	}
	controller := InferenceQueryController{UserMetadataReader: worklistUserMetadataStub{metadata: map[string]string{
		"user-muted-admin": `{"notifyAutomaticRuns":false,"tutorialProgressStep":3}`,
		"user-owner":       `{"notifyAutomaticRuns":true}`,
	}}}

	cases := []struct {
		name           string
		userID, role   string
		notification   serviceTypes.WorklistNotification
		notify         bool
		cancelledByYou bool
	}{
		{"job owner with role USER", "user-owner", "USER", automatic, true, false},
		{"plain USER", "user-plain", "USER", automatic, false, false},
		{"ADMIN", "user-admin", "ADMIN", automatic, true, false},
		{"OWNER", "user-workspace-owner", "OWNER", automatic, true, false},
		{"ADMIN who muted automatic runs", "user-muted-admin", "ADMIN", automatic, false, false},
		{"requester of a viewer run", "user-requester", "USER", viewerRun, true, true},
		{"ADMIN who did not request a viewer run", "user-admin", "ADMIN", viewerRun, false, false},
	}
	for _, tc := range cases {
		payload := streamOneWorklistEvent(t, controller, tc.userID, tc.role, tc.notification)
		require.Equal(t, tc.notify, payload["notify"], tc.name)
		require.Equal(t, tc.cancelledByYou, payload["cancelledByYou"], tc.name)
		// The status itself always reaches the client.
		require.Equal(t, "study-1", payload["studyInstanceUID"], tc.name)
		require.Equal(t, tc.notification.RunID, payload["runId"], tc.name)
		require.NotContains(t, payload, "audience", tc.name)
	}
}

func TestStreamWorklistEventsKeepsNotificationsWhenPreferenceIsUnreadable(t *testing.T) {
	automatic := serviceTypes.WorklistNotification{
		Type: serviceTypes.WorklistNotificationTypeStudyStatusUpdated, TenantID: "tenant-a",
		StudyInstanceUID: "study-1", RunID: "run-1", Trigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		Audience: serviceTypes.WorklistNotificationAudience{Resolved: true, AnyJobNotificationsEnabled: true},
	}
	controller := InferenceQueryController{UserMetadataReader: worklistUserMetadataStub{err: errors.New("MISSING_RECORD")}}
	payload := streamOneWorklistEvent(t, controller, "user-admin", "ADMIN", automatic)
	require.Equal(t, true, payload["notify"])
}

func TestAutomaticRunNotificationsMuted(t *testing.T) {
	require.True(t, automaticRunNotificationsMuted(`{"notifyAutomaticRuns":false}`))
	require.False(t, automaticRunNotificationsMuted(`{"notifyAutomaticRuns":true}`))
	require.False(t, automaticRunNotificationsMuted(`{"notifyAutomaticRuns":"false"}`))
	require.False(t, automaticRunNotificationsMuted(`{"tutorialProgressStep":2}`))
	require.False(t, automaticRunNotificationsMuted(``))
	require.False(t, automaticRunNotificationsMuted(`not json`))
}
