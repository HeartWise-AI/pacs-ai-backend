package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/infrastructures/providers/api/dockerinference"
	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

const streamTestToken = "gateway-secret"

// streamTestModel stands in for a model container behind the real HTTP client.
type streamTestModel struct {
	server *httptest.Server
	mu     sync.Mutex
	// handle, when set, replaces the default behaviour of reading the body.
	handle        func(http.ResponseWriter, *http.Request)
	requests      int
	body          []byte
	keepBody      bool
	received      int64
	checksum      uint32
	contentLength int64
	chunked       bool
	completed     bool
}

func newStreamTestModel(t testing.TB) *streamTestModel {
	model := &streamTestModel{keepBody: true}
	model.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model.mu.Lock()
		model.requests++
		handle := model.handle
		model.contentLength, model.chunked = r.ContentLength, len(r.TransferEncoding) > 0
		model.mu.Unlock()
		if handle != nil {
			handle(w, r)
			return
		}
		hash := crc32.NewIEEE()
		var kept bytes.Buffer
		var sink io.Writer = hash
		if model.keepBody {
			sink = io.MultiWriter(hash, &kept)
		}
		count, err := io.CopyBuffer(sink, r.Body, make([]byte, 1<<20))
		model.mu.Lock()
		model.received, model.checksum, model.body, model.completed = count, hash.Sum32(), kept.Bytes(), err == nil
		model.mu.Unlock()
		if err != nil {
			return
		}
		_ = json.NewEncoder(w).Encode(dockerInferenceTypes.PredictResponse{
			Success: true, Data: map[string]interface{}{"ef": float64(55)},
		})
	}))
	t.Cleanup(model.server.Close)
	return model
}

func (model *streamTestModel) host() string { return strings.TrimPrefix(model.server.URL, "http://") }

// streamTestService forwards the prepared request with the production client,
// in place of the registry lookup and the model manager.
type streamTestService struct {
	application.InferenceCommandServiceInterface
	host    string
	before  func() error
	mu      sync.Mutex
	calls   int
	request dockerInferenceTypes.PredictRequest
	tenant  string
	ref     string
}

func (service *streamTestService) PredictPreparedInferenceModel(
	ctx context.Context, tenantID, containerRef string, request dockerInferenceTypes.PredictRequest,
) (dockerInferenceTypes.PredictResponse, error) {
	service.mu.Lock()
	service.calls++
	service.request, service.tenant, service.ref = request, tenantID, containerRef
	service.mu.Unlock()
	if service.before != nil {
		if err := service.before(); err != nil {
			return dockerInferenceTypes.PredictResponse{}, err
		}
	}
	return (&dockerinference.DockerInferenceAPI{}).Predict(ctx, service.host, request)
}

func newStreamTestGateway(t testing.TB, service application.InferenceCommandServiceInterface) *httptest.Server {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", streamTestToken)
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	gateway := httptest.NewServer(http.HandlerFunc(controller.PredictPreparedInferenceModel))
	t.Cleanup(gateway.Close)
	return gateway
}

func postStreamTest(t testing.TB, url string, body io.Reader, length int64) (*http.Response, map[string]interface{}) {
	request, err := http.NewRequest(http.MethodPost, url, body)
	require.NoError(t, err)
	request.ContentLength = length
	request.Header.Set("Authorization", "Bearer "+streamTestToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultTransport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var decoded map[string]interface{}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&decoded))
	return response, decoded
}

// streamTestValue is a request object of exactly size bytes.
func streamTestValue(size int) []byte {
	prefix, suffix := `{"outputMode":"JSON","seriesInstanceImages":{"0":{"0":"`, `"}}}`
	value := make([]byte, 0, size)
	value = append(value, prefix...)
	for len(value) < size-len(suffix) {
		value = append(value, "QUJDREVGR0g}{"[len(value)%11])
	}
	return append(value, suffix...)
}

