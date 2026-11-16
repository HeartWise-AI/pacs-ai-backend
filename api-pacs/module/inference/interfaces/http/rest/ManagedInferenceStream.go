package rest

import (
	"bufio"
	"bytes"
	"errors"
	"expvar"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

// managedHeadPeekBytes bounds what the gateway reads before it knows where a
// streamed request value starts: tenantId, containerRef and the "request" key.
const managedHeadPeekBytes = 4096

var (
	errManagedStreamTail     = errors.New("request envelope does not end with the request object")
	errManagedStreamDetached = errors.New("request stream is no longer available")

	managedPredictStreamsTotal = expvar.NewMap("internal_inference_predict_streams_total")
)

// managedPredictStreamingEnabled reads INTERNAL_INFERENCE_PREDICT_STREAMING.
// Streaming is on unless the setting is an explicit negative.
func managedPredictStreamingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INTERNAL_INFERENCE_PREDICT_STREAMING"))) {
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

// managedPredictStream is the request value of an envelope
// `{…,"request":<value>}` read from the client connection as the model reads
// it. The envelope must end with the value's closing brace directly followed
// by its own, which is how study-service writes it; the value's last byte is
// held back until that ending has been read and checked, so a model never
// receives a complete body cut out of an envelope laid out differently.
type managedPredictStream struct {
	mu        sync.Mutex
	source    io.Reader
	remaining int64 // bytes of the value not yet handed to the model
	detached  bool
	sourceErr error
	forwarded int64
	firstRead time.Time
	lastRead  time.Time
}

func (stream *managedPredictStream) Read(buffer []byte) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.detached {
		return 0, errManagedStreamDetached
	}
	if stream.sourceErr != nil {
		return 0, stream.sourceErr
	}
	if stream.remaining == 0 {
		return 0, io.EOF
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if stream.firstRead.IsZero() {
		stream.firstRead = time.Now()
	}
	if stream.remaining == 1 {
		var tail [2]byte
		if _, err := io.ReadFull(stream.source, tail[:]); err != nil {
			return 0, stream.fail(err)
		}
		if tail != [2]byte{'}', '}'} {
			return 0, stream.fail(errManagedStreamTail)
		}
		buffer[0] = '}'
		stream.remaining, stream.forwarded, stream.lastRead = 0, stream.forwarded+1, time.Now()
		return 1, nil
	}
	if limit := stream.remaining - 1; int64(len(buffer)) > limit {
		buffer = buffer[:limit]
	}
	count, err := stream.source.Read(buffer)
	stream.remaining -= int64(count)
	stream.forwarded += int64(count)
	if err != nil {
		// The value is not complete yet, so the end of the body is an error too.
		return count, stream.fail(err)
	}
	return count, nil
}

func (stream *managedPredictStream) fail(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	stream.sourceErr = err
	return err
}

// Close is called by the HTTP transport when it is done with the body.
func (stream *managedPredictStream) Close() error { return nil }

// detach takes the client body back from the HTTP transport, which may still
// hold the stream after a failed exchange: it waits for a read in progress and
// refuses later ones.
func (stream *managedPredictStream) detach() {
	stream.mu.Lock()
	stream.detached = true
	stream.mu.Unlock()
}

// predictPreparedStream serves a large prepared request without holding it in
// memory. It returns false, having consumed nothing from head, when the body
// does not start the way study-service writes it.
//
// The model manager admits the request, loads the model if needed and confirms
// it is ready while the client is already sending; the client simply blocks on
// a full TCP window until the model starts reading. Nothing is retried once
// the first byte has left: a model that fails while receiving is answered as
// 502, which study-service treats as transient and retries as a whole job.
func (controller *InferenceCommandController) predictPreparedStream(
	w http.ResponseWriter,
	r *http.Request,
	head *bufio.Reader,
	body io.Reader,
	maxBytes int64,
) bool {
	started := time.Now()
	peeked, err := head.Peek(managedHeadPeekBytes)
	if err != nil {
		return false
	}
	envelope, offset, ok := parseManagedHead(peeked)
	if !ok || strings.TrimSpace(envelope.TenantID) == "" || strings.TrimSpace(envelope.ContainerRef) == "" {
		return false
	}
	// What follows the head is the request value and the envelope's closing brace.
	valueLength := r.ContentLength - int64(offset) - 1
	if valueLength < 2 {
		return false
	}
	if _, err = head.Discard(offset); err != nil {
		return false
	}
	buffered, _ := head.Peek(head.Buffered())
	source := io.MultiReader(bytes.NewReader(bytes.Clone(buffered)), body)
	headParsed := time.Now()

	stream := &managedPredictStream{source: source, remaining: valueLength}
	result, err := controller.InferenceCommandServiceInterface.PredictPreparedInferenceModel(
		r.Context(), envelope.TenantID, envelope.ContainerRef,
		dockerInferenceTypes.PredictRequest{Stream: stream, StreamLength: valueLength},
	)
	stream.detach()
	finished := time.Now()

	complete := stream.remaining == 0
	if !complete && stream.sourceErr == nil {
		// Read the rest of the upload so the client receives this answer
		// instead of a connection reset in the middle of its body.
		if _, drainErr := io.Copy(io.Discard, source); drainErr != nil {
			stream.sourceErr = drainErr
		}
	}

	status := http.StatusOK
	outcome := "success"
	switch {
	case requestbody.IsTooLarge(stream.sourceErr):
		status, outcome = http.StatusRequestEntityTooLarge, "too_large"
		requestbody.ObserveRejection(r, maxBytes, "internal_inference")
		requestbody.WriteTooLarge(w)
	case stream.sourceErr != nil:
		// The upload was cut short or its ending is not `}}`.
		status, outcome = http.StatusBadRequest, "invalid_body"
		writeManagedInferenceResponse(w, status, "Invalid payload request.", apiError.InvalidRequestPayload)
	case err != nil:
		var notReady interface{ ModelNotReady() bool }
		interrupted := !stream.firstRead.IsZero() && !complete
		recorder := &statusRecorder{ResponseWriter: w}
		if writeInferenceManagerError(recorder, err) {
			status, outcome = recorder.status, "manager_unavailable"
		} else if interrupted || (errors.As(err, &notReady) && notReady.ModelNotReady()) {
			status, outcome = http.StatusBadGateway, "model_interrupted"
			writeManagedInferenceResponse(w, status,
				"Inference runtime was not available for the whole request; retry it.", apiError.DockerInferenceError)
		} else {
			writeManagedPredictError(recorder, err)
			status, outcome = recorder.status, "failed"
		}
	default:
		response := viewmodels.HTTPResponseVM{
			Status:  http.StatusOK,
			Success: true,
			Message: "Successfully applied prediction to inference model.",
			Data:    result.Data,
		}
		response.JSON(w)
	}

	managedPredictStreamsTotal.Add(outcome, 1)
	admission, forward := finished.Sub(headParsed), time.Duration(0)
	if !stream.firstRead.IsZero() {
		admission = stream.firstRead.Sub(headParsed)
		end := stream.lastRead
		if end.IsZero() {
			end = finished
		}
		forward = end.Sub(stream.firstRead)
	}
	log.Printf(
		"[inference] event=predict_stream request_id=%s outcome=%s status=%d bytes=%d forwarded_bytes=%d "+
			"head_ms=%d admission_wait_ms=%d forward_ms=%d total_ms=%d",
		middleware.GetReqID(r.Context()), outcome, status, valueLength, stream.forwarded,
		headParsed.Sub(started).Milliseconds(), admission.Milliseconds(), forward.Milliseconds(),
		time.Since(started).Milliseconds(),
	)
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
