package requestbody

import (
	"encoding/json"
	"expvar"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
)

func TestLimitRejectsDeclaredOversizedBodyBeforeHandler(t *testing.T) {
	metricKey := "scope=api,reason=declared_length"
	metricBefore := expvarMapValue(requestBodyRejectionsTotal, metricKey)
	called := false
	handler := Limit(8)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader("123456789"))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.False(t, called)
	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Equal(t, apiError.RequestBodyTooLarge, responseErrorCode(t, recorder))
	require.Equal(t, metricBefore+1, expvarMapValue(requestBodyRejectionsTotal, metricKey))
}

type readDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (recorder *readDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	recorder.deadline = deadline
	return nil
}

func TestWithReadDeadlineExtendsDeadlineForClinicalUpload(t *testing.T) {
	called := false
	timeout := 2 * time.Hour
	handler := WithReadDeadline(timeout)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	recorder := &readDeadlineRecorder{ResponseRecorder: httptest.NewRecorder(), deadline: time.Now()}
	request := httptest.NewRequest(http.MethodPost, "/proxy/orthanc/dicom-web/studies", http.NoBody)
	before := time.Now()

	handler.ServeHTTP(recorder, request)
	after := time.Now()

	require.True(t, called)
	require.False(t, recorder.deadline.IsZero())
	require.False(t, recorder.deadline.Before(before.Add(timeout)))
	require.False(t, recorder.deadline.After(after.Add(timeout)))
}

func TestWithReadDeadlineUsesSafeFallback(t *testing.T) {
	handler := WithReadDeadline(0)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	recorder := &readDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	request := httptest.NewRequest(http.MethodPost, "/proxy/orthanc/dicom-web/studies", http.NoBody)
	before := time.Now()

	handler.ServeHTTP(recorder, request)

	require.False(t, recorder.deadline.Before(before.Add(DefaultDICOMWebReadTimeout)))
}

func TestLimitStateSupportsConcurrentAccess(t *testing.T) {
	state := &limitState{}
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)

	go func() {
		defer workers.Done()
		<-start
		for index := 0; index < 1000; index++ {
			state.exceeded.Store(true)
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for index := 0; index < 1000; index++ {
			_ = state.exceeded.Load()
		}
	}()

	close(start)
	workers.Wait()
	require.True(t, state.exceeded.Load())
}

func TestLimitMapsChunkedReadOverflowToPayloadTooLarge(t *testing.T) {
	handler := Limit(8)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"invalid"}`))
		}
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"value":"too large"}`))
	request.ContentLength = -1
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Equal(t, apiError.RequestBodyTooLarge, responseErrorCode(t, recorder))
}

func TestLimitAllowsBodyAtBoundary(t *testing.T) {
	called := false
	handler := Limit(8)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		var payload map[string]interface{}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"a":1}`))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestConfiguredMaxBytesUsesSafeFallback(t *testing.T) {
	require.Equal(t, int64(6*1024*1024*1024), DefaultDICOMWebMaxBytes)
	t.Setenv("TEST_MAX_BYTES", "invalid")
	require.Equal(t, int64(123), PositiveInt64FromEnvironment("TEST_MAX_BYTES", 123))
	t.Setenv("TEST_MAX_BYTES", "456")
	require.Equal(t, int64(456), PositiveInt64FromEnvironment("TEST_MAX_BYTES", 123))
}

func TestIsTooLargeRecognizesNativeMaxBytesError(t *testing.T) {
	require.True(t, IsTooLarge(&http.MaxBytesError{Limit: 8}))
}

func responseErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response.ErrorCode
}

func expvarMapValue(metric *expvar.Map, key string) int64 {
	value := metric.Get(key)
	if value == nil {
		return 0
	}
	counter, ok := value.(*expvar.Int)
	if !ok {
		return 0
	}
	return counter.Value()
}