func TestManagedPredictStreamForwardsExactlyTheRequestValue(t *testing.T) {
	heads := map[string]string{
		"study-service layout": `{"tenantId":"tenant-a","containerRef":"echo-prime","request":`,
		"whitespace":           "{ \"tenantId\" : \"tenant-a\" ,\r\n\t\"containerRef\":\"echo-prime\", \"request\" :\n  ",
		"container first":      `{"containerRef":"echo-prime","tenantId":"tenant-a","request":`,
	}
	sizes := []int{managedFastPathMinBytes, managedFastPathMinBytes + 1, managedFastPathMinBytes + 4095, 20<<20 + 7}
	for name, head := range heads {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%d", name, size), func(t *testing.T) {
				model := newStreamTestModel(t)
				service := &streamTestService{host: model.host()}
				gateway := newStreamTestGateway(t, service)
				value := streamTestValue(size)
				body := append(append([]byte(head), value...), '}')

				response, decoded := postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))

				require.Equal(t, http.StatusOK, response.StatusCode)
				require.Equal(t, true, decoded["success"])
				require.Equal(t, float64(55), decoded["data"].(map[string]interface{})["ef"])
				require.Equal(t, "tenant-a", service.tenant)
				require.Equal(t, "echo-prime", service.ref)
				require.NotNil(t, service.request.Stream, "a large study-service body must be streamed")
				require.Empty(t, service.request.Raw)
				require.Equal(t, int64(len(value)), service.request.StreamLength)
				require.Equal(t, int64(len(value)), model.contentLength, "the model must get an exact Content-Length")
				require.False(t, model.chunked)
				require.True(t, bytes.Equal(value, model.body), "forwarded bytes differ from the request value")
			})
		}
	}
}

func TestManagedPredictFallsBackToTheBufferedPath(t *testing.T) {
	value := streamTestValue(managedFastPathMinBytes)
	small := streamTestValue(1024)
	envelope := func(head string, value []byte, tail string) []byte {
		return append(append([]byte(head), value...), tail...)
	}
	cases := map[string]struct {
		body       []byte
		chunked    bool
		streaming  string
		wantStatus int
	}{
		"small body":          {body: envelope(`{"tenantId":"t","containerRef":"c","request":`, small, `}`), wantStatus: 200},
		"unknown length":      {body: envelope(`{"tenantId":"t","containerRef":"c","request":`, value, `}`), chunked: true, wantStatus: 200},
		"streaming off":       {body: envelope(`{"tenantId":"t","containerRef":"c","request":`, value, `}`), streaming: "false", wantStatus: 200},
		"request first":       {body: envelope(`{"request":`, value, `,"tenantId":"t","containerRef":"c"}`), wantStatus: 200},
		"unknown key":         {body: envelope(`{"tenantId":"t","note":1,"containerRef":"c","request":`, value, `}`), wantStatus: 200},
		"head beyond peek":    {body: envelope(`{"tenantId":"t",`+strings.Repeat(" ", managedHeadPeekBytes)+`"containerRef":"c","request":`, value, `}`), wantStatus: 200},
		"missing tenant":      {body: envelope(`{"containerRef":"c","request":`, value, `}`), wantStatus: 400},
		"request not object":  {body: envelope(`{"tenantId":"t","containerRef":"c","request":[`, value, `]}`), wantStatus: 400},
		"not json":            {body: bytes.Repeat([]byte("x"), managedFastPathMinBytes), wantStatus: 400},
		"small with trailing": {body: envelope(`{"tenantId":"t","containerRef":"c","request":`, small, " }\n"), wantStatus: 200},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if test.streaming != "" {
				t.Setenv("INTERNAL_INFERENCE_PREDICT_STREAMING", test.streaming)
			}
			model := newStreamTestModel(t)
			service := &streamTestService{host: model.host()}
			gateway := newStreamTestGateway(t, service)
			var body io.Reader = bytes.NewReader(test.body)
			length := int64(len(test.body))
			if test.chunked {
				body, length = io.MultiReader(body), -1
			}

			response, _ := postStreamTest(t, gateway.URL, body, length)

			require.Equal(t, test.wantStatus, response.StatusCode)
			if test.wantStatus != http.StatusOK {
				require.Zero(t, service.calls)
				return
			}
			require.Nil(t, service.request.Stream, "this body must take the buffered path")
			want := value
			if strings.HasPrefix(name, "small") {
				want = small
			}
			require.True(t, bytes.Equal(want, bytes.TrimSpace(service.request.Raw)))
			require.True(t, bytes.Equal(want, bytes.TrimSpace(model.body)))
		})
	}
}

