package requestbody

import (
	"encoding/json"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

const (
	DefaultAPIMaxBytes         int64         = 16 * 1024 * 1024
	DefaultDICOMWebMaxBytes    int64         = 6 * 1024 * 1024 * 1024
	DefaultDICOMWebReadTimeout time.Duration = 2 * time.Hour
)

var ErrTooLarge = errors.New("request body too large")

var requestBodyRejectionsTotal = expvar.NewMap("request_body_rejections_total")

var rejectionScopes = map[string]struct{}{
	"api": {}, "inference_predict": {}, "inference_ingestion_csv": {},
	"orchestrator_thread": {}, "orchestrator_chat": {}, "orchestrator_dicom": {},
	"dicomweb": {}, "registration": {},
}

type limitState struct {
	exceeded atomic.Bool
}

type stateReadCloser struct {
	io.ReadCloser
	state *limitState
}

func (body *stateReadCloser) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		body.state.exceeded.Store(true)
	}
	return count, err
}

type limitResponseWriter struct {
	http.ResponseWriter
	state       *limitState
	wroteHeader bool
	rejected    bool
}

func (writer *limitResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *limitResponseWriter) WriteHeader(status int) {
	if writer.state.exceeded.Load() {
		writer.reject()
		return
	}
	if writer.wroteHeader || writer.rejected {
		return
	}
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *limitResponseWriter) Write(data []byte) (int, error) {
	if writer.state.exceeded.Load() {
		writer.reject()
		return len(data), nil
	}
	if writer.rejected {
		return len(data), nil
	}
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *limitResponseWriter) reject() {
	if writer.rejected || writer.wroteHeader {
		return
	}
	writer.rejected = true
	writeTooLarge(writer.ResponseWriter)
}

// Limit rejects declared oversized bodies before the handler and keeps
// chunked bodies bounded. Streamed overflows receive a stable HTTP 413 when
// the downstream handler has not already committed a response.
func Limit(maxBytes int64) func(http.Handler) http.Handler {
	return LimitWithScope(maxBytes, "api")
}

// LimitWithScope applies Limit while using a bounded operational scope for
// rejection logs and metrics.
func LimitWithScope(maxBytes int64, scope string) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = DefaultAPIMaxBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !methodMayHaveBody(request.Method) || request.Body == nil || request.Body == http.NoBody {
				next.ServeHTTP(writer, request)
				return
			}
			if request.ContentLength > maxBytes {
				ObserveRejection(request, maxBytes, scope)
				writeTooLarge(writer)
				return
			}

			state := &limitState{}
			request.Body = &stateReadCloser{
				ReadCloser: http.MaxBytesReader(writer, request.Body, maxBytes),
				state:      state,
			}
			limitedWriter := &limitResponseWriter{ResponseWriter: writer, state: state}
			next.ServeHTTP(limitedWriter, request)
			if state.exceeded.Load() {
				ObserveRejection(request, maxBytes, scope)
				limitedWriter.reject()
			}
		})
	}
}

// DecodeJSON applies an endpoint-specific bound and rejects trailing JSON.
func DecodeJSON(writer http.ResponseWriter, request *http.Request, destination interface{}, maxBytes int64) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(destination); err != nil {
		return classifyDecodeError(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return classifyDecodeError(err)
	}
	return nil
}

func classifyDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return ErrTooLarge
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func IsTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.Is(err, ErrTooLarge) || errors.As(err, &maxBytesError)
}

func WriteTooLarge(writer http.ResponseWriter) {
	writeTooLarge(writer)
}

func writeTooLarge(writer http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusRequestEntityTooLarge,
		Success:   false,
		Message:   "Request body is too large.",
		ErrorCode: apiError.RequestBodyTooLarge,
	}
	response.JSON(writer)
}

// ObserveRejection emits bounded operational metadata and a bounded expvar
// counter. It deliberately excludes URLs, identifiers, and request payloads.
func ObserveRejection(request *http.Request, maxBytes int64, scope string) {
	if _, allowed := rejectionScopes[scope]; !allowed {
		scope = "unknown"
	}
	reason := "streamed"
	if request.ContentLength > maxBytes {
		reason = "declared_length"
	}
	requestBodyRejectionsTotal.Add(fmt.Sprintf("scope=%s,reason=%s", scope, reason), 1)
	log.Printf(
		"[security] event=request_body_rejected scope=%s request_id=%s method=%s content_length=%d max_bytes=%d",
		scope,
		middleware.GetReqID(request.Context()),
		request.Method,
		request.ContentLength,
		maxBytes,
	)
}

func methodMayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func PositiveInt64FromEnvironment(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// WithReadDeadline extends the server-wide read deadline for authenticated,
// explicitly size-bounded clinical upload routes without allowing a client to
// hold the request open indefinitely.
func WithReadDeadline(timeout time.Duration) func(http.Handler) http.Handler {
	if timeout <= 0 {
		timeout = DefaultDICOMWebReadTimeout
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			deadline := time.Now().Add(timeout)
			if err := http.NewResponseController(writer).SetReadDeadline(deadline); err != nil {
				log.Printf("cannot extend clinical upload read deadline: %v", err)
			}
			next.ServeHTTP(writer, request)
		})
	}
}