func TestManagedPredictStreamingSetting(t *testing.T) {
	for value, want := range map[string]bool{"": true, "true": true, "TRUE": true, "1": true, "nonsense": true, "false": false, " False ": false, "0": false, "off": false, "no": false} {
		t.Setenv("INTERNAL_INFERENCE_PREDICT_STREAMING", value)
		require.Equal(t, want, managedPredictStreamingEnabled(), "value %q", value)
	}
}

func TestManagedPredictRejectsADeclaredOversizedBodyBeforeReadingIt(t *testing.T) {
	t.Setenv("INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES", fmt.Sprint(9<<20))
	model := newStreamTestModel(t)
	service := &streamTestService{host: model.host()}
	gateway := newStreamTestGateway(t, service)
	value := streamTestValue(10 << 20)
	body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')
	var read atomic.Int64

	response, decoded := postStreamTest(t, gateway.URL, countingReader{bytes.NewReader(body), &read}, int64(len(body)))

	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	require.Equal(t, "REQUEST_BODY_TOO_LARGE", decoded["errorCode"])
	require.Equal(t, "Request body is too large.", decoded["message"])
	require.Zero(t, service.calls)
	require.Zero(t, model.requests)

	// At the limit the body is streamed.
	t.Setenv("INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES", fmt.Sprint(len(body)))
	response, _ = postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NotNil(t, service.request.Stream)
}

type countingReader struct {
	io.Reader
	count *atomic.Int64
}

func (reader countingReader) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	reader.count.Add(int64(count))
	return count, err
}

func TestManagedPredictStreamFailsCleanlyOnAnUnexpectedTail(t *testing.T) {
	for name, tail := range map[string]string{
		"trailing newline":   "}\n",
		"space before brace": " }",
		"key after request":  `,"note":1}`,
		"missing brace":      `"`,
	} {
		t.Run(name, func(t *testing.T) {
			model := newStreamTestModel(t)
			service := &streamTestService{host: model.host()}
			gateway := newStreamTestGateway(t, service)
			value := streamTestValue(managedFastPathMinBytes)
			body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), tail...)

			response, decoded := postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))

			require.Equal(t, http.StatusBadRequest, response.StatusCode)
			require.Equal(t, "INVALID_REQUEST_PAYLOAD", decoded["errorCode"])
			require.NotNil(t, service.request.Stream)
			model.server.CloseClientConnections()
			require.Eventually(t, func() bool {
				model.mu.Lock()
				defer model.mu.Unlock()
				return model.requests == 1 && !model.completed && model.received < model.contentLength
			}, 5*time.Second, 10*time.Millisecond, "the model must never receive a complete body")
		})
	}
}

func TestManagedPredictStreamAnswersAModelLostWhileReceivingAsTransient(t *testing.T) {
	model := newStreamTestModel(t)
	model.handle = func(w http.ResponseWriter, r *http.Request) {
		// The inference process dies after the first megabyte.
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.(*net.TCPConn).SetLinger(0)
			_ = connection.Close()
		}
	}
	service := &streamTestService{host: model.host()}
	gateway := newStreamTestGateway(t, service)
	value := streamTestValue(64 << 20)
	body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')

	// The client still gets the answer although most of its body was not forwarded.
	response, decoded := postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))

	require.Equal(t, http.StatusBadGateway, response.StatusCode)
	require.Equal(t, "DOCKER_INFERENCE_ERROR", decoded["errorCode"])
	require.Equal(t, 1, model.requests, "a streamed prediction is never replayed")
}

func TestManagedPredictStreamAnswersAProxyErrorDuringTheUploadAsTransient(t *testing.T) {
	model := newStreamTestModel(t)
	model.handle = func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusBadGateway)
	}
	service := &streamTestService{host: model.host()}
	gateway := newStreamTestGateway(t, service)
	value := streamTestValue(64 << 20)
	body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')

	response, _ := postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))

	require.Equal(t, http.StatusBadGateway, response.StatusCode)
	require.Equal(t, 1, model.requests)
}

func TestManagedPredictStreamKeepsTheErrorClassOfAModelThatReadTheWholeRequest(t *testing.T) {
	model := newStreamTestModel(t)
	model.handle = func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}
	service := &streamTestService{host: model.host()}
	gateway := newStreamTestGateway(t, service)
	value := streamTestValue(managedFastPathMinBytes)
	body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')

	response, decoded := postStreamTest(t, gateway.URL, bytes.NewReader(body), int64(len(body)))

	require.Equal(t, http.StatusInternalServerError, response.StatusCode)
	require.Equal(t, "DOCKER_INFERENCE_ERROR", decoded["errorCode"])
}

func TestManagedPredictStreamAnswersErrorsRaisedBeforeTheFirstByteOverTheWholeUpload(t *testing.T) {
	cases := map[string]struct {
		err        error
		wantStatus int
		retryAfter string
	}{
		"admission timeout": {err: &modelmanager.AdmissionTimeout{}, wantStatus: 503, retryAfter: "1"},
		"model not ready":   {err: &modelmanager.ModelNotReadyError{}, wantStatus: 502},
		"not registered":    {err: fmt.Errorf("MISSING_RECORD"), wantStatus: 404},
		"other":             {err: fmt.Errorf("model runtime is not idle"), wantStatus: 500},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			model := newStreamTestModel(t)
			service := &streamTestService{host: model.host(), before: func() error { return test.err }}
			gateway := newStreamTestGateway(t, service)
			value := streamTestValue(64 << 20)
			body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')
			var read atomic.Int64

			response, _ := postStreamTest(t, gateway.URL, countingReader{bytes.NewReader(body), &read}, int64(len(body)))

			require.Equal(t, test.wantStatus, response.StatusCode)
			require.Equal(t, test.retryAfter, response.Header.Get("Retry-After"))
			require.Equal(t, int64(len(body)), read.Load(), "the upload must be read to its end before answering")
			require.Zero(t, model.requests)
		})
	}
}

// The route's read deadline, not the server-wide one, bounds an upload that
// waits for admission and then arrives slowly.
func TestManagedPredictStreamOutlivesTheServerReadTimeout(t *testing.T) {
	const serverReadTimeout = 300 * time.Millisecond
	value := streamTestValue(managedFastPathMinBytes)
	body := append(append([]byte(`{"tenantId":"t","containerRef":"c","request":`), value...), '}')
	run := func(t *testing.T, routeDeadline bool) (int, error) {
		t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", streamTestToken)
		model := newStreamTestModel(t)
		service := &streamTestService{host: model.host(), before: func() error {
			time.Sleep(2 * serverReadTimeout) // admission and model load
			return nil
		}}
		controller := InferenceCommandController{InferenceCommandServiceInterface: service}
		var handler http.Handler = http.HandlerFunc(controller.PredictPreparedInferenceModel)
		handler = requestbody.LimitWithScope(1<<30, "internal_inference")(handler)
		if routeDeadline {
			handler = requestbody.WithReadDeadline(time.Minute)(handler)
		}
		gateway := httptest.NewUnstartedServer(handler)
		gateway.Config.ReadTimeout = serverReadTimeout
		gateway.Start()
		defer gateway.Close()
		// The second half of the body is sent well after the server-wide timeout.
		half := len(body) / 2
		slow := io.MultiReader(bytes.NewReader(body[:half]), &delayedReader{delay: 4 * serverReadTimeout, reader: bytes.NewReader(body[half:])})
		request, err := http.NewRequest(http.MethodPost, gateway.URL, slow)
		require.NoError(t, err)
		request.ContentLength = int64(len(body))
		request.Header.Set("Authorization", "Bearer "+streamTestToken)
		response, err := (&http.Transport{}).RoundTrip(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}

	status, err := run(t, true)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	status, err = run(t, false)
	require.True(t, err != nil || status != http.StatusOK, "without the route deadline the server-wide timeout cuts the upload")
}

type delayedReader struct {
	delay  time.Duration
	once   sync.Once
	reader io.Reader
}

func (reader *delayedReader) Read(buffer []byte) (int, error) {
	reader.once.Do(func() { time.Sleep(reader.delay) })
	return reader.reader.Read(buffer)
}

// streamTestEnvelope generates an envelope whose request value is valueLength
// bytes, without holding it in memory, and the checksum of that value.
func streamTestEnvelope(valueLength int64) (io.Reader, int64, func() uint32) {
	head := `{"tenantId":"tenant-a","containerRef":"echo-prime","request":`
	prefix, suffix := `{"outputMode":"JSON","seriesInstanceImages":{"0":{"0":"`, `"}}}`
	hash := crc32.NewIEEE()
	value := io.TeeReader(io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(patternReader{}, valueLength-int64(len(prefix)+len(suffix))),
		strings.NewReader(suffix),
	), hash)
	return io.MultiReader(strings.NewReader(head), value, strings.NewReader("}")), int64(len(head)) + valueLength + 1, hash.Sum32
}

type patternReader struct{}

var patternBlock = bytes.Repeat([]byte("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="), 4096)

func (patternReader) Read(buffer []byte) (int, error) {
	count := 0
	for count < len(buffer) {
		count += copy(buffer[count:], patternBlock)
	}
	return count, nil
}

func heapInUse() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapInuse
}

// A body far larger than the allowed heap growth passes through the gateway:
// client, gateway and model all run in this process, and none may hold it.
func TestManagedPredictStreamDoesNotHoldTheBodyInMemory(t *testing.T) {
	const valueLength = 512 << 20
	const allowedHeapGrowth = 48 << 20
	t.Setenv("INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES", fmt.Sprint(int64(1<<30)))
	model := newStreamTestModel(t)
	model.keepBody = false
	service := &streamTestService{host: model.host()}
	gateway := newStreamTestGateway(t, service)
	body, length, checksum := streamTestEnvelope(valueLength)

	runtime.GC()
	baseline := heapInUse()
	var peak atomic.Uint64
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if now := heapInUse(); now > peak.Load() {
					peak.Store(now)
				}
			}
		}
	}()
	started := time.Now()
	response, _ := postStreamTest(t, gateway.URL, body, length)
	elapsed := time.Since(started)
	close(stop)
	sampler.Wait()

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, int64(valueLength), model.received)
	require.Equal(t, int64(valueLength), model.contentLength)
	require.Equal(t, checksum(), model.checksum, "forwarded bytes differ from the request value")
	growth := int64(peak.Load()) - int64(baseline)
	t.Logf("streamed %d MiB in %s (%.0f MiB/s, checksums included); peak heap growth %.1f MiB",
		valueLength>>20, elapsed.Round(time.Millisecond), float64(valueLength>>20)/elapsed.Seconds(), float64(growth)/(1<<20))
	require.Less(t, growth, int64(allowedHeapGrowth), "heap grew with the body")
}

// BenchmarkManagedPredictStream measures client → gateway → model throughput
// over loopback for a 256 MiB request value.
func BenchmarkManagedPredictStream(b *testing.B) {
	const valueLength = 256 << 20
	b.Setenv("INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES", fmt.Sprint(int64(1<<30)))
	model := newStreamTestModel(b)
	model.handle = func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyBuffer(io.Discard, r.Body, make([]byte, 1<<20))
		_ = json.NewEncoder(w).Encode(dockerInferenceTypes.PredictResponse{Success: true})
	}
	gateway := newStreamTestGateway(b, &streamTestService{host: model.host()})
	head := `{"tenantId":"tenant-a","containerRef":"echo-prime","request":{"d":"`
	length := int64(len(head)) + valueLength - 8 + 3
	b.SetBytes(valueLength)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := io.MultiReader(strings.NewReader(head), io.LimitReader(patternReader{}, valueLength-8), strings.NewReader(`"}}`))
		response, _ := postStreamTest(b, gateway.URL, body, length)
		if response.StatusCode != http.StatusOK {
			b.Fatalf("status %d", response.StatusCode)
		}
	}
}
